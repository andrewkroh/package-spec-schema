// Licensed to Elasticsearch B.V. under one or more agreements.
// Elasticsearch B.V. licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/util"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

var (
	workDir  string // Directory where package-spec is stored.
	outDir   string // Directory where versioned directories containing schemas are written.
	dialect  string // JSON Schema dialect that the package-specs implement. Applied as $schema to all schemas.
	baseURI  string // Base URI to apply to schema $ids.
	gitURL   string // Git clone URL.
	gitRef   string // Git reference from which schemas will be generated.
	gitFetch bool   // Perform a git fetch when clone directory already exists.
)

func init() {
	flag.StringVar(&workDir, "w", ".package-spec-schema", "working directory")
	flag.StringVar(&outDir, "o", ".", "output directory")
	flag.StringVar(&dialect, "d", "https://json-schema.org/draft/2020-12/schema", "json schema dialect")
	flag.StringVar(&baseURI, "base-uri", "https://schemas.elastic.dev/package-spec", "base URI to apply to schema $ids")
	flag.StringVar(&gitURL, "git-url", "https://github.com/elastic/package-spec.git", "git clone URL")
	flag.StringVar(&gitRef, "git-ref", "", "git ref of package-spec, defaults to all version tags")
	flag.BoolVar(&gitFetch, "git-fetch", false, "git fetch new changes from package-spec")
}

func main() {
	flag.Parse()

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	git, err := NewGitRepository(gitURL, workDir, gitFetch)
	if err != nil {
		return err
	}

	// Get release tags.
	var gitRefs []*plumbing.Reference
	if gitRef != "" {
		hash, err := git.ResolveReference(gitRef)
		if err != nil {
			return err
		}
		gitRefs = append(gitRefs, plumbing.NewReferenceFromStrings(gitRef, hash.String()))
	} else {
		gitRefs, err = git.GetReleaseTags()
		if err != nil {
			return err
		}
	}

	for _, ref := range gitRefs {
		if err := writeSchemas(git, ref); err != nil {
			return err
		}
	}
	return nil
}

func writeSchemas(git *GitRepository, ref *plumbing.Reference) error {
	ver := ref.Name().String()
	if v := tagToSemver(ref); v != nil {
		ver = v.String()
	}
	dir := filepath.Join(outDir, ver, "jsonschema")

	if err := git.Checkout(ref); err != nil {
		return err
	}

	wt, err := git.Worktree()
	if err != nil {
		return err
	}

	repoPath, err := getSpecPath(wt.Filesystem)
	if err != nil {
		return err
	}

	if err := os.RemoveAll(dir); err != nil {
		return err
	}

	var written []string
	err = util.Walk(wt.Filesystem, repoPath, func(path string, info os.FileInfo, walkErr error) (err error) {
		if walkErr != nil {
			return walkErr
		}
		// The pseudo JSON Schema files have a .spec.yml suffix.
		if !strings.HasSuffix(filepath.Base(path), ".spec.yml") {
			return nil
		}

		f, err := wt.Filesystem.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}()

		// Get the schema file path relative directory containing the specs.
		relPath := strings.TrimPrefix(filepath.ToSlash(path), filepath.ToSlash(repoPath)+"/")
		relPath = strings.Replace(relPath, ".spec.yml", ".jsonschema.json", 1)

		written = append(written, relPath)
		return writeSchema(relPath, f, dir, ver)
	})
	if err != nil {
		return err
	}

	// Don't overwrite the root manifest.jsonschema.json that exists in <=1.7.1.
	if !slices.Contains(written, "manifest.jsonschema.json") {
		b, err := combinedManifestSchema(written, ver)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "manifest.jsonschema.json"), b, 0o600)
	}
	return nil
}

func writeSchema(relPath string, r io.Reader, destDir, version string) error {
	// Convert the YAML to JSON with some necessary cleanup.
	buf := new(bytes.Buffer)
	if err := convertSpecYAMLToJSONSchema(relPath, r, buf, version); err != nil {
		return fmt.Errorf("failed converting spec.yml file to JSON schema for %q: %w", relPath, err)
	}

	// Write to disk.
	destFile := filepath.Join(destDir, relPath)
	if err := os.MkdirAll(filepath.Dir(destFile), 0o700); err != nil {
		return err
	}
	return os.WriteFile(destFile, buf.Bytes(), 0o600)
}

func convertSpecYAMLToJSONSchema(path string, r io.Reader, w io.Writer, version string) error {
	var m map[string]any
	dec := yaml.NewDecoder(r)
	if err := dec.Decode(&m); err != nil {
		return fmt.Errorf("failed to decode YAML: %w", err)
	}

	v, ok := m["spec"]
	if !ok {
		return errors.New("spec key not found in YAML")
	}
	spec, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("spec is not an object, got %T", v)
	}

	if err := patchSchema(version, path, spec); err != nil {
		return fmt.Errorf("failed to patch schema: %w", err)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(spec)
}

