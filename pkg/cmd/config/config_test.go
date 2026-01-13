package config

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
)

func TestNewCmdConfig(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
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
