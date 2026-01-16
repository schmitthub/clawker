package image

import (
	"sort"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdImage(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdImage(f)

	// Verify command basics
	require.Equal(t, "image", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)

	// Verify this is a parent command (no RunE)
	require.Nil(t, cmd.RunE)
}

func TestNewCmdImage_Subcommands(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdImage(f)

	// Get registered subcommands
	subcommands := cmd.Commands()

	// Expect 5 subcommands: build, inspect, list, prune, remove
	require.Len(t, subcommands, 5)

	// Get subcommand names and sort them
	var names []string
	for _, sub := range subcommands {
		names = append(names, sub.Name())
	}
	sort.Strings(names)

	// Verify expected subcommands (alphabetically sorted)
	expected := []string{"build", "inspect", "list", "prune", "remove"}
	require.Equal(t, expected, names)
}
