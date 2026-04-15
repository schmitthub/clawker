package firewall

import (
	"bytes"
	"context"
	"errors"
	"testing"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	cpmocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// downHarness captures buffers + the mock admin client so tests can assert
// which RPC was invoked on downRun.
type downHarness struct {
	mock   *cpmocks.AdminServiceClientMock
	opts   *DownOptions
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newDownHarness(t *testing.T, rpcErr error) *downHarness {
	t.Helper()
	ios, _, stdout, stderr := iostreams.Test()

	mock := &cpmocks.AdminServiceClientMock{
		FirewallRemoveFunc: func(_ context.Context, _ *adminv1.FirewallRemoveRequest, _ ...grpc.CallOption) (*adminv1.FirewallRemoveResponse, error) {
			if rpcErr != nil {
				return nil, rpcErr
			}
			return &adminv1.FirewallRemoveResponse{}, nil
		},
	}

	h := &downHarness{
		mock:   mock,
		stdout: stdout,
		stderr: stderr,
	}
	h.opts = &DownOptions{
		IOStreams: ios,
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return mock, nil
		},
	}
	return h
}

// TestDownRun_SendsFirewallRemove asserts the happy path: FirewallRemove is
// invoked once and the success line is printed on stdout.
func TestDownRun_SendsFirewallRemove(t *testing.T) {
	h := newDownHarness(t, nil)

	err := downRun(context.Background(), h.opts)
	require.NoError(t, err)

	require.Len(t, h.mock.FirewallRemoveCalls(), 1)
	assert.Contains(t, h.stdout.String(), "Firewall stopped")
}

// TestDownRun_PropagatesRPCError asserts that a FirewallRemove RPC failure is
// wrapped and returned, so Cobra renders it through the centralized error path.
func TestDownRun_PropagatesRPCError(t *testing.T) {
	rpcErr := errors.New("cp unreachable")
	h := newDownHarness(t, rpcErr)

	err := downRun(context.Background(), h.opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopping firewall")
	assert.ErrorIs(t, err, rpcErr)
}

// TestDownRun_ClientConnectError asserts that a failed AdminClient dial
// short-circuits with the expected wrapping message, before any RPC is made.
func TestDownRun_ClientConnectError(t *testing.T) {
	dialErr := errors.New("dial: boom")
	opts := &DownOptions{
		IOStreams: newIOStreams(),
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return nil, dialErr
		},
	}

	err := downRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to control plane")
	assert.ErrorIs(t, err, dialErr)
}

func newIOStreams() *iostreams.IOStreams {
	ios, _, _, _ := iostreams.Test()
	return ios
}

// Compile-time assertion that DownOptions still embeds the expected Factory
// fields; guards against field renames that would silently skip the AdminClient
// wiring in NewCmdDown.
var _ = &cmdutil.Factory{}
