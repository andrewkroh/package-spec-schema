// Licensed to Elasticsearch B.V. under one or more agreements.
// Elasticsearch B.V. licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/coreos/go-semver/semver"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

var slugSanitizer = strings.NewReplacer("/", "_", " ", "")

// GitRepository wraps git repository operations.
type GitRepository struct {
	repo *git.Repository
}

// NewGitRepository opens or clones the remote repository.
func NewGitRepository(githubURL, workDir string, fetch bool) (*GitRepository, error) {
	repoURL, err := url.Parse(githubURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse repository URL: %w", err)
	}

	repoDir := filepath.Join(
		workDir,
		"git",
		slugSanitizer.Replace(strings.TrimSuffix(strings.TrimPrefix(repoURL.Path, "/"), ".git")),
	)

	// Open or clone.
	repo, err := git.PlainOpen(repoDir)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		log.Printf("Cloning into %v.", repoDir)
		if err := os.MkdirAll(repoDir, 0o700); err != nil {
			return nil, fmt.Errorf("failed to create directory: %w", err)
		}
		repo, err = git.PlainClone(repoDir, false, &git.CloneOptions{
			URL: githubURL,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open/clone repository: %w", err)
	}

	gitRepo := &GitRepository{repo: repo}

	if fetch {
		if err := gitRepo.Fetch(); err != nil {
			return nil, err
		}
	}

	return gitRepo, nil
}

// Fetch retrieves the latest changes from the remote repository.
func (g *GitRepository) Fetch() error {
	log.Println("Fetching latest changes.")
	err := g.repo.Fetch(&git.FetchOptions{})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("failed in git fetch: %w", err)
	}
	log.Println("Fetch completed.")
	return nil
}

func (g *GitRepository) ResolveReference(ref string) (*plumbing.Reference, error) {
	// First try to resolve as a revision (handles commits, branches, tags)
	hash, err := g.repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve revision %q: %w", ref, err)
	}

	// Get all references and find one that points to this hash
	refs, err := g.repo.References()
	if err != nil {
		return nil, fmt.Errorf("failed to get references: %w", err)
	}

	var foundRef *plumbing.Reference
	err = refs.ForEach(func(r *plumbing.Reference) error {
		if r.Hash() == *hash {
			foundRef = r
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate references: %w", err)
	}

	// If we found a reference pointing to the hash, return it
	if foundRef != nil {
		return foundRef, nil
	}

	// If no reference found, create a hash reference
	return plumbing.NewHashReference(plumbing.ReferenceName("refs/heads/"+ref), *hash), nil
}

// GetReleaseTags returns all release tags sorted by semantic version.
func (g *GitRepository) GetReleaseTags() ([]*plumbing.Reference, error) {
	tagItr, err := g.repo.Tags()
	if err != nil {
		return nil, fmt.Errorf("failed to get tags: %w", err)
	}

	versionToRef := map[*semver.Version]*plumbing.Reference{}
	err = tagItr.ForEach(func(reference *plumbing.Reference) error {
		ver := tagToSemver(reference)
		if ver == nil || ver.PreRelease != "" {
			return nil
		}

		versionToRef[ver] = reference
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate tags: %w", err)
	}

	// Sort versions
	versions := slices.Collect(maps.Keys(versionToRef))
	semver.Sort(versions)

	out := make([]*plumbing.Reference, 0, len(versions))
	for _, ver := range versions {
		out = append(out, versionToRef[ver])
	}

	return out, nil
}

// Checkout checks out a specific git reference.
func (g *GitRepository) Checkout(ref *plumbing.Reference) error {
	wt, err := g.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	log.Println("Cleaning repo.")
	err = wt.Clean(&git.CleanOptions{
		Dir: true,
	})
	if err != nil {
		return fmt.Errorf("clean failed: %w", err)
	}

	log.Printf("Checking out %v.", ref)
	err = wt.Checkout(&git.CheckoutOptions{
		Hash:  ref.Hash(),
		Force: true,
	})
	if err != nil {
		return fmt.Errorf("checkout failed for %s: %w", ref, err)
	}
	log.Println("Checkout completed.")

	return nil
}

// Worktree returns the git worktree.
func (g *GitRepository) Worktree() (*git.Worktree, error) {
	return g.repo.Worktree()
}

// tagToSemver converts a git tag reference to a semantic version.
// Returns nil if the tag is not a valid semantic version.
func tagToSemver(ref *plumbing.Reference) *semver.Version {
	tag := ref.Name().Short()

	if !strings.HasPrefix(tag, "v") {
		return nil
	}
	tag = strings.TrimPrefix(tag, "v")

	ver, err := semver.NewVersion(tag)
	if err != nil {
		return nil
	}

	return ver
}
