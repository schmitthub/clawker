package list

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFactory(t *testing.T, mgr project.ProjectManager) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, outBuf, errBuf := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		ProjectManager: func() (project.ProjectManager, error) {
			return mgr, nil
		},
	}, outBuf, errBuf
}

// --- Tier 1: Flag parsing tests ---

func TestNewCmdList(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantQuiet bool
	}{
		{name: "no flags", input: ""},
		{name: "quiet flag", input: "-q", wantQuiet: true},
		{name: "quiet flag long", input: "--quiet", wantQuiet: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, _, _ := iostreams.Test()
			f := &cmdutil.Factory{IOStreams: ios}

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
		{name: "json flag", input: "--json"},
		{name: "format json", input: "--format json"},
		{name: "json and format mutually exclusive", input: "--json --format table", wantErr: "--format and --json are mutually exclusive"},
		{name: "quiet and json mutually exclusive", input: "-q --json", wantErr: "--quiet and --format/--json are mutually exclusive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, _, _ := iostreams.Test()
			f := &cmdutil.Factory{IOStreams: ios}

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
	ios, _, _, errBuf := iostreams.Test()

	opts := &ListOptions{
		IOStreams:      ios,
		TUI:            tui.NewTUI(ios),
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Format:         &cmdutil.FormatFlags{},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)
	assert.Contains(t, errBuf.String(), "No registered projects found")
}

func TestListRun_ProjectManagerError(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	opts := &ListOptions{
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
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
	mgr.ListProjectsFunc = func(_ context.Context) ([]project.ProjectState, error) {
		return []project.ProjectState{
			{Name: "alpha", Root: "/tmp/does-not-exist-alpha", Status: project.ProjectMissing},
			{Name: "beta", Root: "/tmp/does-not-exist-beta", Status: project.ProjectOK, Worktrees: []project.WorktreeState{
				{Branch: "feat-1", Path: "/tmp/wt1"},
			}},
		}, nil
	}

	ios, _, outBuf, _ := iostreams.Test()
	opts := &ListOptions{
		IOStreams:      ios,
		TUI:            tui.NewTUI(ios),
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Format:         &cmdutil.FormatFlags{},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	output := outBuf.String()
	assert.Contains(t, output, "alpha")
	assert.Contains(t, output, "beta")
	assert.Contains(t, output, "NAME")
	assert.Contains(t, output, "ROOT")
}

func TestListRun_Quiet(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListProjectsFunc = func(_ context.Context) ([]project.ProjectState, error) {
		return []project.ProjectState{
			{Name: "alpha", Root: "/tmp/alpha", Status: project.ProjectOK},
			{Name: "beta", Root: "/tmp/beta", Status: project.ProjectOK},
		}, nil
	}

	ios, _, outBuf, _ := iostreams.Test()
	opts := &ListOptions{
		IOStreams:      ios,
		TUI:            tui.NewTUI(ios),
		ProjectManager: func() (project.ProjectManager, error) { return mgr, nil },
		Format:         &cmdutil.FormatFlags{Quiet: true},
	}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, "alpha\nbeta\n", outBuf.String())
}

func TestListRun_JSON(t *testing.T) {
	mgr := projectmocks.NewMockProjectManager()
	mgr.ListProjectsFunc = func(_ context.Context) ([]project.ProjectState, error) {
		return []project.ProjectState{
			{Name: "alpha", Root: "/tmp/does-not-exist-alpha", Status: project.ProjectMissing},
		}, nil
	}

	f, outBuf, errBuf := testFactory(t, mgr)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"--json"})
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

func TestBuildProjectRows(t *testing.T) {
	states := []project.ProjectState{
		{Name: "foo", Root: "/tmp/does-not-exist-foo", Status: project.ProjectMissing},
		{Name: "bar", Root: "/tmp", Status: project.ProjectOK, Worktrees: []project.WorktreeState{
			{Branch: "w1", Path: "/tmp/w1"},
		}},
	}

	rows := buildProjectRows(states)
	require.Len(t, rows, 2)

	assert.Equal(t, "foo", rows[0].Name)
	assert.Equal(t, "/tmp/does-not-exist-foo", rows[0].Root)
	assert.Equal(t, 0, rows[0].Worktrees)
	assert.Equal(t, "missing", rows[0].Status)

	assert.Equal(t, "bar", rows[1].Name)
	assert.Equal(t, "/tmp", rows[1].Root)
	assert.Equal(t, 1, rows[1].Worktrees)
	assert.Equal(t, "ok", rows[1].Status)
}
