package cmdutil

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// WorktreeSpec holds the parsed --worktree flag value.
type WorktreeSpec struct {
	Branch string // Branch name to use/create
	Base   string // Base branch (empty if not specified)
}

// validBranchNameRegex validates git branch names.
// Disallows shell metacharacters and git-special characters.
var validBranchNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)

// ParseWorktreeFlag parses the --worktree flag value.
//
// Syntax:
//   - Empty string: auto-generate branch name
//   - "branch": use existing or create from HEAD
//   - "branch:base": create branch from specified base
//
// Returns WorktreeSpec with Branch and Base fields.
// Returns error if branch name is invalid (contains shell metacharacters).
func ParseWorktreeFlag(value, agentName string) (*WorktreeSpec, error) {
	// Empty value means auto-generate
	if value == "" {
		branch := generateBranchName(agentName)
		return &WorktreeSpec{Branch: branch}, nil
	}

	// Check for colon syntax (branch:base)
	parts := strings.SplitN(value, ":", 2)
	branch := parts[0]
	var base string
	if len(parts) == 2 {
		base = parts[1]
	}

	// Validate branch name
	if err := validateBranchName(branch); err != nil {
		return nil, fmt.Errorf("invalid branch name %q: %w", branch, err)
	}

	// Validate base if specified
	if base != "" {
		if err := validateBranchName(base); err != nil {
			return nil, fmt.Errorf("invalid base branch %q: %w", base, err)
		}
	}

	return &WorktreeSpec{
		Branch: branch,
		Base:   base,
	}, nil
}

// generateBranchName creates an auto-generated branch name.
// Format: clawker-<agent>-<timestamp>
func generateBranchName(agentName string) string {
	timestamp := time.Now().Format("20060102-150405")
	if agentName == "" {
		agentName = "session"
	}
	return fmt.Sprintf("clawker-%s-%s", agentName, timestamp)
}

// validateBranchName checks if a branch name is safe.
// Rejects names containing shell metacharacters or git-special chars.
func validateBranchName(name string) error {
	if name == "" {
		return fmt.Errorf("branch name cannot be empty")
	}

	if !validBranchNameRegex.MatchString(name) {
		return fmt.Errorf("contains invalid characters (only alphanumeric, dots, underscores, hyphens, and slashes allowed)")
	}

	// Additional git-specific checks
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("cannot start with a hyphen")
	}
	if strings.HasSuffix(name, ".lock") {
		return fmt.Errorf("cannot end with .lock")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("cannot contain consecutive dots")
	}
	if strings.Contains(name, "@{") {
		return fmt.Errorf("cannot contain @{")
	}

	return nil
}
