package disable //nolint:testpackage // exercises the unexported run function directly

import (
	"bytes"
	"context"
	"errors"
	"testing"

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
	//nolint:exhaustruct // factory wires only the fields the disable command reads
	return &cmdutil.Factory{
		IOStreams:      ios,
		ProjectManager: projectManagerFor(""),
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return disableClient(nil), nil
		},
	}
}

// disableClient is a mock AdminService client for the single RPC disable
// drives. rpcErr, when non-nil, is returned instead of a result.
func disableClient(rpcErr error) *adminv1mocks.AdminServiceClientMock {
	//nolint:exhaustruct // mock wires only the RPCs this command drives
	return &adminv1mocks.AdminServiceClientMock{
		FirewallDisableFunc: func(_ context.Context, _ *adminv1.FirewallDisableRequest, _ ...grpc.CallOption) (*adminv1.FirewallDisableResult, error) {
			if rpcErr != nil {
				return nil, rpcErr
			}
			//nolint:exhaustruct // result carries no fields the command reads
			return &adminv1.FirewallDisableResult{}, nil
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

//nolint:exhaustruct // table rows set only the fields each case exercises
func TestNewCmdDisable(t *testing.T) {
	tests := []struct {
		name      string
		input     []string
		wantAgent string
		wantErr   bool
	}{
		{
			name:      "agent flag",
			input:     []string{"--agent", "dev"},
			wantAgent: "dev",
		},
		{
			name:    "missing required agent",
			input:   nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestFactory(t)

			var gotOpts *DisableOptions
			cmd := NewCmdDisable(f, func(_ context.Context, opts *DisableOptions) error {
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
				assert.Nil(t, gotOpts, "runF must not fire when flag validation fails")
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			assert.Equal(t, tt.wantAgent, gotOpts.Agent)
			assert.NotNil(t, gotOpts.IOStreams)
			assert.NotNil(t, gotOpts.AdminClient)
		})
	}
}

// TestDisableRun asserts the container ref the CLI puts on the wire (the CP
// resolves it to a cgroup server-side) and that an RPC failure is surfaced
// with the agent-scoped header rather than swallowed.
//
//nolint:exhaustruct // table rows set only the fields each case exercises
func TestDisableRun(t *testing.T) {
	rpcErr := errors.New("cp refused")

	tests := []struct {
		name            string
		agent           string
		projectName     string
		rpcErr          error
		wantContainerID string
		wantErrContains string
		wantStdout      string
	}{
		{
			name:            "disables within a project",
			agent:           "dev",
			projectName:     "acme",
			wantContainerID: "clawker.acme.dev",
			wantStdout:      "Firewall disabled for agent dev",
		},
		{
			name:            "disables outside a project",
			agent:           "dev",
			wantContainerID: "clawker.dev",
			wantStdout:      "Firewall disabled for agent dev",
		},
		{
			name:            "surfaces rpc failure",
			agent:           "dev",
			rpcErr:          rpcErr,
			wantContainerID: "clawker.dev",
			wantErrContains: "disabling firewall for dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, _ := iostreams.Test()
			client := disableClient(tt.rpcErr)
			opts := &DisableOptions{
				IOStreams:      ios,
				ProjectManager: projectManagerFor(tt.projectName),
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					return client, nil
				},
				Agent: tt.agent,
			}

			err := disableRun(context.Background(), opts)
			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
			} else {
				require.NoError(t, err)
				assert.Contains(t, stdout.String(), tt.wantStdout)
			}

			calls := client.FirewallDisableCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, tt.wantContainerID, calls[0].In.GetContainerId())
		})
	}
}
