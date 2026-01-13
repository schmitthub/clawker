package prune

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
)

func TestNewCmdPrune(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdPrune(f)

	if cmd.Use != "prune" {
		t.Errorf("expected Use 'prune', got '%s'", cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
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

	// Check default values
	allFlag := cmd.Flags().Lookup("all")
	if allFlag.DefValue != "false" {
		t.Errorf("expected --all default 'false', got '%s'", allFlag.DefValue)
	}

	forceFlag := cmd.Flags().Lookup("force")
	if forceFlag.DefValue != "false" {
		t.Errorf("expected --force default 'false', got '%s'", forceFlag.DefValue)
	}
}
