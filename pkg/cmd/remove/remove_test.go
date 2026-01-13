package remove

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/pflag"
)

func TestNewCmdRemove(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	if cmd.Use != "remove" {
		t.Errorf("expected Use 'remove', got '%s'", cmd.Use)
	}

	// Check flags exist
	flags := []struct {
		name      string
		shorthand string
	}{
		{"name", "n"},
		{"project", "p"},
		{"unused", "u"},
		{"all", "a"},
		{"force", "f"},
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

func TestNewCmdRemove_Aliases(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	expected := []string{"rm"}
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

func TestNewCmdRemove_DefaultValues(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	// Verify default values for key flags
	tests := []struct {
		name     string
		expected string
	}{
		{"unused", "false"},
		{"all", "false"},
		{"force", "false"},
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

func TestNewCmdRemove_HelpText(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	// Verify Long description contains key information
	expectedInLong := []string{
		"--name",
		"--project",
		"--unused",
		"WARNING",
		"persistent data",
	}

	for _, expected := range expectedInLong {
		if !contains(cmd.Long, expected) {
			t.Errorf("Long description should contain %q", expected)
		}
	}

	// Verify Example contains key patterns
	expectedInExample := []string{
		"clawker remove -n",
		"clawker remove -p",
		"clawker remove --unused",
		"clawker remove --unused --all",
		"--force",
	}

	for _, expected := range expectedInExample {
		if !contains(cmd.Example, expected) {
			t.Errorf("Example should contain %q", expected)
		}
	}
}

func TestNewCmdRemove_FlagShorthandConflicts(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	// Verify shorthand flags are unique
	shorthands := make(map[string]string)
	cmd.Flags().VisitAll(func(fl *pflag.Flag) {
		if fl.Shorthand != "" {
			if existing, ok := shorthands[fl.Shorthand]; ok {
				t.Errorf("shorthand -%s used by both --%s and --%s", fl.Shorthand, existing, fl.Name)
			}
			shorthands[fl.Shorthand] = fl.Name
		}
	})
}

func TestNewCmdRemove_FlagTypes(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	// Test string flags
	stringFlags := []string{"name", "project"}
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
	boolFlags := []string{"unused", "all", "force"}
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
}

func TestRemoveOptions_ZeroValue(t *testing.T) {
	// Verify zero value is safe to use
	opts := &RemoveOptions{}

	// All bools should be false
	if opts.Unused {
		t.Error("Unused should default to false")
	}
	if opts.All {
		t.Error("All should default to false")
	}
	if opts.Force {
		t.Error("Force should default to false")
	}

	// All strings should be empty
	if opts.Name != "" {
		t.Error("Name should default to empty")
	}
	if opts.Project != "" {
		t.Error("Project should default to empty")
	}
}

func TestNewCmdRemove_FlagRequirements(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	// Test that command requires at least one of: name, project, unused
	// Note: We can't easily test MarkFlagsOneRequired without executing,
	// but we can verify the flags are properly configured

	// Each mode flag should exist
	modeFlags := []string{"name", "project", "unused"}
	for _, name := range modeFlags {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("expected mode flag --%s to exist", name)
		}
	}

	// Verify --all requires --unused context (in usage, not enforced by cobra)
	allFlag := cmd.Flags().Lookup("all")
	if allFlag == nil {
		t.Error("expected --all flag to exist")
	}
	if !contains(allFlag.Usage, "--unused") {
		t.Error("--all usage should mention --unused context")
	}
}

func TestNewCmdRemove_CommandMetadata(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	// Verify command metadata
	if cmd.Use != "remove" {
		t.Errorf("expected Use 'remove', got %q", cmd.Use)
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

	// Verify command has RunE (not Run)
	if cmd.RunE == nil {
		t.Error("expected RunE to be set")
	}
}

func TestNewCmdRemove_FlagUsageText(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdRemove(f)

	// Verify each flag has meaningful usage text
	flagsWithUsage := map[string]string{
		"name":    "Container name",
		"project": "project",
		"unused":  "unused",
		"all":     "volumes",
		"force":   "Force",
	}

	for name, expectedUsageContent := range flagsWithUsage {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("expected flag --%s to exist", name)
			continue
		}
		if !contains(flag.Usage, expectedUsageContent) {
			t.Errorf("flag --%s usage %q should contain %q", name, flag.Usage, expectedUsageContent)
		}
	}
}
