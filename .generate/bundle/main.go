// Licensed to Elasticsearch B.V. under one or more agreements.
// Elasticsearch B.V. licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	inDir  string // Directory containing the JSON schemas.
	outDir string // Directory where to write bundled schemas.
)

func init() {
	flag.StringVar(&inDir, "i", "", "input directory containing JSON Schema files")
	flag.StringVar(&outDir, "o", "", "output directory")
}

func main() {
	flag.Parse()

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if inDir == "" {
		return errors.New("no input dir specified")
	}
	if outDir == "" {
		return errors.New("no output dir specified")
	}

	_, err := exec.LookPath("jsonschema")
	if err != nil {
		return errors.New("jsonschema tool not found in $PATH")
	}

	// Find the .jsonschema.json files.
	schemas, err := findFiles(inDir, func(path string, _ os.FileInfo) bool {
		return strings.HasSuffix(path, ".jsonschema.json")
	})
	if err != nil {
		return fmt.Errorf("failed finding files: %w", err)
	}

	// Convert each file into a bundle.
	for _, schema := range schemas {
		if err = bundleSchema(schema, inDir, outDir); err != nil {
			return fmt.Errorf("bundling %q failed: %w", schema, err)
		}
	}
	return nil
}

func bundleSchema(schemaPath, inDir, outDir string) error {
	// https://github.com/sourcemeta/jsonschema/blob/main/docs/bundle.markdown
	args := []string{
		"bundle",
		schemaPath,
		"--resolve", inDir,
		"--without-id",
	}
	out, err := jsonschemaExec(args...)
	if err != nil {
		return err
	}

	outFile := filepath.Join(outDir, trimFilePrefix(schemaPath, inDir))

	if err = os.MkdirAll(filepath.Dir(outFile), 0o700); err != nil {
		return err
	}
	if err = os.WriteFile(outFile, out, 0o600); err != nil {
		return err
	}
	return nil
}

// findFiles walks a directory and returns all files that match the given predicate.
func findFiles(dir string, match func(path string, info os.FileInfo) bool) ([]string, error) {
	var out []string
	err := filepath.Walk(dir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !match(path, info) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func jsonschemaExec(args ...string) (stdout []byte, err error) {
	cmd := exec.Command("jsonschema", args...)
	outBuf := new(bytes.Buffer)
	cmd.Stdout = outBuf
	errBuf := new(bytes.Buffer)
	cmd.Stderr = errBuf

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errBuf.Bytes())
		return nil, fmt.Errorf("failed running jsonschema %s: %w", strings.Join(args, " "), err)
	}
	return outBuf.Bytes(), nil
}

func trimFilePrefix(path, prefix string) string {
	path = filepath.ToSlash(path)
	prefix = filepath.ToSlash(prefix)
	return strings.TrimPrefix(path, prefix+"/")
}
