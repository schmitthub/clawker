package down //nolint:testpackage // exercises the unexported run function directly

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
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
)

// removeClient is a mock AdminService client for the single RPC down drives.
// rpcErr drives the FirewallRemove outcome; requests are read back via moq's
// recorded Calls accessors.
func removeClient(rpcErr error) *adminv1mocks.AdminServiceClientMock {
	//nolint:exhaustruct // mock wires only the RPCs this command drives
	return &adminv1mocks.AdminServiceClientMock{
		FirewallRemoveFunc: func(_ context.Context, _ *adminv1.FirewallRemoveRequest, _ ...grpc.CallOption) (*adminv1.FirewallRemoveResult, error) {
			if rpcErr != nil {
				return nil, rpcErr
			}
			return &adminv1.FirewallRemoveResult{}, nil
		},
	}
}

func TestNewCmdDown(t *testing.T) {
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
			ios, _, _, _ := iostreams.Test()
			//nolint:exhaustruct // factory wires only the fields the down command reads
			f := &cmdutil.Factory{
				IOStreams: ios,
				Logger: func() (*logger.Logger, error) {
					return logger.Nop(), nil
				},
				Client: func(_ context.Context) (*docker.Client, error) {
					return nil, errors.New("unused")
				},
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					return removeClient(nil), nil
				},
			}

			var gotOpts *DownOptions
			cmd := NewCmdDown(f, func(_ context.Context, opts *DownOptions) error {
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
			assert.NotNil(t, gotOpts.Client)
			assert.NotNil(t, gotOpts.AdminClient)
		})
	}
}

// TestDownRun covers the teardown contract: FirewallRemove is invoked once and
// the success line lands on stdout; an RPC failure is wrapped and returned so
// Cobra renders it through the centralized error path; and a failed AdminClient
// dial short-circuits with the expected wrapping message before any RPC is made.
func TestDownRun(t *testing.T) {
	rpcErr := errors.New("cp unreachable")
	dialErr := errors.New("dial: boom")

	tests := []struct {
		name            string
		dialErr         error
		rpcErr          error
		wantErr         bool
		wantErrIs       error
		wantErrContains string
		wantRemoveCalls int
		wantStdout      string
	}{
		{
			name:            "sends firewall remove",
			dialErr:         nil,
			rpcErr:          nil,
			wantErr:         false,
			wantErrIs:       nil,
			wantErrContains: "",
			wantRemoveCalls: 1,
			wantStdout:      "Firewall stopped",
		},
		{
			name:            "propagates rpc error",
			dialErr:         nil,
			rpcErr:          rpcErr,
			wantErr:         true,
			wantErrIs:       rpcErr,
			wantErrContains: "stopping firewall",
			wantRemoveCalls: 1,
			wantStdout:      "",
		},
		{
			name:            "client connect error",
			dialErr:         dialErr,
			rpcErr:          nil,
			wantErr:         true,
			wantErrIs:       dialErr,
			wantErrContains: "connecting to control plane",
			wantRemoveCalls: 0,
			wantStdout:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, _ := iostreams.Test()
			mock := removeClient(tt.rpcErr)
			opts := &DownOptions{
				IOStreams: ios,
				// nil Client skips the CP-running short-circuit so the run
				// function goes straight to the AdminClient dial.
				Client: nil,
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					if tt.dialErr != nil {
						return nil, tt.dialErr
					}
					return mock, nil
				},
			}

			err := downRun(context.Background(), opts)
			if tt.wantErr {
				require.ErrorIs(t, err, tt.wantErrIs)
				assert.Contains(t, err.Error(), tt.wantErrContains)
			} else {
				require.NoError(t, err)
			}

			assert.Len(t, mock.FirewallRemoveCalls(), tt.wantRemoveCalls)
			if tt.wantStdout != "" {
				assert.Contains(t, stdout.String(), tt.wantStdout)
			}
		})
	}
}