// patchSchema applies schema patches.
func patchSchema(version, relativePath string, spec map[string]any) error {
	// Add a schema dialect.
	spec["$schema"] = dialect

	// Add a base URI to aid tools in resolving relative $refs.
	id, err := schemaID(version, relativePath)
	if err != nil {
		return fmt.Errorf("failed to make schema id: %w", err)
	}
	spec["$id"] = id

	// Apply all other schema patches in a single pass.
	return patchSchemaInPlace(spec)
}

// patchSchemaInPlace applies all schema transformations in a single recursive pass:
//
//   - removes $id fields (except root level)
//   - replace .spec.yml file naming with .jsonschema.json
//   - URI encodes $ref values
//   - removes additionalProperties: true
func patchSchemaInPlace(v any) error {
	return patchSchemaInPlaceRecursive(v, true)
}

func patchSchemaInPlaceRecursive(v any, isRoot bool) error {
	switch obj := v.(type) {
	case map[string]any:
		for key, value := range obj {
			switch key {
			case "$id":
				// Remove $id fields except at root level
				if !isRoot {
					delete(obj, "$id")
				}
			case "$ref":
				if refStr, ok := value.(string); ok {
					// Replace .spec.yml file naming with .jsonschema.json
					refStr = strings.Replace(refStr, ".spec.yml", ".jsonschema.json", 1)

					// URI encode $ref fragment values
					var err error
					refStr, err = encodeURIFragment(refStr)
					if err != nil {
						return fmt.Errorf("failed to encode $ref %q: %w", refStr, err)
					}

					obj[key] = refStr
				}
			case "additionalProperties":
				// Remove additionalProperties: true
				if b, ok := value.(bool); ok && b {
					delete(obj, key)
				}
				// Handle additionalProperties as an object.
				fallthrough
			default:
				// Recursively process nested values
				if err := patchSchemaInPlaceRecursive(value, false); err != nil {
					return err
				}
			}
		}
	case []any:
		for _, item := range obj {
			if err := patchSchemaInPlaceRecursive(item, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// encodeURIFragment encodes specific characters in URI fragments.
func encodeURIFragment(ref string) (string, error) {
	if !strings.Contains(ref, "#") {
		return ref, nil
	}

	parts := strings.SplitN(ref, "#", 2)
	if len(parts) != 2 {
		return ref, nil
	}

	base, fragment := parts[0], parts[1]
	if fragment == "" {
		return ref, nil
	}

	// Only encode specific characters that need encoding in URI fragments.
	encodedFragment := fragment
	encodedFragment = strings.ReplaceAll(encodedFragment, "^", "%5E")

	return base + "#" + encodedFragment, nil
}

// getSpecPath searches for the repository path that contains the specifications.
// The location has varied over time, so this determines which of the two
// locations to use.
func getSpecPath(fs billy.Filesystem) (string, error) {
	search := []string{
		"spec",
		"versions/1",
	}

	for _, path := range search {
		info, _ := fs.Stat(path)
		if info != nil {
			return path, nil
		}
	}
	return "", errors.New("no spec found")
}

func combinedManifestSchema(files []string, version string) ([]byte, error) {
	manifestTypes := map[string]string{
		"content":     "content/manifest.jsonschema.json",
		"input":       "input/manifest.jsonschema.json",
		"integration": "integration/manifest.jsonschema.json",
	}
	maps.DeleteFunc(manifestTypes, func(typ, path string) bool {
		return !slices.ContainsFunc(files, func(s string) bool {
			return strings.HasSuffix(filepath.ToSlash(s), path)
		})
	})
	if len(manifestTypes) == 0 {
		return nil, errors.New("no manifest types found")
	}

	id, err := schemaID(version, "manifest.jsonschema.json")
	if err != nil {
		return nil, err
	}

	s := jsonschema.Schema{
		Schema:      "https://json-schema.org/draft/2020-12/schema",
		ID:          id,
		Title:       "Package Manifest",
		Description: "Schema for package manifests.",
		Type:        "object",
		Required:    []string{"type"},
		Properties: map[string]*jsonschema.Schema{
			"type": {
				Type: "string",
				Enum: []interface{}{slices.Sorted(maps.Keys(manifestTypes))},
			},
		},
		Defs: map[string]*jsonschema.Schema{},
	}

	for _, manifestType := range slices.Sorted(maps.Keys(manifestTypes)) {
		definitionName := manifestType + "-manifest"
		s.AllOf = append(s.AllOf, &jsonschema.Schema{
			If: &jsonschema.Schema{
				Properties: map[string]*jsonschema.Schema{
					"type": {Const: jsonschema.Ptr(any(manifestType))},
				},
			},
			Then: &jsonschema.Schema{
				Ref: "#/$defs/" + definitionName,
			},
		})
		s.Defs[definitionName] = &jsonschema.Schema{Ref: "./" + manifestTypes[manifestType]}
	}

	return json.MarshalIndent(s, "", "  ")
}

func schemaID(version, relativePath string) (string, error) {
	u, err := url.Parse(baseURI)
	if err != nil {
		return "", fmt.Errorf("failed to parse base-uri: %w", err)
	}
	u.Path = path.Join(u.Path, version, relativePath)
	return u.String(), nil
}
