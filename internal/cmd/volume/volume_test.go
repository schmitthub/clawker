package volume

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestNewCmdVolume(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}
	cmd := NewCmdVolume(f)

	// Verify command basics
	if cmd.Use != "volume" {
		t.Errorf("expected Use 'volume', got '%s'", cmd.Use)
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

func TestNewCmdVolume_Subcommands(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}
	cmd := NewCmdVolume(f)

	subcommands := cmd.Commands()

	// Check expected subcommands are registered
	expectedSubcommands := []string{"create", "inspect", "list", "prune", "remove"}
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
