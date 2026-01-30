package ralph

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRalph(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRalph(f)

	// Test parent command properties
	assert.Equal(t, "ralph", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Test subcommands exist
	subCmds := cmd.Commands()
	require.Len(t, subCmds, 4)

	var runCmd, statusCmd, resetCmd, tuiCmd *cobra.Command
	for _, sub := range subCmds {
		switch sub.Use {
		case "run":
			runCmd = sub
		case "status":
			statusCmd = sub
		case "reset":
			resetCmd = sub
		case "tui":
			tuiCmd = sub
		}
	}

	require.NotNil(t, runCmd, "run subcommand should exist")
	require.NotNil(t, statusCmd, "status subcommand should exist")
	require.NotNil(t, resetCmd, "reset subcommand should exist")
	require.NotNil(t, tuiCmd, "tui subcommand should exist")
}

