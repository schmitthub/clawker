package cmdutil_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
)

func TestAddApproveGrantsFlag(t *testing.T) {
	var approve bool
	command := new(cobra.Command)
	command.Use = "test"
	command.RunE = func(*cobra.Command, []string) error {
		return nil
	}
	cmdutil.AddApproveGrantsFlag(command, &approve)
	command.SetArgs([]string{"--" + cmdutil.FlagApproveGrants})

	require.NoError(t, command.Execute())

	assert.True(t, approve)
	flag := command.Flags().Lookup(cmdutil.FlagApproveGrants)
	require.NotNil(t, flag)
	assert.Contains(t, flag.Usage, "declared host socket")
}
