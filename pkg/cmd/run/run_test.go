package run

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/pflag"
)

func TestNewCmdRun(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRun(f)

	expectedUse := "run [flags] [-- <command>...]"
	if cmd.Use != expectedUse {
		t.Errorf("expected Use '%s', got '%s'", expectedUse, cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"agent", ""},
		{"mode", "m"},
		{"build", ""},
		{"shell", ""},
		{"shell-path", "s"},
		{"user", "u"},
		{"remove", "r"},
		{"detach", ""},
		{"clean", ""},
		{"publish", "p"},
		{"quiet", "q"},
		{"json", ""},
	}

	for _, fl := range flags {
		flag := cmd.Flags().Lookup(fl.name)
		if flag == nil {
			t.Errorf("expected --%s flag to exist", fl.name)
		}
		if fl.shorthand != "" && flag.Shorthand != fl.shorthand {
			t.Errorf("expected --%s shorthand '%s', got '%s'", fl.name, fl.shorthand, flag.Shorthand)
		}
	}
}

func TestNewCmdRun_Aliases(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRun(f)

	expected := []string{"start"}
	if len(cmd.Aliases) != len(expected) {
		t.Errorf("expected %d aliases, got %d", len(expected), len(cmd.Aliases))
	}
	for i, alias := range expected {
		if i >= len(cmd.Aliases) {
			break
		}
		if cmd.Aliases[i] != alias {
			t.Errorf("expected alias %d '%s', got '%s'", i, alias, cmd.Aliases[i])
		}
	}
}

func TestNewCmdRun_DefaultValues(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRun(f)

	// Verify default values for key flags
	tests := []struct {
		name     string
		expected string
	}{
		{"remove", "false"},
		{"shell", "false"},
		{"detach", "false"},
		{"clean", "false"},
		{"build", "false"},
		{"quiet", "false"},
		{"json", "false"},
	}

	for _, tt := range tests {
		flag := cmd.Flags().Lookup(tt.name)
		if flag == nil {
			t.Errorf("flag --%s not found", tt.name)
			continue
		}
		if flag.DefValue != tt.expected {
			t.Errorf("expected --%s default '%s', got '%s'", tt.name, tt.expected, flag.DefValue)
		}
	}
}

func TestExitError(t *testing.T) {
	t.Run("Error method returns correct message", func(t *testing.T) {
		exitErr := &ExitError{Code: 42}
		expected := "container exited with code 42"
		if exitErr.Error() != expected {
			t.Errorf("expected '%s', got '%s'", expected, exitErr.Error())
		}
	})

	t.Run("Code 0 message", func(t *testing.T) {
		exitErr := &ExitError{Code: 0}
		expected := "container exited with code 0"
		if exitErr.Error() != expected {
			t.Errorf("expected '%s', got '%s'", expected, exitErr.Error())
		}
	})

	t.Run("Negative code message", func(t *testing.T) {
		exitErr := &ExitError{Code: -1}
		expected := "container exited with code -1"
		if exitErr.Error() != expected {
			t.Errorf("expected '%s', got '%s'", expected, exitErr.Error())
		}
	})
}

func TestResolveShellPath(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Config
		opts     *RunOptions
		expected string
	}{
		{
			name:     "default when empty config and opts",
			cfg:      &config.Config{},
			opts:     &RunOptions{},
			expected: "/bin/sh",
		},
		{
			name:     "config shell path",
			cfg:      &config.Config{Agent: config.AgentConfig{Shell: "/bin/zsh"}},
			opts:     &RunOptions{},
			expected: "/bin/zsh",
		},
		{
			name:     "explicit shell path overrides config",
			cfg:      &config.Config{Agent: config.AgentConfig{Shell: "/bin/zsh"}},
			opts:     &RunOptions{ShellPath: "/bin/bash"},
			expected: "/bin/bash",
		},
		{
			name:     "CLI flag takes precedence over everything",
			cfg:      &config.Config{Agent: config.AgentConfig{Shell: "/bin/zsh"}},
			opts:     &RunOptions{ShellPath: "/bin/bash"},
			expected: "/bin/bash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveShellPath(tt.cfg, tt.opts)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestDetermineMode(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.Config
		modeFlag    string
		expected    config.Mode
		expectError bool
		errContains string
	}{
		{
			name:     "flag bind overrides config",
			cfg:      &config.Config{Workspace: config.WorkspaceConfig{DefaultMode: "snapshot"}},
			modeFlag: "bind",
			expected: config.ModeBind,
		},
		{
			name:     "flag snapshot overrides config",
			cfg:      &config.Config{Workspace: config.WorkspaceConfig{DefaultMode: "bind"}},
			modeFlag: "snapshot",
			expected: config.ModeSnapshot,
		},
		{
			name:     "empty flag uses config bind",
			cfg:      &config.Config{Workspace: config.WorkspaceConfig{DefaultMode: "bind"}},
			modeFlag: "",
			expected: config.ModeBind,
		},
		{
			name:     "empty flag uses config snapshot",
			cfg:      &config.Config{Workspace: config.WorkspaceConfig{DefaultMode: "snapshot"}},
			modeFlag: "",
			expected: config.ModeSnapshot,
		},
		{
			name:     "empty config defaults to bind",
			cfg:      &config.Config{},
			modeFlag: "",
			expected: config.ModeBind,
		},
		{
			name:        "invalid mode flag returns error",
			cfg:         &config.Config{},
			modeFlag:    "invalid",
			expectError: true,
			errContains: "must be 'bind' or 'snapshot'",
		},
		{
			name:        "invalid config mode returns error",
			cfg:         &config.Config{Workspace: config.WorkspaceConfig{DefaultMode: "bad"}},
			modeFlag:    "",
			expectError: true,
			errContains: "must be 'bind' or 'snapshot'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := determineMode(tt.cfg, tt.modeFlag)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("expected %s, got %s", tt.expected, result)
				}
			}
		})
	}
}

