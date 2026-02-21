// Package git provides Git repository operations, including worktree management.
// This is a Tier 1 (Leaf) package — it imports only stdlib and go-git, NOT any internal packages.
package git

import (
	"github.com/go-git/go-git/v6/plumbing"
)

// WorktreeInfo contains information about a git worktree.
type WorktreeInfo struct {
	// Name is the worktree name (typically the branch name).
	Name string

	// Slug is the caller-provided identifier for this worktree entry.
	// Preserved from WorktreeDirEntry to avoid re-computation.
	Slug string

	// Path is the filesystem path to the worktree directory.
	Path string

	// Head is the commit hash the worktree is checked out to.
	Head plumbing.Hash

	// Branch is the branch reference if the worktree is not detached.
	// Empty string for detached HEAD state.
	Branch string

	// IsDetached indicates whether the worktree has a detached HEAD.
	IsDetached bool

	// Error indicates any error encountered while reading worktree info.
	// If non-nil, other fields may be incomplete/zero-valued.
	Error error
}

// WorktreeDirEntry contains information about a worktree directory.
// This is the directory-level info (name, slug, path), separate from git metadata.
type WorktreeDirEntry struct {
	// Name is the original name (typically a branch name with slashes).
	Name string
	// Slug is the filesystem-safe version of the name.
	Slug string
	// Path is the absolute filesystem path to the worktree directory.
	Path string
}

// WorktreeDirProvider manages worktree directories for callers.
// Defined here for dependency inversion so the git package remains
// unconcerned with caller configuration/layout details.
type WorktreeDirProvider interface {
	// GetOrCreateWorktreeDir returns the path to a worktree directory,
	// creating it if it doesn't exist. The name is typically a branch name
	// which will be slugified for filesystem safety.
	GetOrCreateWorktreeDir(name string) (string, error)

	// GetWorktreeDir returns the path to an existing worktree directory.
	// Returns an error if the worktree directory doesn't exist.
	GetWorktreeDir(name string) (string, error)

	// DeleteWorktreeDir removes a worktree directory.
	// Returns an error if the directory doesn't exist.
	DeleteWorktreeDir(name string) error
}
