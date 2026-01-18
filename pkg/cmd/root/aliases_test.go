package root

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestRegisterAliases(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	root := &cobra.Command{Use: "clawker"}

	registerAliases(root, f)

	// Verify all expected aliases are registered
	expectedAliases := []string{"build", "run", "start", "ps"}
	for _, name := range expectedAliases {
		cmd, _, err := root.Find([]string{name})
		require.NoError(t, err, "alias %q should be found", name)
		require.NotNil(t, cmd, "alias %q should not be nil", name)
		require.Equal(t, name, cmd.Name(), "alias should have correct name")
	}
}

func TestBuildAlias(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	root := &cobra.Command{Use: "clawker"}

	registerAliases(root, f)

	cmd, _, err := root.Find([]string{"build"})
	require.NoError(t, err)

	// Verify Use field is set correctly
	require.Equal(t, "build [OPTIONS]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Verify examples use top-level command
	require.Contains(t, cmd.Example, "clawker build")
	require.NotContains(t, cmd.Example, "clawker image build")
}

func TestBuildAlias_HasExpectedFlags(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	root := &cobra.Command{Use: "clawker"}

	registerAliases(root, f)

	cmd, _, err := root.Find([]string{"build"})
	require.NoError(t, err)

	// Check all Docker CLI-compatible flags exist
	expectedFlags := []struct {
		name      string
		shorthand string
	}{
		{"file", "f"},
		{"tag", "t"},
		{"no-cache", ""},
		{"pull", ""},
		{"build-arg", ""},
		{"label", ""},
		{"target", ""},
		{"quiet", "q"},
		{"progress", ""},
		{"network", ""},
	}

	for _, fl := range expectedFlags {
		t.Run(fl.name, func(t *testing.T) {
			flag := cmd.Flags().Lookup(fl.name)
			require.NotNil(t, flag, "expected --%s flag to exist", fl.name)
			if fl.shorthand != "" {
				require.Equal(t, fl.shorthand, flag.Shorthand,
					"expected --%s shorthand -%s", fl.name, fl.shorthand)
			}
		})
	}
}

func TestBuildAlias_FlagParsing(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name: "no flags",
			args: []string{},
		},
		{
			name: "file flag",
			args: []string{"-f", "Dockerfile.dev"},
		},
		{
			name: "tag flag",
			args: []string{"-t", "myapp:latest"},
		},
		{
			name: "multiple flags",
			args: []string{"-f", "Dockerfile", "-t", "myapp:latest", "--no-cache", "-q"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmdutil.New("1.0.0", "abc123")
			root := &cobra.Command{Use: "clawker"}
			registerAliases(root, f)

			cmd, _, err := root.Find([]string{"build"})
			require.NoError(t, err)

			// Override RunE to prevent actual execution
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				return nil
			}

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRunAlias(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	root := &cobra.Command{Use: "clawker"}

	registerAliases(root, f)

	cmd, _, err := root.Find([]string{"run"})
	require.NoError(t, err)

	require.Equal(t, "run [OPTIONS] IMAGE [COMMAND] [ARG...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotNil(t, cmd.RunE)
}

func TestStartAlias(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	root := &cobra.Command{Use: "clawker"}

	registerAliases(root, f)

	cmd, _, err := root.Find([]string{"start"})
	require.NoError(t, err)

	require.Equal(t, "start [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotNil(t, cmd.RunE)
}

func TestPsAlias(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	root := &cobra.Command{Use: "clawker"}

	registerAliases(root, f)

	cmd, _, err := root.Find([]string{"ps"})
	require.NoError(t, err)

	require.Equal(t, "ps [OPTIONS]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotNil(t, cmd.RunE)
}

func TestAliasExampleOverride(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	root := &cobra.Command{Use: "clawker"}

	registerAliases(root, f)

	// Build alias has custom Example - should NOT contain "clawker image build"
	buildCmd, _, err := root.Find([]string{"build"})
	require.NoError(t, err)
	require.Contains(t, buildCmd.Example, "clawker build")
	require.NotContains(t, buildCmd.Example, "clawker image build",
		"build alias should have custom Example that references top-level command")

	// Run alias has no custom Example - inherits from target command
	runCmd, _, err := root.Find([]string{"run"})
	require.NoError(t, err)
	require.NotEmpty(t, runCmd.Example, "run alias should inherit Example from target command")
}
