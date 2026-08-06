package up //nolint:testpackage // exercises the unexported run function directly

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
	cpmanager "github.com/schmitthub/clawker/controlplane/manager"
	cpmanagermocks "github.com/schmitthub/clawker/controlplane/manager/mocks"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
)

func newTestFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	ios, _, _, _ := iostreams.Test() //nolint:dogsled // iostreams.Test returns three buffers this helper does not assert on
	//nolint:exhaustruct // factory wires only the fields the up command reads
	return &cmdutil.Factory{
		IOStreams: ios,
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
		//nolint:exhaustruct // mock wires only the lifecycle calls up drives
		ControlPlane: func(context.Context) (cpmanager.Manager, error) { return &cpmanagermocks.ManagerMock{}, nil },
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return initClient(), nil
		},
	}
}

// initClient is a mock AdminService client for the single RPC up drives.
func initClient() *adminv1mocks.AdminServiceClientMock {
	//nolint:exhaustruct // mock wires only the RPCs this command drives
	return &adminv1mocks.AdminServiceClientMock{
		FirewallInitFunc: func(_ context.Context, _ *adminv1.FirewallInitRequest, _ ...grpc.CallOption) (*adminv1.FirewallInitResult, error) {
			return &adminv1.FirewallInitResult{
				EnvoyIp:   "10.9.0.2",
				CorednsIp: "10.9.0.3",
				NetworkId: "netid-abc",
			}, nil
		},
	}
}

func TestNewCmdUp(t *testing.T) {
	tests := []struct {
		name  string
		input []string
	}{
		{
			name:  "no flags",
			input: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestFactory(t)

			var gotOpts *UpOptions
			cmd := NewCmdUp(f, func(_ context.Context, opts *UpOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.input)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			require.NoError(t, cmd.Execute())
			require.NotNil(t, gotOpts)
			assert.NotNil(t, gotOpts.IOStreams)
			assert.NotNil(t, gotOpts.ControlPlane)
			assert.NotNil(t, gotOpts.AdminClient)
		})
	}
}

// TestUpRun covers the CP-bootstrap-before-dial ordering contract that
// `firewall up` owns: Manager.Start MUST fire before any AdminClient dial so
// the RPC hits a live CP instead of fail-fast, and a CP bootstrap failure MUST
// short-circuit before the dial — no point dialing a CP that refused to come
// up.
func TestUpRun(t *testing.T) {
	bootErr := errors.New("cp healthz timed out")
	dialErr := errors.New("stub dial refused")

	tests := []struct {
		name            string
		ensureErr       error
		dialErr         error
		wantErr         bool
		wantErrIs       error
		wantErrContains string
		wantAdminCalled bool
		wantStdout      string
	}{
		{
			name:            "ensures control plane before dial",
			ensureErr:       nil,
			dialErr:         dialErr,
			wantErr:         true,
			wantErrIs:       dialErr,
			wantErrContains: "connecting to control plane",
			wantAdminCalled: true,
			wantStdout:      "",
		},
		{
			name:            "fails fast when cp bootstrap fails",
			ensureErr:       bootErr,
			dialErr:         nil,
			wantErr:         true,
			wantErrIs:       bootErr,
			wantErrContains: "bringing control plane up",
			wantAdminCalled: false,
			wantStdout:      "",
		},
		{
			name:            "brings the stack up",
			ensureErr:       nil,
			dialErr:         nil,
			wantErr:         false,
			wantErrIs:       nil,
			wantErrContains: "",
			wantAdminCalled: true,
			wantStdout:      "Firewall stack up",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, _ := iostreams.Test()
			//nolint:exhaustruct // mock wires only the lifecycle calls up drives
			mgr := &cpmanagermocks.ManagerMock{
				StartFunc: func(_ context.Context) error { return tt.ensureErr },
			}
			adminCalled := false
			opts := &UpOptions{
				IOStreams:    ios,
				ControlPlane: func(context.Context) (cpmanager.Manager, error) { return mgr, nil },
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					adminCalled = true
					if tt.dialErr != nil {
						return nil, tt.dialErr
					}
					return initClient(), nil
				},
			}

			err := upRun(context.Background(), opts)
			if tt.wantErr {
				require.ErrorIs(t, err, tt.wantErrIs)
				assert.Contains(t, err.Error(), tt.wantErrContains)
			} else {
				require.NoError(t, err)
			}

			assert.Len(t, mgr.StartCalls(), 1, "Start must fire exactly once")
			assert.Equal(t, tt.wantAdminCalled, adminCalled)
			if tt.wantStdout != "" {
				assert.Contains(t, stdout.String(), tt.wantStdout)
				assert.Contains(t, stdout.String(), "10.9.0.2")
			}
		})
	}
}
