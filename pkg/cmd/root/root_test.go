package root

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
)

func TestNewCmdRoot(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRoot(f)

	if cmd.Use != "clawker" {
		t.Errorf("expected Use 'clawker', got '%s'", cmd.Use)
	}

	if cmd.Version != "1.0.0" {
		t.Errorf("expected Version '1.0.0', got '%s'", cmd.Version)
	}

	// Check subcommands are registered
	// Note: 'start' is now an alias to 'run', not a separate command
	// Note: 'shell' was removed (use 'run --shell' instead)
	subcommands := cmd.Commands()
	expectedCmds := map[string]bool{
		"init":   false,
		"build":  false,
		"run":    false, // Also aliased as 'start'
		"stop":   false,
		"logs":   false,
		"config": false,
	}

	for _, sub := range subcommands {
		// Use Name() to get just the command name without arguments
		if _, ok := expectedCmds[sub.Name()]; ok {
			expectedCmds[sub.Name()] = true
		}
	}

	for name, found := range expectedCmds {
		if !found {
			t.Errorf("expected subcommand '%s' to be registered", name)
		}
	}
}

func TestNewCmdRoot_GlobalFlags(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRoot(f)

	// Check debug flag exists
	debugFlag := cmd.PersistentFlags().Lookup("debug")
	if debugFlag == nil {
		t.Error("expected --debug flag to exist")
	}

	// Check workdir flag exists
	workdirFlag := cmd.PersistentFlags().Lookup("workdir")
	if workdirFlag == nil {
		t.Error("expected --workdir flag to exist")
	}
}
