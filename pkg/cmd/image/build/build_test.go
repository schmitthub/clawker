package build

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmd(t *testing.T) {
	f := &cmdutil.Factory{}

	var runCalled bool
	cmd := NewCmd(f)

	// Override RunE to verify it runs
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		runCalled = true
		return nil
	}

	// Cobra hack-around for help flag
	cmd.Flags().BoolP("help", "x", false, "")

	cmd.SetArgs([]string{})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.True(t, runCalled)
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "build", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist (same as top-level build)
	require.NotNil(t, cmd.Flags().Lookup("no-cache"))
	require.NotNil(t, cmd.Flags().Lookup("dockerfile"))
}
