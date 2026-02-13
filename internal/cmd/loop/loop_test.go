package loop

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdLoop(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdLoop(f)

	// Test parent command properties
	assert.Equal(t, "loop", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Test subcommands exist
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
}
