package up

import (
	"testing"

	"github.com/schmitthub/claucker/pkg/cmdutil"
)

func TestNewCmdUp(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdUp(f)

	if cmd.Use != "up" {
		t.Errorf("expected Use 'up', got '%s'", cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"mode", "m"},
		{"build", ""},
		{"detach", ""},
		{"clean", ""},
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
