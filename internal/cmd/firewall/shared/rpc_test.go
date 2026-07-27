package shared_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/schmitthub/clawker/internal/cmd/firewall/shared"
)

// reasonErr builds a gRPC status error carrying one errdetails.ErrorInfo per
// reason — the wire shape the CP's toStatus produces.
func reasonErr(t *testing.T, msg string, reasons ...string) error {
	t.Helper()
	st := status.New(codes.FailedPrecondition, msg)
	for _, r := range reasons {
		var err error
		//nolint:exhaustruct // only Reason participates in CLI dispatch
		st, err = st.WithDetails(&errdetails.ErrorInfo{Reason: r})
		require.NoError(t, err)
	}
	return st.Err()
}

// TestWrapRPCError_RemediationCatalog locks the Reason → remediation
// dispatch: the CLI's entire recovery guidance rides on these wire strings,
// so a CP-side Reason rename must fail here, not silently degrade every
// firewall command to a bare status message.
func TestWrapRPCError_RemediationCatalog(t *testing.T) {
	tests := []struct {
		reason   string
		wantHint string
	}{
		{reason: "CP_NOT_RUNNING", wantHint: "clawker controlplane up"},
		{reason: "QUEUE_CLOSED", wantHint: "shutting down"},
		{reason: "FIREWALL_NOT_INITIALIZED", wantHint: "clawker firewall up"},
		{reason: "CONTAINER_GONE", wantHint: "no longer exists"},
		{reason: "RULE_INVALID", wantHint: "domain syntax"},
		{reason: "RULE_STORE_WRITE", wantHint: "not persisted"},
		{reason: "CERT_REGEN", wantHint: "rotate-ca"},
		{reason: "STACK_PROBE", wantHint: "Docker daemon"},
		{reason: "CONFIG_REGEN", wantHint: "NOT restarted"},
		{reason: "ENVOY_RESTART", wantHint: "clawker-envoy"},
		{reason: "COREDNS_RESTART", wantHint: "clawker-coredns"},
		{reason: "STACK_UNHEALTHY", wantHint: "clawker firewall status"},
		{reason: "ROUTE_SYNC", wantHint: "clawker firewall reload"},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			err := shared.WrapRPCError("doing the thing", reasonErr(t, "boom", tt.reason))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "doing the thing")
			assert.Contains(t, err.Error(), tt.wantHint,
				"Reason %s must map to its remediation line", tt.reason)
		})
	}
}

func TestWrapRPCError_EdgeShapes(t *testing.T) {
	t.Run("nil error passes through", func(t *testing.T) {
		require.NoError(t, shared.WrapRPCError("header", nil))
	})

	t.Run("multiple reasons all surface", func(t *testing.T) {
		err := shared.WrapRPCError("reconciling",
			reasonErr(t, "boom", "ENVOY_RESTART", "COREDNS_RESTART"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "clawker-envoy")
		assert.Contains(t, err.Error(), "clawker-coredns")
	})

	t.Run("unknown reason falls back to status message", func(t *testing.T) {
		err := shared.WrapRPCError("doing the thing", reasonErr(t, "boom", "SOME_FUTURE_REASON"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom",
			"an unmapped Reason must surface the status message, never vanish")
	})

	t.Run("no typed details falls back to status message", func(t *testing.T) {
		err := shared.WrapRPCError("doing the thing", status.Error(codes.Unavailable, "dial refused"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dial refused")
	})

	t.Run("plain error keeps its text", func(t *testing.T) {
		err := shared.WrapRPCError("doing the thing", errors.New("plain failure"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plain failure")
	})
}
