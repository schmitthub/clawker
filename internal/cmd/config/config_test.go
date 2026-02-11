package config

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestNewCmdConfig(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}
	cmd := NewCmdConfig(f)

	if cmd.Use != "config" {
		t.Errorf("expected Use 'config', got '%s'", cmd.Use)
	}

	// Check check subcommand exists
	subcommands := cmd.Commands()
	foundCheck := false
	for _, sub := range subcommands {
		if sub.Use == "check" {
			foundCheck = true
			break
		}
	}

	if !foundCheck {
		t.Error("expected 'check' subcommand to be registered")
	}
}
