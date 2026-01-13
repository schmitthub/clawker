package remove

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
)

func TestNewCmdRemove(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	if cmd.Use != "remove" {
		t.Errorf("expected Use 'remove', got '%s'", cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"name", "n"},
		{"project", "p"},
		{"unused", "u"},
		{"all", "a"},
		{"force", "f"},
	}

	for _, fl := range flags {
		flag := cmd.Flags().Lookup(fl.name)
		if flag == nil {
			t.Errorf("expected --%s flag to exist", fl.name)
		}
		if fl.shorthand != "" && flag.Shorthand != fl.shorthand {
			t.Errorf("expected --%s shorthand '%s', got '%s'", fl.name, fl.shorthand, flag.Shorthand)
		}
	}
}

func TestNewCmdRemove_Aliases(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	expected := []string{"rm"}
	if len(cmd.Aliases) != len(expected) {
		t.Errorf("expected %d aliases, got %d", len(expected), len(cmd.Aliases))
	}
	for i, alias := range expected {
		if i >= len(cmd.Aliases) {
			break
		}
		if cmd.Aliases[i] != alias {
			t.Errorf("expected alias %d '%s', got '%s'", i, alias, cmd.Aliases[i])
		}
	}
}

func TestNewCmdRemove_DefaultValues(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	// Verify default values for key flags
	tests := []struct {
		name     string
		expected string
	}{
		{"unused", "false"},
		{"all", "false"},
		{"force", "false"},
	}

	for _, tt := range tests {
		flag := cmd.Flags().Lookup(tt.name)
		if flag == nil {
			t.Errorf("flag --%s not found", tt.name)
			continue
		}
		if flag.DefValue != tt.expected {
			t.Errorf("expected --%s default '%s', got '%s'", tt.name, tt.expected, flag.DefValue)
		}
	}
}
