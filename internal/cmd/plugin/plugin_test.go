package plugin_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmd/plugin"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestNewCmdPlugin(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	cmd := plugin.NewCmdPlugin(f)

	assert.Equal(t, "plugin", cmd.Use)
	assert.Contains(t, cmd.Aliases, "skill")
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)
	assert.Nil(t, cmd.RunE, "parent command should have no RunE")

	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}
	require.True(t, subcommands["install"], "missing install subcommand")
	require.True(t, subcommands["show"], "missing show subcommand")
	require.True(t, subcommands["remove"], "missing remove subcommand")
}
