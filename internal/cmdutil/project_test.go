package cmdutil_test

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/project"
)

func TestRunsAnywhere(t *testing.T) {
	root := &cobra.Command{Use: "clawker"}
	parent := &cobra.Command{Use: "project"}
	child := &cobra.Command{Use: "register"}
	gated := &cobra.Command{Use: "container"}
	root.AddCommand(parent, gated)
	parent.AddCommand(child)

	require.False(t, cmdutil.RunsAnywhere(child))
	cmdutil.MarkRunsAnywhere(parent)
	require.True(t, cmdutil.RunsAnywhere(child), "mark covers subcommands")
	require.False(t, cmdutil.RunsAnywhere(gated))
	require.True(t, cmdutil.RunsAnywhere(&cobra.Command{Use: "help"}))
	require.True(t, cmdutil.RunsAnywhere(&cobra.Command{Use: cobra.ShellCompRequestCmd}))
}

func TestRequireProject(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	empty, err := project.NewRegistryFromString("")
	require.NoError(t, err)
	err = cmdutil.RequireProject(func() (project.Registry, error) { return empty, nil })
	require.ErrorIs(t, err, project.ErrNotInProject)

	seeded, err := project.NewRegistryFromString("projects:\n  - name: demo\n    root: " + root + "\n")
	require.NoError(t, err)
	require.NoError(t, cmdutil.RequireProject(func() (project.Registry, error) { return seeded, nil }))

	boom := errors.New("boom")
	err = cmdutil.RequireProject(func() (project.Registry, error) { return nil, boom })
	require.ErrorIs(t, err, boom)
	require.Error(t, cmdutil.RequireProject(nil))
}
