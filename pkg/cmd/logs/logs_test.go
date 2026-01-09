package logs

import (
	"testing"

	"github.com/schmitthub/claucker/pkg/cmdutil"
)

func TestNewCmdLogs(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdLogs(f)

	if cmd.Use != "logs" {
		t.Errorf("expected Use 'logs', got '%s'", cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"follow", "f"},
		{"tail", ""},
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

	// Check default tail value
	tailFlag := cmd.Flags().Lookup("tail")
	if tailFlag.DefValue != "100" {
		t.Errorf("expected --tail default '100', got '%s'", tailFlag.DefValue)
	}
}
