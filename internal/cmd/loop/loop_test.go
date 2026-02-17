package loop

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdLoop(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
	}
	cmd := NewCmdLoop(f)

	// Test parent command properties
	assert.Equal(t, "loop", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Parent command should not have RunE (requires subcommand)
	assert.Nil(t, cmd.RunE)
}

func TestNewCmdLoop_Subcommands(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
	}
	cmd := NewCmdLoop(f)

	subCmds := cmd.Commands()
	require.Len(t, subCmds, 4)

	var iterateCmd, tasksCmd, statusCmd, resetCmd *cobra.Command
	for _, sub := range subCmds {
		switch sub.Use {
		case "iterate":
			iterateCmd = sub
		case "tasks":
			tasksCmd = sub
		case "status":
			statusCmd = sub
		case "reset":
			resetCmd = sub
		}
	}

	require.NotNil(t, iterateCmd, "iterate subcommand should exist")
	require.NotNil(t, tasksCmd, "tasks subcommand should exist")
	require.NotNil(t, statusCmd, "status subcommand should exist")
	require.NotNil(t, resetCmd, "reset subcommand should exist")

	// Verify subcommands have descriptions
	assert.NotEmpty(t, iterateCmd.Short)
	assert.NotEmpty(t, tasksCmd.Short)
	assert.NotEmpty(t, statusCmd.Short)
	assert.NotEmpty(t, resetCmd.Short)

	// Verify subcommands have RunE (they are leaf commands)
	assert.NotNil(t, iterateCmd.RunE)
	assert.NotNil(t, tasksCmd.RunE)
	assert.NotNil(t, statusCmd.RunE)
	assert.NotNil(t, resetCmd.RunE)
}
