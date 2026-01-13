package shell

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
)

func TestNewCmdShell(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdShell(f)

	if cmd.Use != "shell" {
		t.Errorf("expected Use 'shell', got '%s'", cmd.Use)
	}

	// Check aliases
	if len(cmd.Aliases) != 1 || cmd.Aliases[0] != "sh" {
		t.Errorf("expected Aliases ['sh'], got %v", cmd.Aliases)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"agent", ""},
		{"shell", "s"},
		{"user", "u"},
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

	// Check default shell value
	shellFlag := cmd.Flags().Lookup("shell")
	if shellFlag.DefValue != "/bin/bash" {
		t.Errorf("expected --shell default '/bin/bash', got '%s'", shellFlag.DefValue)
	}
}
