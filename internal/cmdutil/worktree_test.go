package cmdutil

import (
	"strings"
	"testing"
)

func TestParseWorktreeFlag(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		agentName  string
		wantBranch string
		wantBase   string
		wantErr    string
	}{
		{
			name:       "empty value generates branch name",
			value:      "",
			agentName:  "dev",
			wantBranch: "clawker-dev-", // prefix only, timestamp varies
		},
		{
			name:       "empty value with empty agent",
			value:      "",
			agentName:  "",
			wantBranch: "clawker-session-",
		},
		{
			name:       "simple branch name",
			value:      "feat-42",
			agentName:  "dev",
			wantBranch: "feat-42",
			wantBase:   "",
		},
		{
			name:       "branch with slashes",
			value:      "feature/foo/bar",
			agentName:  "dev",
			wantBranch: "feature/foo/bar",
			wantBase:   "",
		},
		{
			name:       "branch:base syntax",
			value:      "feat-42:main",
			agentName:  "dev",
			wantBranch: "feat-42",
			wantBase:   "main",
		},
		{
			name:       "branch:base with slashes",
			value:      "feature/new:develop/v2",
			agentName:  "dev",
			wantBranch: "feature/new",
			wantBase:   "develop/v2",
		},
		{
			name:      "invalid branch with shell metachar semicolon",
			value:     "feat;rm -rf /",
			agentName: "dev",
			wantErr:   "invalid characters",
		},
		{
			name:      "invalid branch with shell metachar backtick",
			value:     "feat`whoami`",
			agentName: "dev",
			wantErr:   "invalid characters",
		},
		{
			name:      "invalid branch with shell metachar dollar",
			value:     "feat$HOME",
			agentName: "dev",
			wantErr:   "invalid characters",
		},
		{
			name:      "invalid branch starting with hyphen",
			value:     "-feat",
			agentName: "dev",
			wantErr:   "invalid characters",
		},
		{
			name:      "invalid branch ending with .lock",
			value:     "feat.lock",
			agentName: "dev",
			wantErr:   "cannot end with .lock",
		},
		{
			name:      "invalid branch with consecutive dots",
			value:     "feat..bar",
			agentName: "dev",
			wantErr:   "cannot contain consecutive dots",
		},
		{
			name:      "invalid branch with @{",
			value:     "feat@{0}",
			agentName: "dev",
			wantErr:   "invalid characters", // @{ contains invalid chars (@ not allowed)
		},
		{
			name:      "invalid base branch",
			value:     "feat:main;evil",
			agentName: "dev",
			wantErr:   "invalid base branch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := ParseWorktreeFlag(tt.value, tt.agentName)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// For auto-generated names, just check prefix
			if tt.value == "" {
				if !strings.HasPrefix(spec.Branch, tt.wantBranch) {
					t.Errorf("expected branch prefix %q, got %q", tt.wantBranch, spec.Branch)
				}
			} else {
				if spec.Branch != tt.wantBranch {
					t.Errorf("expected branch %q, got %q", tt.wantBranch, spec.Branch)
				}
			}

			if spec.Base != tt.wantBase {
				t.Errorf("expected base %q, got %q", tt.wantBase, spec.Base)
			}
		})
	}
}

func TestValidateBranchName(t *testing.T) {
	validNames := []string{
		"main",
		"feature/new-thing",
		"release/v1.0.0",
		"fix_bug_123",
		"feat.test",
		"a",
		"A1",
	}

	for _, name := range validNames {
		t.Run("valid_"+name, func(t *testing.T) {
			if err := validateBranchName(name); err != nil {
				t.Errorf("expected %q to be valid, got error: %v", name, err)
			}
		})
	}

	invalidNames := []struct {
		name    string
		wantErr string
	}{
		{"", "cannot be empty"},
		{"-start", "invalid characters"},
		{"end.lock", "cannot end with .lock"},
		{"has..dots", "cannot contain consecutive dots"},
		{"has@{ref}", "invalid characters"},
		{"has space", "invalid characters"},
		{"has\ttab", "invalid characters"},
		{"has\nnewline", "invalid characters"},
	}

	for _, tc := range invalidNames {
		t.Run("invalid_"+tc.name, func(t *testing.T) {
			err := validateBranchName(tc.name)
			if err == nil {
				t.Errorf("expected error for %q, got nil", tc.name)
				return
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}
