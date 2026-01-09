package sh

import (
	"testing"

	"github.com/schmitthub/claucker/pkg/cmdutil"
)

func TestNewCmdSh(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdSh(f)

	if cmd.Use != "sh" {
		t.Errorf("expected Use 'sh', got '%s'", cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
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
