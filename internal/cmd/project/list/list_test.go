package list

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/shlex"
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

func TestNewCmdList(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantQuiet bool
	}{
		{
			name:  "no flags",
			input: "",
		},
		{
			name:      "quiet flag",
			input:     "-q",
			wantQuiet: true,
		},
		{
			name:      "quiet flag long",
			input:     "--quiet",
			wantQuiet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreamstest.New()
			f := &cmdutil.Factory{IOStreams: tio.IOStreams}

			var gotOpts *ListOptions
			cmd := NewCmdList(f, func(_ context.Context, opts *ListOptions) error {
				gotOpts = opts
				return nil
			})

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			assert.Equal(t, tt.wantQuiet, gotOpts.Format.Quiet)
		})
	}
}

func TestNewCmdList_FormatFlags(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:  "json flag",
			input: "--json",
		},
		{
			name:  "format json",
			input: "--format json",
		},
		{
			name:    "json and format mutually exclusive",
			input:   "--json --format table",
			wantErr: "--format and --json are mutually exclusive",
		},
		{
			name:    "quiet and json mutually exclusive",
			input:   "-q --json",
			wantErr: "--quiet and --format/--json are mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreamstest.New()
			f := &cmdutil.Factory{IOStreams: tio.IOStreams}

			cmd := NewCmdList(f, func(_ context.Context, _ *ListOptions) error {
				return nil
			})

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// --- Tier 2: Run function tests ---

func TestListRun_Empty(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	_, tio := testFactory(t, mgr)

	opts := &ListOptions{
		IOStreams:      tio.IOStreams,
		TUI:            tui.NewTUI(tio.IOStreams),
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Format:         &cmdutil.FormatFlags{},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)
	assert.Contains(t, tio.ErrBuf.String(), "No registered projects found")
}

func TestListRun_ProjectManagerError(t *testing.T) {
	tio := iostreamstest.New()
	opts := &ListOptions{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		ProjectManager: func() (project.ProjectManager, error) {
			return nil, errors.New("boom")
		},
		Format: &cmdutil.FormatFlags{},
	}

	err := listRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading project manager")
}

func TestListRun_Table(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/does-not-exist-alpha"},
			{Name: "beta", Root: "/tmp/does-not-exist-beta", Worktrees: map[string]config.WorktreeEntry{
				"feat-1": {Path: "/tmp/wt1", Branch: "feat-1"},
			}},
		}, nil
	}

	_, tio := testFactory(t, mgr)
	opts := &ListOptions{
		IOStreams:      tio.IOStreams,
		TUI:            tui.NewTUI(tio.IOStreams),
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Format:         &cmdutil.FormatFlags{},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.Contains(t, output, "alpha")
	assert.Contains(t, output, "beta")
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "ROOT")
}

func TestListRun_Quiet(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/alpha"},
			{Name: "beta", Root: "/tmp/beta"},
		}, nil
	}

	_, tio := testFactory(t, mgr)
	opts := &ListOptions{
		IOStreams:      tio.IOStreams,
		TUI:            tui.NewTUI(tio.IOStreams),
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Format:         &cmdutil.FormatFlags{Quiet: true},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	output := tio.OutBuf.String()
	assert.Equal(t, "alpha\nbeta\n", output)
}

func TestListRun_JSON(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListFunc = func(_ context.Context) ([]config.ProjectEntry, error) {
		return []config.ProjectEntry{
			{Name: "alpha", Root: "/tmp/does-not-exist-alpha"},
		}, nil
	}

	f, tio := testFactory(t, mgr)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"--json"})
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

func TestBuildProjectRows(t *testing.T) {
	entries := []config.ProjectEntry{
		{Name: "foo", Root: "/tmp/does-not-exist-foo"},
		{Name: "bar", Root: "/tmp", Worktrees: map[string]config.WorktreeEntry{
			"w1": {Path: "/tmp/w1", Branch: "w1"},
		}},
	}

	rows := buildProjectRows(entries)
	require.Len(t, rows, 2)

	assert.Equal(t, "foo", rows[0].Name)
	assert.Equal(t, "/tmp/does-not-exist-foo", rows[0].Root)
	assert.Equal(t, 0, rows[0].Worktrees)
	assert.False(t, rows[0].Exists)

	assert.Equal(t, "bar", rows[1].Name)
	assert.Equal(t, "/tmp", rows[1].Root)
	assert.Equal(t, 1, rows[1].Worktrees)
	assert.True(t, rows[1].Exists)
}

func TestDirExists(t *testing.T) {
	assert.True(t, dirExists("/tmp"))
	assert.False(t, dirExists("/tmp/definitely-not-a-real-dir-xyz"))
}
