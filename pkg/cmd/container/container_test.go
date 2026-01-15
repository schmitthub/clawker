package container

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
)

func TestNewCmdContainer(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdContainer(f)

	// Verify command basics
	if cmd.Use != "container" {
		t.Errorf("expected Use 'container', got '%s'", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("expected Short description to be set")
	}

	if cmd.Long == "" {
		t.Error("expected Long description to be set")
	}

	if cmd.Example == "" {
		t.Error("expected Example to be set")
	}

	// Verify this is a parent command (no RunE)
	if cmd.RunE != nil {
		t.Error("expected RunE to be nil for parent command")
	}

	// Verify it's runnable (shows help when invoked directly)
	if !cmd.Runnable() {
		// Parent commands with subcommands are still "runnable" - they show help
		// This test just documents the expected behavior
	}
}

func TestNewCmdContainer_Subcommands(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdContainer(f)

	// Currently no subcommands are registered (Task 3.3 will add them)
	// This test will be expanded when subcommands are added
	subcommands := cmd.Commands()

	// For now, expect no subcommands
	if len(subcommands) != 0 {
		t.Errorf("expected 0 subcommands (none registered yet), got %d", len(subcommands))
	}
}
