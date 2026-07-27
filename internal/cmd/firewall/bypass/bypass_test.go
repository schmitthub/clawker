package bypass //nolint:testpackage // exercises the unexported run function directly

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
)

func newTestFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	ios, _, _, _ := iostreams.Test() //nolint:dogsled // iostreams.Test returns three buffers this helper does not assert on
	//nolint:exhaustruct // factory wires only the fields the bypass command reads
	return &cmdutil.Factory{
		IOStreams:      ios,
		ProjectManager: projectManagerFor(""),
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return bypassMockClient(nil, nil), nil
		},
	}
}

// bypassMockClient is a mock AdminService client for the two RPCs bypass
// drives: FirewallBypass to start one and FirewallEnable to stop one.
// Either error, when non-nil, is returned instead of a result.
func bypassMockClient(bypassErr, enableErr error) *adminv1mocks.AdminServiceClientMock {
	//nolint:exhaustruct // mock wires only the RPCs this command drives
	return &adminv1mocks.AdminServiceClientMock{
		FirewallBypassFunc: func(_ context.Context, _ *adminv1.FirewallBypassRequest, _ ...grpc.CallOption) (*adminv1.FirewallBypassResult, error) {
			if bypassErr != nil {
				return nil, bypassErr
			}
			//nolint:exhaustruct // result carries no fields the command reads
			return &adminv1.FirewallBypassResult{}, nil
		},
		FirewallEnableFunc: func(_ context.Context, _ *adminv1.FirewallEnableRequest, _ ...grpc.CallOption) (*adminv1.FirewallEnableResult, error) {
			if enableErr != nil {
				return nil, enableErr
			}
			//nolint:exhaustruct // result carries no fields the command reads
			return &adminv1.FirewallEnableResult{}, nil
		},
	}
}

// projectManagerFor returns a ProjectManager closure whose CurrentProject
// resolves to the named project, or fails when name is empty (the
// "no current project" case that collapses the container ref to two segments).
func projectManagerFor(name string) func() (project.ProjectManager, error) {
	return func() (project.ProjectManager, error) {
		pm := projectmocks.NewMockProjectManager()
		pm.CurrentProjectFunc = func(_ context.Context) (project.Project, error) {
			if name == "" {
				return nil, project.ErrProjectNotFound
			}
			return projectmocks.NewMockProject(name, "/repo/"+name), nil
		}
		return pm, nil
	}
}

// TestNewCmdBypass covers the arg/flag contract: the duration positional is
// required except under --stop (which rejects it), and a malformed or
// non-positive duration is refused before the run function is ever reached.
//
//nolint:exhaustruct // table rows set only the fields each case exercises
func TestNewCmdBypass(t *testing.T) {
	tests := []struct {
		name               string
		input              []string
		wantAgent          string
		wantDuration       time.Duration
		wantStop           bool
		wantNonInteractive bool
		wantErr            bool
	}{
		{
			name:         "duration arg",
			input:        []string{"5m", "--agent", "dev"},
			wantAgent:    "dev",
			wantDuration: 5 * time.Minute,
		},
		{
			name:               "non-interactive",
			input:              []string{"90s", "--agent", "dev", "--non-interactive"},
			wantAgent:          "dev",
			wantDuration:       90 * time.Second,
			wantNonInteractive: true,
		},
		{
			name:      "stop without duration",
			input:     []string{"--stop", "--agent", "dev"},
			wantAgent: "dev",
			wantStop:  true,
		},
		{
			name:    "stop rejects duration",
			input:   []string{"--stop", "5m", "--agent", "dev"},
			wantErr: true,
		},
		{
			name:    "missing duration",
			input:   []string{"--agent", "dev"},
			wantErr: true,
		},
		{
			name:    "invalid duration",
			input:   []string{"forever", "--agent", "dev"},
			wantErr: true,
		},
		{
			name:    "non-positive duration",
			input:   []string{"0s", "--agent", "dev"},
			wantErr: true,
		},
		{
			name:    "sub-second duration",
			input:   []string{"500ms", "--agent", "dev"},
			wantErr: true,
		},
		{
			name:    "fractional-second duration",
			input:   []string{"90.7s", "--agent", "dev"},
			wantErr: true,
		},
		{
			name:    "missing required agent",
			input:   []string{"5m"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestFactory(t)

			var gotOpts *BypassOptions
			cmd := NewCmdBypass(f, func(_ context.Context, opts *BypassOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.input)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			err := cmd.Execute()
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, gotOpts, "runF must not fire when arg/flag validation fails")
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			assert.Equal(t, tt.wantAgent, gotOpts.Agent)
			assert.Equal(t, tt.wantDuration, gotOpts.Duration)
			assert.Equal(t, tt.wantStop, gotOpts.Stop)
			assert.Equal(t, tt.wantNonInteractive, gotOpts.NonInteractive)
			assert.NotNil(t, gotOpts.AdminClient)
		})
	}
}

