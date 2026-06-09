package add

import (
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

func newTestIOStreams() *iostreams.IOStreams {
	ios, _, _, _ := iostreams.Test()
	return ios
}

func TestAddRun_ProjectLoadError(t *testing.T) {
	opts := &AddOptions{
		IOStreams: newTestIOStreams(),
		ProjectManager: func() (project.ProjectManager, error) {
			return nil, errors.New("boom")
		},
		Branch: "feature-1",
	}

	err := addRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading project manager")
}

func TestAddRun_ThreadsNoTrackToCreateWorktree(t *testing.T) {
	tests := []struct {
		name        string
		noTrack     bool
		wantNoTrack bool
	}{
		{name: "--no-track set", noTrack: true, wantNoTrack: true},
		{name: "--no-track unset (default)", noTrack: false, wantNoTrack: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proj := projectmocks.NewMockProject("p", "/repo")
			proj.CreateWorktreeFunc = func(_ context.Context, _, _ string, noTrack bool) (string, error) {
				return "/wt/path", assertNoTrack(t, noTrack, tt.wantNoTrack)
			}
			pm := projectmocks.NewMockProjectManager()
			pm.CurrentProjectFunc = func(_ context.Context) (project.Project, error) { return proj, nil }

			opts := &AddOptions{
				IOStreams:      newTestIOStreams(),
				ProjectManager: func() (project.ProjectManager, error) { return pm, nil },
				Branch:         "feature/x",
				NoTrack:        tt.noTrack,
			}
			require.NoError(t, addRun(context.Background(), opts))

			calls := proj.CreateWorktreeCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, tt.wantNoTrack, calls[0].NoTrack, "opts.NoTrack must reach CreateWorktree")
		})
	}
}

// assertNoTrack lets the mock fail loudly if the wrong bool is threaded.
func assertNoTrack(t *testing.T, got, want bool) error {
	t.Helper()
	assert.Equal(t, want, got, "CreateWorktree received the wrong noTrack value")
	return nil
}

func TestNewCmdAdd_RunFReceivesArgsAndFlags(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: newTestIOStreams()}

	called := false
	cmd := NewCmdAdd(f, func(_ context.Context, opts *AddOptions) error {
		called = true
		assert.Equal(t, "feature/login", opts.Branch)
		assert.Equal(t, "main", opts.Base)
		return nil
	})

	cmd.SetArgs([]string{"feature/login", "--base", "main"})
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}
