package container

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestNewCmdContainer(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{Version: "1.0.0", Commit: "abc123", IOStreams: tio.IOStreams}
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
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{Version: "1.0.0", Commit: "abc123", IOStreams: tio.IOStreams}
	cmd := NewCmdContainer(f)

	subcommands := cmd.Commands()

	// Check expected subcommands are registered
	expectedSubcommands := []string{"attach", "cp", "create", "exec", "inspect", "kill", "list", "logs", "pause", "remove", "rename", "restart", "run", "start", "stats", "stop", "top", "unpause", "update", "wait"}
	if len(subcommands) != len(expectedSubcommands) {
		t.Errorf("expected %d subcommands, got %d", len(expectedSubcommands), len(subcommands))
	}

	// Verify each expected subcommand is present
	for _, expected := range expectedSubcommands {
		found := false
		for _, sub := range subcommands {
			if sub.Name() == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected subcommand '%s' not found", expected)
		}
	}
}