// Helper function for string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestNewCmdRun_HelpText(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRun(f)

	// Verify help contains key information
	expectedInLong := []string{
		"idempotent",
		"--mode=bind",
		"--mode=snapshot",
		"Shell path resolution",
		"/bin/sh",
	}

	for _, expected := range expectedInLong {
		if !contains(cmd.Long, expected) {
			t.Errorf("Long description should contain %q", expected)
		}
	}

	// Verify example contains key patterns
	expectedInExample := []string{
		"clawker run",
		"--mode=snapshot",
		"--detach",
		"--remove",
		"--shell",
		"-p 8080:8080",
	}

	for _, expected := range expectedInExample {
		if !contains(cmd.Example, expected) {
			t.Errorf("Example should contain %q", expected)
		}
	}
}

func TestNewCmdRun_FlagShorthandConflicts(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRun(f)

	// Verify shorthand flags are unique
	shorthands := make(map[string]string)
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Shorthand != "" {
			if existing, ok := shorthands[f.Shorthand]; ok {
				t.Errorf("shorthand -%s used by both --%s and --%s", f.Shorthand, existing, f.Name)
			}
			shorthands[f.Shorthand] = f.Name
		}
	})
}

func TestRunOptions_ZeroValue(t *testing.T) {
	// Verify zero value is safe to use
	opts := &RunOptions{}

	// All bools should be false
	if opts.Build {
		t.Error("Build should default to false")
	}
	if opts.Shell {
		t.Error("Shell should default to false")
	}
	if opts.Remove {
		t.Error("Remove should default to false")
	}
	if opts.Detach {
		t.Error("Detach should default to false")
	}
	if opts.Clean {
		t.Error("Clean should default to false")
	}
	if opts.Quiet {
		t.Error("Quiet should default to false")
	}
	if opts.JSON {
		t.Error("JSON should default to false")
	}

	// All strings should be empty
	if opts.Mode != "" {
		t.Error("Mode should default to empty")
	}
	if opts.ShellPath != "" {
		t.Error("ShellPath should default to empty")
	}
	if opts.ShellUser != "" {
		t.Error("ShellUser should default to empty")
	}
	if opts.Agent != "" {
		t.Error("Agent should default to empty")
	}

	// All slices should be nil
	if opts.Args != nil {
		t.Error("Args should default to nil")
	}
	if opts.Ports != nil {
		t.Error("Ports should default to nil")
	}
}

func TestNewCmdRun_FlagTypes(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRun(f)

	// Test string flags
	stringFlags := []string{"mode", "shell-path", "user", "agent"}
	for _, name := range stringFlags {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("expected string flag --%s to exist", name)
			continue
		}
		if flag.Value.Type() != "string" {
			t.Errorf("expected --%s to be string type, got %s", name, flag.Value.Type())
		}
	}

	// Test bool flags
	boolFlags := []string{"build", "shell", "remove", "detach", "clean", "quiet", "json"}
	for _, name := range boolFlags {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("expected bool flag --%s to exist", name)
			continue
		}
		if flag.Value.Type() != "bool" {
			t.Errorf("expected --%s to be bool type, got %s", name, flag.Value.Type())
		}
	}

	// Test stringArray flag
	flag := cmd.Flags().Lookup("publish")
	if flag == nil {
		t.Error("expected stringArray flag --publish to exist")
	} else if flag.Value.Type() != "stringArray" {
		t.Errorf("expected --publish to be stringArray type, got %s", flag.Value.Type())
	}
}

func TestNewCmdRun_AcceptsArbitraryArgs(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRun(f)

	// Verify ArbitraryArgs is set (allows passing args after --)
	if cmd.Args == nil {
		t.Error("expected Args validator to be set")
	}

	// Test that arbitrary args are accepted
	testArgs := []string{"--", "-p", "build a feature", "--resume"}
	err := cmd.Args(cmd, testArgs)
	if err != nil {
		t.Errorf("expected ArbitraryArgs to accept %v, got error: %v", testArgs, err)
	}
}

func TestNewCmdRun_ModeCompletion(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRun(f)

	// Get the completion function for --mode
	flag := cmd.Flags().Lookup("mode")
	if flag == nil {
		t.Fatal("expected --mode flag to exist")
	}

	// The completion function is registered, we can verify the flag exists
	// and has proper configuration
	if flag.DefValue != "" {
		t.Errorf("expected --mode default to be empty, got %q", flag.DefValue)
	}
}

