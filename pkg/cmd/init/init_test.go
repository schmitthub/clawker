package init

import (
	"testing"

	"github.com/schmitthub/claucker/pkg/cmdutil"
)

func TestNewCmdInit(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdInit(f)

	if cmd.Use != "init [project-name]" {
		t.Errorf("expected Use 'init [project-name]', got '%s'", cmd.Use)
	}

	// Check force flag exists
	forceFlag := cmd.Flags().Lookup("force")
	if forceFlag == nil {
		t.Error("expected --force flag to exist")
	}
	if forceFlag.Shorthand != "f" {
		t.Errorf("expected --force shorthand 'f', got '%s'", forceFlag.Shorthand)
	}
}
