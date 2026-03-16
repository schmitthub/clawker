package info

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Tier 1: Flag parsing tests ---

func TestNewCmdInfo_RunFReceivesArgs(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	called := false
	cmd := NewCmdInfo(f, func(_ context.Context, opts *InfoOptions) error {
		called = true
		assert.Equal(t, "my-app", opts.Name)
		return nil
	})

	cmd.SetArgs([]string{"my-app"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}

func TestNewCmdInfo_RequiresExactlyOneArg(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	cmd := NewCmdInfo(f, func(_ context.Context, _ *InfoOptions) error {
		return nil
	})

	cmd.SetArgs([]string{})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	require.Error(t, err)

	cmd.SetArgs([]string{"a", "b"})
	err = cmd.Execute()
	require.Error(t, err)
}

// --- Tier 2: Run function tests ---

func TestInfoRun_ProjectManagerError(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	opts := &InfoOptions{
		IOStreams: ios,
		ProjectManager: func() (project.ProjectManager, error) {
			return nil, errors.New("boom")
		},
		Name: "alpha",
	}

	err := infoRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading project manager")
}

func TestInfoRun_ProjectNotFound(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListProjectsFunc = func(_ context.Context) ([]project.ProjectState, error) {
		return []project.ProjectState{
			{Name: "alpha", Root: "/tmp/alpha", Status: project.ProjectOK},
		}, nil
	}

	ios, _, _, _ := iostreams.Test()
	opts := &InfoOptions{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Name:           "unknown",
	}

	err := infoRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `project "unknown" is not registered`)
}

func TestInfoRun_Success(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListProjectsFunc = func(_ context.Context) ([]project.ProjectState, error) {
		return []project.ProjectState{
			{
				Name:   "alpha",
				Root:   "/tmp",
				Status: project.ProjectOK,
				Worktrees: []project.WorktreeState{
					{Branch: "feat-1", Path: "/tmp/wt1", Status: project.WorktreeHealthy},
				},
			},
		}, nil
	}

	ios, _, outBuf, _ := iostreams.Test()
	opts := &InfoOptions{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Name:           "alpha",
	}

	err := infoRun(context.Background(), opts)
	require.NoError(t, err)

	output := outBuf.String()
	assert.Contains(t, output, "alpha")
	assert.Contains(t, output, "/tmp")
	assert.Contains(t, output, "feat-1")
}

func TestInfoRun_MissingDirectory(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListProjectsFunc = func(_ context.Context) ([]project.ProjectState, error) {
		return []project.ProjectState{
			{Name: "alpha", Root: "/tmp/does-not-exist-xyz", Status: project.ProjectMissing},
		}, nil
	}

	ios, _, outBuf, _ := iostreams.Test()
	opts := &InfoOptions{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Name:           "alpha",
	}

	err := infoRun(context.Background(), opts)
	require.NoError(t, err)

	output := outBuf.String()
	assert.Contains(t, output, "alpha")
	assert.Contains(t, output, "missing")
}

func TestInfoRun_NoWorktrees(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListProjectsFunc = func(_ context.Context) ([]project.ProjectState, error) {
		return []project.ProjectState{
			{Name: "alpha", Root: "/tmp", Status: project.ProjectOK},
		}, nil
	}

	ios, _, outBuf, _ := iostreams.Test()
	opts := &InfoOptions{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Name:           "alpha",
	}

	err := infoRun(context.Background(), opts)
	require.NoError(t, err)
	assert.Contains(t, outBuf.String(), "none")
}

func TestInfoRun_JSON(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListProjectsFunc = func(_ context.Context) ([]project.ProjectState, error) {
		return []project.ProjectState{
			{Name: "alpha", Root: "/tmp/does-not-exist-xyz", Status: project.ProjectMissing},
		}, nil
	}

	ios, _, outBuf, errBuf := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams:      ios,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
	}
	cmd := NewCmdInfo(f, nil)
	cmd.SetArgs([]string{"alpha", "--json"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	output := outBuf.String()
	assert.Contains(t, output, `"name": "alpha"`)
	assert.Contains(t, output, `"status": "missing"`)
}

// --- Tier 3: Unit tests ---

func TestFindByName(t *testing.T) {
	states := []project.ProjectState{
		{Name: "alpha", Root: "/tmp/alpha"},
		{Name: "beta", Root: "/tmp/beta"},
	}

	s, ok := findByName(states, "beta")
	assert.True(t, ok)
	assert.Equal(t, "beta", s.Name)

	_, ok = findByName(states, "gamma")
	assert.False(t, ok)
}

func TestBuildDetail(t *testing.T) {
	state := project.ProjectState{
		Name:   "alpha",
		Root:   "/tmp",
		Status: project.ProjectOK,
		Worktrees: []project.WorktreeState{
			{Branch: "feat-b", Path: "/tmp/wt-b", Status: project.WorktreeHealthy},
			{Branch: "feat-a", Path: "/tmp/wt-a", Status: project.WorktreeBroken},
		},
	}

	detail := buildDetail(state)
	assert.Equal(t, "alpha", detail.Name)
	assert.Equal(t, "/tmp", detail.Root)
	assert.Equal(t, "ok", detail.Status)
	require.Len(t, detail.Worktrees, 2)
	assert.Equal(t, "feat-a", detail.Worktrees[0].Branch)
	assert.Equal(t, "broken", detail.Worktrees[0].Status)
	assert.Equal(t, "feat-b", detail.Worktrees[1].Branch)
}
