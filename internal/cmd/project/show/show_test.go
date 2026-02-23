package show

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFactory(t *testing.T, mgr project.ProjectManager) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()
	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		ProjectManager: func() (project.ProjectManager, error) {
			return mgr, nil
		},
	}, tio
}

// --- Tier 1: Flag parsing tests ---

func TestNewCmdShow_RunFReceivesArgs(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	called := false
	cmd := NewCmdShow(f, func(_ context.Context, opts *ShowOptions) error {
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

func TestNewCmdShow_RequiresExactlyOneArg(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	cmd := NewCmdShow(f, func(_ context.Context, _ *ShowOptions) error {
		return nil
	})

	// No args.
	cmd.SetArgs([]string{})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	require.Error(t, err)

	// Two args.
	cmd.SetArgs([]string{"a", "b"})
	err = cmd.Execute()
	require.Error(t, err)
}

// --- Tier 2: Run function tests ---

func TestShowRun_ProjectManagerError(t *testing.T) {
	tio := iostreamstest.New()
	opts := &ShowOptions{
		IOStreams: tio.IOStreams,
		ProjectManager: func() (project.ProjectManager, error) {
			return nil, errors.New("boom")
		},
		Name:   "alpha",
		Format: &cmdutil.FormatFlags{},
	}

	err := showRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading project manager")
}

func TestShowRun_ProjectNotFound(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/alpha"},
		}, nil
	}

	tio := iostreamstest.New()
	opts := &ShowOptions{
		IOStreams:      tio.IOStreams,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Name:           "unknown",
		Format:         &cmdutil.FormatFlags{},
	}

	err := showRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `project "unknown" is not registered`)
}

func TestShowRun_Success(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{
				Name: "alpha",
				Root: "/tmp",
				Worktrees: map[string]config.WorktreeEntry{
					"feat-1": {Path: "/tmp/wt1", Branch: "feat-1"},
				},
			},
		}, nil
	}

	tio := iostreamstest.New()
	opts := &ShowOptions{
		IOStreams:      tio.IOStreams,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Name:           "alpha",
		Format:         &cmdutil.FormatFlags{},
	}

	err := showRun(context.Background(), opts)
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.Contains(t, output, "alpha")
	assert.Contains(t, output, "/tmp")
	assert.Contains(t, output, "feat-1")
}

func TestShowRun_MissingDirectory(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/does-not-exist-xyz"},
		}, nil
	}

	tio := iostreamstest.New()
	opts := &ShowOptions{
		IOStreams:      tio.IOStreams,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Name:           "alpha",
		Format:         &cmdutil.FormatFlags{},
	}

	err := showRun(context.Background(), opts)
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.Contains(t, output, "alpha")
	assert.Contains(t, output, "missing")
}

func TestShowRun_NoWorktrees(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp"},
		}, nil
	}

	tio := iostreamstest.New()
	opts := &ShowOptions{
		IOStreams:      tio.IOStreams,
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Name:           "alpha",
		Format:         &cmdutil.FormatFlags{},
	}

	err := showRun(context.Background(), opts)
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.Contains(t, output, "none")
}

func TestShowRun_JSON(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/does-not-exist-xyz"},
		}, nil
	}

	f, tio := testFactory(t, mgr)
	cmd := NewCmdShow(f, nil)
	cmd.SetArgs([]string{"alpha", "--json"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.Contains(t, output, `"name": "alpha"`)
	assert.Contains(t, output, `"exists": false`)
}

// --- Tier 3: Unit tests ---

func TestFindProjectByName(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/alpha"},
			{Name: "beta", Root: "/tmp/beta"},
		}, nil
	}

	entry, err := findProjectByName(context.Background(), mgr, "beta")
	require.NoError(t, err)
	assert.Equal(t, "beta", entry.Name)
	assert.Equal(t, "/tmp/beta", entry.Root)

	_, err = findProjectByName(context.Background(), mgr, "gamma")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"gamma" is not registered`)
}

func TestBuildProjectDetail(t *testing.T) {
	entry := config.ProjectEntry{
		Name: "alpha",
		Root: "/tmp",
		Worktrees: map[string]config.WorktreeEntry{
			"feat-b": {Path: "/tmp/wt-b", Branch: "feat-b"},
			"feat-a": {Path: "/tmp/wt-a", Branch: "feat-a"},
		},
	}

	detail := buildProjectDetail(entry)
	assert.Equal(t, "alpha", detail.Name)
	assert.Equal(t, "/tmp", detail.Root)
	assert.True(t, detail.Exists)
	require.Len(t, detail.Worktrees, 2)
	// Sorted by branch name.
	assert.Equal(t, "feat-a", detail.Worktrees[0].Branch)
	assert.Equal(t, "feat-b", detail.Worktrees[1].Branch)
}
