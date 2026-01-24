package generate

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
)

func TestNewCmdGenerate(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdGenerate(f)

	expectedUse := "generate [versions...]"
	if cmd.Use != expectedUse {
		t.Errorf("expected Use '%s', got '%s'", expectedUse, cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"skip-fetch", ""},
		{"cleanup", ""},
		{"output", "o"},
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
	skipFetchFlag := cmd.Flags().Lookup("skip-fetch")
	if skipFetchFlag.DefValue != "false" {
		t.Errorf("expected --skip-fetch default 'false', got '%s'", skipFetchFlag.DefValue)
	}

	cleanupFlag := cmd.Flags().Lookup("cleanup")
	if cleanupFlag.DefValue != "true" {
		t.Errorf("expected --cleanup default 'true', got '%s'", cleanupFlag.DefValue)
	}

	outputFlag := cmd.Flags().Lookup("output")
	if outputFlag.DefValue != "" {
		t.Errorf("expected --output default '', got '%s'", outputFlag.DefValue)
	}
}
