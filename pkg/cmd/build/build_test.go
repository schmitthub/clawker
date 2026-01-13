package build

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
)

func TestNewCmdBuild(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdBuild(f)

	if cmd.Use != "build" {
		t.Errorf("expected Use 'build', got '%s'", cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"no-cache", ""},
		{"dockerfile", ""},
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
	noCacheFlag := cmd.Flags().Lookup("no-cache")
	if noCacheFlag.DefValue != "false" {
		t.Errorf("expected --no-cache default 'false', got '%s'", noCacheFlag.DefValue)
	}

	dockerfileFlag := cmd.Flags().Lookup("dockerfile")
	if dockerfileFlag.DefValue != "" {
		t.Errorf("expected --dockerfile default '', got '%s'", dockerfileFlag.DefValue)
	}
}
