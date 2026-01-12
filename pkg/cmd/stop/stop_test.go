package stop

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
)

func TestNewCmdStop(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdStop(f)

	if cmd.Use != "stop" {
		t.Errorf("expected Use 'stop', got '%s'", cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"agent", ""},
		{"clean", ""},
		{"force", "f"},
		{"timeout", "t"},
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
