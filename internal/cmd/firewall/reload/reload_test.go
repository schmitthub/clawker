package reload //nolint:testpackage // exercises the unexported run function directly

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
)

// testFactory returns a Factory plus the captured stdout/stderr buffers so
// reload tests can assert on emitted text.
func testFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	//nolint:exhaustruct // command tests wire only the Factory nouns reload reads
	f := &cmdutil.Factory{IOStreams: ios}
	return f, out, errOut
}

// reloadClient is a mock AdminService client for the single RPC reload drives.
// A non-nil rpcErr fails the call; otherwise the result carries stackRestarted.
func reloadClient(stackRestarted bool, rpcErr error) *adminv1mocks.AdminServiceClientMock {
	//nolint:exhaustruct // mock wires only the RPCs this command drives
	return &adminv1mocks.AdminServiceClientMock{
		FirewallReloadFunc: func(_ context.Context, _ *adminv1.FirewallReloadRequest, _ ...grpc.CallOption) (*adminv1.FirewallReloadResult, error) {
			if rpcErr != nil {
				return nil, rpcErr
			}
			return &adminv1.FirewallReloadResult{StackRestarted: stackRestarted}, nil
		},
	}
}

// TestNewCmdReload asserts the command is constructed with the Factory nouns
// reloadRun needs and that RunE dispatches to the injected runF.
func TestNewCmdReload(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErr    bool
		wantCalled bool
	}{
		{name: "no args dispatches to runF", args: []string{}, wantErr: false, wantCalled: true},
		{name: "unknown flag rejected", args: []string{"--nope"}, wantErr: true, wantCalled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _, _ := testFactory(t)
			f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
				return reloadClient(true, nil), nil
			}

			var gotOpts *ReloadOptions
			cmd := NewCmdReload(f, func(_ context.Context, opts *ReloadOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.SetContext(context.Background())
			cmd.SetArgs(tt.args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			err := cmd.Execute()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if !tt.wantCalled {
				assert.Nil(t, gotOpts, "runF must not run when flag parsing fails")
				return
			}
			require.NotNil(t, gotOpts)
			assert.Same(t, f.IOStreams, gotOpts.IOStreams)
			assert.NotNil(t, gotOpts.AdminClient)
		})
	}
}

// reloadCase is one row of the reloadRun table: the mocked server outcome plus
// the error text and stream content it must produce.
type reloadCase struct {
	name           string
	stackRestarted bool
	rpcErr         error
	dialErr        error
	wantErr        string
	wantOut        string
	wantErrOut     string
	wantQuietErr   bool
}

// assertOutcome checks a reloadRun result against the case expectations.
func (c reloadCase) assertOutcome(t *testing.T, err error, out, errOut *bytes.Buffer) {
	t.Helper()
	if c.wantErr != "" {
		require.Error(t, err)
		assert.Contains(t, err.Error(), c.wantErr)
	} else {
		require.NoError(t, err)
	}
	if c.wantOut != "" {
		assert.Contains(t, out.String(), c.wantOut)
	}
	if c.wantErrOut != "" {
		assert.Contains(t, errOut.String(), c.wantErrOut)
	}
	if c.wantQuietErr {
		assert.Empty(t, errOut.String(), "no note when the live stack was reloaded")
	}
}

// TestReloadRun covers the reload outcomes: the live-reload success line, the
// deferred-effect note when the stack was down, and the two failure paths.
func TestReloadRun(t *testing.T) {
	rpcErr := errors.New("cp unreachable")
	dialErr := errors.New("dial: boom")

	tests := []reloadCase{
		{
			name:           "stack restarted prints success only",
			stackRestarted: true,
			rpcErr:         nil,
			dialErr:        nil,
			wantErr:        "",
			wantOut:        "Firewall configuration reloaded",
			wantErrOut:     "",
			wantQuietErr:   true,
		},
		{
			name:           "stack down prints deferred-effect note",
			stackRestarted: false,
			rpcErr:         nil,
			dialErr:        nil,
			wantErr:        "",
			wantOut:        "Firewall configuration reloaded",
			wantErrOut:     "configuration regenerated; firewall is not running",
			wantQuietErr:   false,
		},
		{
			name:           "rpc failure is wrapped",
			stackRestarted: false,
			rpcErr:         rpcErr,
			dialErr:        nil,
			wantErr:        "reloading firewall",
			wantOut:        "",
			wantErrOut:     "",
			wantQuietErr:   false,
		},
		{
			name:           "dial failure short-circuits",
			stackRestarted: false,
			rpcErr:         nil,
			dialErr:        dialErr,
			wantErr:        "connecting to control plane",
			wantOut:        "",
			wantErrOut:     "",
			wantQuietErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, out, errOut := testFactory(t)
			client := reloadClient(tt.stackRestarted, tt.rpcErr)
			opts := &ReloadOptions{
				IOStreams: f.IOStreams,
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					if tt.dialErr != nil {
						return nil, tt.dialErr
					}
					return client, nil
				},
			}

			tt.assertOutcome(t, reloadRun(context.Background(), opts), out, errOut)
		})
	}
}

// TestReloadRun_UsesBringupDeadline guards the timeout the flat command
// carried: FirewallReload runs Stack.WaitForHealthy server-side, so it must
// get the bringup RPC deadline rather than the quick-RPC default. Asserted
// through the deadline the run function put on the RPC context.
func TestReloadRun_UsesBringupDeadline(t *testing.T) {
	f, _, _ := testFactory(t)
	client := reloadClient(true, nil)
	opts := &ReloadOptions{
		IOStreams: f.IOStreams,
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return client, nil
		},
	}

	require.NoError(t, reloadRun(context.Background(), opts))

	require.Len(t, client.FirewallReloadCalls(), 1)
	deadline, ok := client.FirewallReloadCalls()[0].Ctx.Deadline()
	require.True(t, ok, "RPC context must carry a deadline")
	assert.Greater(t, time.Until(deadline), time.Minute,
		"reload must use the stack bringup deadline, not the quick-RPC default")
}