// bypassRunCase is one row of the bypassRun table: the options that select a
// non-interactive path plus the RPC traffic and rendering it must produce.
type bypassRunCase struct {
	name               string
	stop               bool
	duration           time.Duration
	projectName        string
	bypassErr          error
	enableErr          error
	wantContainerID    string
	wantBypassCalls    int
	wantEnableCalls    int
	wantTimeoutSeconds uint32
	wantStdout         string
	wantErrOut         string
	wantErrContains    string
}

// assertOutput checks the run's error and rendered output against the case.
func (tc bypassRunCase) assertOutput(t *testing.T, err error, stdout, errOut *bytes.Buffer) {
	t.Helper()
	if tc.wantErrContains != "" {
		require.Error(t, err)
		assert.Contains(t, err.Error(), tc.wantErrContains)
		return
	}
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), tc.wantStdout)
	if tc.wantErrOut != "" {
		assert.Contains(t, errOut.String(), tc.wantErrOut)
	}
}

// assertCalls checks which RPCs fired and what container ref / timeout they
// carried, read off moq's recorded calls rather than a copied proto value.
func (tc bypassRunCase) assertCalls(t *testing.T, client *adminv1mocks.AdminServiceClientMock) {
	t.Helper()
	enableCalls := client.FirewallEnableCalls()
	bypassCalls := client.FirewallBypassCalls()
	require.Len(t, enableCalls, tc.wantEnableCalls)
	require.Len(t, bypassCalls, tc.wantBypassCalls)

	if tc.wantEnableCalls > 0 {
		assert.Equal(t, tc.wantContainerID, enableCalls[0].In.GetContainerId())
	}
	if tc.wantBypassCalls > 0 {
		assert.Equal(t, tc.wantContainerID, bypassCalls[0].In.GetContainerId())
		assert.Equal(t, tc.wantTimeoutSeconds, bypassCalls[0].In.GetTimeoutSeconds())
	}
}

// TestBypassRun covers the two non-TTY paths: --stop (restore enforcement via
// FirewallEnable, never FirewallBypass) and --non-interactive (start the
// server-side dead-man timer via FirewallBypass with the duration converted to
// whole seconds, and never re-enable). The interactive countdown dashboard is
// deliberately not driven here — it needs a TTY and a real ticker.
//
//nolint:exhaustruct // table rows set only the fields each case exercises
func TestBypassRun(t *testing.T) {
	bypassErr := errors.New("bpf write failed")
	enableErr := errors.New("cp refused")

	tests := []bypassRunCase{
		{
			name:            "stop re-enables enforcement",
			stop:            true,
			projectName:     "acme",
			wantContainerID: "clawker.acme.dev",
			wantEnableCalls: 1,
			wantStdout:      "Bypass stopped for agent dev",
		},
		{
			name:            "stop surfaces rpc failure",
			stop:            true,
			enableErr:       enableErr,
			wantContainerID: "clawker.dev",
			wantEnableCalls: 1,
			wantErrContains: "stopping bypass for dev",
		},
		{
			name:               "non-interactive starts bypass",
			duration:           90 * time.Second,
			wantContainerID:    "clawker.dev",
			wantBypassCalls:    1,
			wantTimeoutSeconds: 90,
			wantStdout:         "Bypass active for agent dev (expires in 1m30s)",
			wantErrOut:         "clawker firewall bypass --stop --agent dev",
		},
		{
			name:               "non-interactive surfaces rpc failure",
			duration:           30 * time.Second,
			bypassErr:          bypassErr,
			wantContainerID:    "clawker.dev",
			wantBypassCalls:    1,
			wantTimeoutSeconds: 30,
			wantErrContains:    "starting bypass for dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, errOut := iostreams.Test()
			client := bypassMockClient(tt.bypassErr, tt.enableErr)
			//nolint:exhaustruct // TUI is unused by bypassRun's non-interactive paths
			opts := &BypassOptions{
				IOStreams:      ios,
				ProjectManager: projectManagerFor(tt.projectName),
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					return client, nil
				},
				Agent:          "dev",
				Duration:       tt.duration,
				Stop:           tt.stop,
				NonInteractive: !tt.stop,
			}

			err := bypassRun(context.Background(), opts)
			tt.assertOutput(t, err, stdout, errOut)
			tt.assertCalls(t, client)
		})
	}
}
