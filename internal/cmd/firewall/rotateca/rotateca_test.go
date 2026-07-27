package rotateca //nolint:testpackage // exercises the unexported run function directly

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
// rotate-ca tests can assert on emitted text.
func testFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	//nolint:exhaustruct // command tests wire only the Factory nouns rotate-ca reads
	f := &cmdutil.Factory{IOStreams: ios}
	return f, out, errOut
}

// rotateCAClient is a mock AdminService client for the single RPC rotate-ca
// drives. A non-nil rpcErr fails the call; otherwise the result carries
// stackRestarted.
func rotateCAClient(stackRestarted bool, rpcErr error) *adminv1mocks.AdminServiceClientMock {
	//nolint:exhaustruct // mock wires only the RPCs this command drives
	return &adminv1mocks.AdminServiceClientMock{
		FirewallRotateCAFunc: func(_ context.Context, _ *adminv1.FirewallRotateCARequest, _ ...grpc.CallOption) (*adminv1.FirewallRotateCAResult, error) {
			if rpcErr != nil {
				return nil, rpcErr
			}
			return &adminv1.FirewallRotateCAResult{StackRestarted: stackRestarted}, nil
		},
	}
}

// TestNewCmdRotateCA asserts the command keeps its `rotate-ca` invocation
// name, is constructed with the Factory nouns rotateCARun needs, and that RunE
// dispatches to the injected runF.
func TestNewCmdRotateCA(t *testing.T) {
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
				return rotateCAClient(true, nil), nil
			}

			var gotOpts *RotateCAOptions
			cmd := NewCmdRotateCA(f, func(_ context.Context, opts *RotateCAOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.SetContext(context.Background())
			cmd.SetArgs(tt.args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			assert.Equal(t, "rotate-ca", cmd.Use, "the user-facing verb must stay hyphenated")

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

// rotateCACase is one row of the rotateCARun table: the mocked server outcome
// plus the error text and stream content it must produce.
type rotateCACase struct {
	name           string
	stackRestarted bool
	rpcErr         error
	dialErr        error
	wantErr        string
	wantOut        []string
	wantErrOut     string
	wantQuietErr   bool
}

// assertOutcome checks a rotateCARun result against the case expectations.
func (c rotateCACase) assertOutcome(t *testing.T, err error, out, errOut *bytes.Buffer) {
	t.Helper()
	if c.wantErr != "" {
		require.Error(t, err)
		assert.Contains(t, err.Error(), c.wantErr)
	} else {
		require.NoError(t, err)
	}
	for _, want := range c.wantOut {
		assert.Contains(t, out.String(), want)
	}
	if c.wantErrOut != "" {
		assert.Contains(t, errOut.String(), c.wantErrOut)
	}
	if c.wantQuietErr {
		assert.Empty(t, errOut.String(), "no note when the live stack was reloaded")
	}
}

// TestRotateCARun covers the rotate outcomes: the success + rebuild advisory
// pair, the deferred-effect note when the stack was down, and the two failure
// paths.
func TestRotateCARun(t *testing.T) {
	rpcErr := errors.New("cp unreachable")
	dialErr := errors.New("dial: boom")

	tests := []rotateCACase{
		{
			name:           "stack restarted prints rotate and rebuild advisory",
			stackRestarted: true,
			rpcErr:         nil,
			dialErr:        nil,
			wantErr:        "",
			wantOut:        []string{"CA certificate rotated", "Rebuild images and recreate containers"},
			wantErrOut:     "",
			wantQuietErr:   true,
		},
		{
			name:           "stack down prints deferred-effect note",
			stackRestarted: false,
			rpcErr:         nil,
			dialErr:        nil,
			wantErr:        "",
			wantOut:        []string{"CA certificate rotated"},
			wantErrOut:     "CA + per-domain certs regenerated on disk; firewall is not running",
			wantQuietErr:   false,
		},
		{
			name:           "rpc failure is wrapped",
			stackRestarted: false,
			rpcErr:         rpcErr,
			dialErr:        nil,
			wantErr:        "rotating CA",
			wantOut:        nil,
			wantErrOut:     "",
			wantQuietErr:   false,
		},
		{
			name:           "dial failure short-circuits",
			stackRestarted: false,
			rpcErr:         nil,
			dialErr:        dialErr,
			wantErr:        "connecting to control plane",
			wantOut:        nil,
			wantErrOut:     "",
			wantQuietErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, out, errOut := testFactory(t)
			client := rotateCAClient(tt.stackRestarted, tt.rpcErr)
			opts := &RotateCAOptions{
				IOStreams: f.IOStreams,
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					if tt.dialErr != nil {
						return nil, tt.dialErr
					}
					return client, nil
				},
			}

			tt.assertOutcome(t, rotateCARun(context.Background(), opts), out, errOut)
		})
	}
}

// TestRotateCARun_UsesQuickDeadline guards that rotate-ca stays on the
// quick-RPC deadline: unlike reload it does not bring the stack up, so it must
// not inherit the much longer bringup budget.
func TestRotateCARun_UsesQuickDeadline(t *testing.T) {
	f, _, _ := testFactory(t)
	client := rotateCAClient(true, nil)
	opts := &RotateCAOptions{
		IOStreams: f.IOStreams,
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return client, nil
		},
	}

	require.NoError(t, rotateCARun(context.Background(), opts))

	require.Len(t, client.FirewallRotateCACalls(), 1)
	deadline, ok := client.FirewallRotateCACalls()[0].Ctx.Deadline()
	require.True(t, ok, "RPC context must carry a deadline")
	assert.Less(t, time.Until(deadline), time.Minute,
		"rotate-ca must use the quick-RPC deadline, not the stack bringup budget")
}
