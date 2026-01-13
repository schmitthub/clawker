package run

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/pkg/cmdutil"
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
