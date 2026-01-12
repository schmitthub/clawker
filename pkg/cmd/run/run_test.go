package run

import (
	"testing"

	"github.com/schmitthub/claucker/pkg/cmdutil"
)

func TestNewCmdRun(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRun(f)

	expectedUse := "run [flags] [-- <command>...]"
	if cmd.Use != expectedUse {
		t.Errorf("expected Use '%s', got '%s'", expectedUse, cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"agent", ""},
		{"mode", "m"},
		{"build", ""},
		{"shell", ""},
		{"keep", ""},
		{"publish", "p"},
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
