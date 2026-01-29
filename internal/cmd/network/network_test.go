package network

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestNewCmdNetwork(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{Version: "1.0.0", Commit: "abc123", IOStreams: tio.IOStreams}
	cmd := NewCmdNetwork(f)

	// Verify command basics
	if cmd.Use != "network" {
		t.Errorf("expected Use 'network', got '%s'", cmd.Use)
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

func TestNewCmdNetwork_Subcommands(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{Version: "1.0.0", Commit: "abc123", IOStreams: tio.IOStreams}
	cmd := NewCmdNetwork(f)

	subcommands := cmd.Commands()

	// Expect 5 subcommands: create, inspect, list, prune, remove
	if len(subcommands) != 5 {
		t.Errorf("expected 5 subcommands, got %d", len(subcommands))
	}

	// Verify subcommand names (sorted alphabetically by Cobra)
	expectedSubcommands := []string{"create", "inspect", "list", "prune", "remove"}
	for i, expected := range expectedSubcommands {
		if i >= len(subcommands) {
			t.Errorf("missing subcommand: %s", expected)
			continue
		}
		if subcommands[i].Name() != expected {
			t.Errorf("expected subcommand %d to be '%s', got '%s'", i, expected, subcommands[i].Name())
		}
	}
}
