package rm

import (
	"testing"

	"github.com/schmitthub/claucker/pkg/cmdutil"
)

func TestNewCmdRm(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRm(f)

	if cmd.Use != "rm" {
		t.Errorf("expected Use 'rm', got '%s'", cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"name", "n"},
		{"project", "p"},
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

func TestNewCmdRm_Aliases(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRm(f)

	expected := []string{"remove"}
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
