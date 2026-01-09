package down

import (
	"testing"

	"github.com/schmitthub/claucker/pkg/cmdutil"
)

func TestNewCmdDown(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdDown(f)

	if cmd.Use != "down" {
		t.Errorf("expected Use 'down', got '%s'", cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
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
