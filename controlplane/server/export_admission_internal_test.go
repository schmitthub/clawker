package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
)

// TestExportAdmissionUnary pins the process-wide ExportTelemetry cap: the
// call past the limit is refused with Unavailable (retryable) while the others
// are still in the handler, other methods pass through while the lane is
// full, and a released slot admits the next call.
func TestExportAdmissionUnary(t *testing.T) {
	const limit = maxConcurrentExports
	admit := exportAdmissionUnary(limit)
	exportInfo := &grpc.UnaryServerInfo{Server: nil, FullMethod: agentv1.AgentService_ExportTelemetry_FullMethodName}
	registerInfo := &grpc.UnaryServerInfo{Server: nil, FullMethod: agentv1.AgentService_Register_FullMethodName}

	entered := make(chan struct{}, limit)
	release := make(chan struct{})
	blocking := func(ctx context.Context, _ any) (any, error) {
		entered <- struct{}{}
		<-release
		return "ok", ctx.Err()
	}

	var wg sync.WaitGroup
	results := make(chan error, limit)
	for range limit {
		wg.Go(func() {
			_, err := admit(context.Background(), nil, exportInfo, blocking)
			results <- err
		})
	}
	for range limit {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("admitted calls did not reach the handler")
		}
	}

	_, err := admit(context.Background(), nil, exportInfo, blocking)
	require.Equal(t, codes.Unavailable, status.Code(err), "call %d must be refused: %v", limit+1, err)

	passthrough := false
	_, err = admit(context.Background(), nil, registerInfo, func(context.Context, any) (any, error) {
		passthrough = true
		return "ok", nil
	})
	require.NoError(t, err)
	require.True(t, passthrough, "a non-Export method must not consume a slot")

	close(release)
	wg.Wait()
	for range limit {
		require.NoError(t, <-results)
	}

	_, err = admit(context.Background(), nil, exportInfo, func(context.Context, any) (any, error) {
		return "ok", nil
	})
	require.NoError(t, err, "a released slot admits the next call")
}
