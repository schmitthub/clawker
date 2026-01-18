package build

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmdBuild(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdBuild(f)

	require.Equal(t, "build", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Verify examples use top-level command
	require.Contains(t, cmd.Example, "clawker build")
	require.NotContains(t, cmd.Example, "clawker image build")
}

func TestNewCmdBuild_HasSameFlagsAsImageBuild(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdBuild(f)

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

func TestNewCmdBuild_FlagParsing(t *testing.T) {
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
			cmd := NewCmdBuild(f)

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

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
