package root

import (
	"strings"
	"testing"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
)

func TestNewCmdRoot(t *testing.T) {
	f := cmdutil2.New("1.0.0", "abc123")
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
		// Top-level commands (shortcuts)
		"init":     false,
		"build":    false,
		"run":      false, // Alias for "container run"
		"start":    false, // Alias for "container start"
		"config":   false,
		"monitor":  false,
		"generate": false,
		// Management commands
		"container": false,
		"image":     false,
		"volume":    false,
		"network":   false,
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
	f := cmdutil2.New("1.0.0", "abc123")
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

func TestStateChangingCommandsRequireProject(t *testing.T) {
	// Hardcoded list of commands that MUST require project context.
	// Without this annotation, these commands could modify Docker resources
	// when run outside a project directory, potentially affecting unrelated containers.
	// If a command is accidentally removed from protection, this test will fail.
	requiredCommands := [][]string{
		// Container commands (state-modifying)
		{"container", "create"},
		{"container", "run"},
		{"container", "start"},
		{"container", "stop"},
		{"container", "restart"},
		{"container", "kill"},
		{"container", "pause"},
		{"container", "unpause"},
		{"container", "remove"},
		{"container", "exec"},
		{"container", "attach"},
		{"container", "cp"},
		{"container", "rename"},
		{"container", "update"},
		{"container", "wait"},
		// Image commands (state-modifying)
		{"image", "build"},
		{"image", "remove"},
		{"image", "prune"},
		// Volume commands (state-modifying)
		{"volume", "create"},
		{"volume", "remove"},
		{"volume", "prune"},
		// Network commands (state-modifying)
		{"network", "create"},
		{"network", "remove"},
		{"network", "prune"},
	}

	f := cmdutil2.New("1.0.0", "abc123")
	root := NewCmdRoot(f)

	for _, path := range requiredCommands {
		name := strings.Join(path, "/")
		t.Run(name, func(t *testing.T) {
			cmd, _, err := root.Find(path)
			if err != nil {
				t.Fatalf("command %s should exist: %v", name, err)
			}
			if cmd == nil {
				t.Fatalf("command %s should not be nil", name)
			}

			if !cmdutil2.CommandRequiresProject(cmd) {
				t.Errorf("command %s should have requiresProject annotation", name)
			}
		})
	}
}

func TestReadOnlyCommandsDoNotRequireProject(t *testing.T) {
	// Read-only commands should NOT require project context.
	// If a read-only command accidentally gets the annotation, users will be
	// unnecessarily prompted when just listing or inspecting resources.
	readOnlyCommands := [][]string{
		// Container read-only commands
		{"container", "list"},
		{"container", "logs"},
		{"container", "inspect"},
		{"container", "top"},
		{"container", "stats"},
		// Image read-only commands
		{"image", "list"},
		{"image", "inspect"},
		// Volume read-only commands
		{"volume", "list"},
		{"volume", "inspect"},
		// Network read-only commands
		{"network", "list"},
		{"network", "inspect"},
	}

	f := cmdutil2.New("1.0.0", "abc123")
	root := NewCmdRoot(f)

	for _, path := range readOnlyCommands {
		name := strings.Join(path, "/")
		t.Run(name, func(t *testing.T) {
			cmd, _, err := root.Find(path)
			if err != nil {
				t.Fatalf("command %s should exist: %v", name, err)
			}
			if cmd == nil {
				t.Fatalf("command %s should not be nil", name)
			}

			if cmdutil2.CommandRequiresProject(cmd) {
				t.Errorf("read-only command %s should NOT have requiresProject annotation", name)
			}
		})
	}
}
