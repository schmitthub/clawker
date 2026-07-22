package remove

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
)

func newTestIOStreams() *iostreams.IOStreams {
	ios, _, _, _ := iostreams.Test()
	return ios
}

func TestRemoveRun_ProjectLoadError(t *testing.T) {
	opts := &RemoveOptions{
		IOStreams: newTestIOStreams(),
		ProjectManager: func() (project.ProjectManager, error) {
			return nil, errors.New("boom")
		},
		Branches: []string{"feature-1"},
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading project manager")
}

func TestRemoveRun_CurrentProjectError(t *testing.T) {
	opts := &RemoveOptions{
		IOStreams: newTestIOStreams(),
		ProjectManager: func() (project.ProjectManager, error) {
			return projectmocks.NewMockProjectManager(), nil
		},
		Branches: []string{"feature-1"},
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in a registered project directory")
}

func TestNewCmdRemove_RunFReceivesArgsAndFlags(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: newTestIOStreams()}

	called := false
	cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
		called = true
		assert.Equal(t, []string{"feat-a", "feat-b"}, opts.Branches)
		assert.True(t, opts.Force)
		assert.True(t, opts.DeleteBranch)
		return nil
	})

	cmd.SetArgs([]string{"--force", "--delete-branch", "feat-a", "feat-b"})
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}

func TestNewCmdRemove_WiresBranchCompletion(t *testing.T) {
	proj := projectmocks.NewMockProject("demo", "/repo")
	proj.ListWorktreesFunc = func(ctx context.Context) ([]project.WorktreeState, error) {
		return []project.WorktreeState{{Branch: "feat-a"}}, nil //nolint:exhaustruct // sparse fixture
	}
	mgr := projectmocks.NewMockProjectManager()
	mgr.CurrentProjectFunc = func(ctx context.Context) (project.Project, error) {
		return proj, nil
	}
	//nolint:exhaustruct // test factory carries only the nouns completion uses
	f := &cmdutil.Factory{
		IOStreams:      newTestIOStreams(),
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
	}

	cmd := NewCmdRemove(f, nil)
	require.NotNil(t, cmd.ValidArgsFunction)
	cmd.SetContext(context.Background())

	completions, directive := cmd.ValidArgsFunction(cmd, nil, "")
	assert.Equal(t, []cobra.Completion{"feat-a"}, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}
