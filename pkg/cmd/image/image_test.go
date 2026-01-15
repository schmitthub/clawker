package image

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
)

func TestNewCmdImage(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdImage(f)

	// Verify command basics
	if cmd.Use != "image" {
		t.Errorf("expected Use 'image', got '%s'", cmd.Use)
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
}

func TestNewCmdImage_Subcommands(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdImage(f)

	// Currently no subcommands are registered (Task 3.4 will add them)
	// This test will be expanded when subcommands are added
	subcommands := cmd.Commands()

	// For now, expect no subcommands
	if len(subcommands) != 0 {
		t.Errorf("expected 0 subcommands (none registered yet), got %d", len(subcommands))
	}
}
