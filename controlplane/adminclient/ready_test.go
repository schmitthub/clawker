package adminclient_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/adminclient"
)

// healthzServer serves /healthz as not_ready for the first notReady
// requests, then as healthy. It returns the port and the request count.
func healthzServer(t *testing.T, notReady int32) (int, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) <= notReady {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not_ready"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	t.Cleanup(srv.Close)
	return portOf(t, srv.Listener.Addr()), &hits
}

func portOf(t *testing.T, addr net.Addr) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(addr.String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return port
}

func TestAwaitReady_WaitsWhileBooting(t *testing.T) {
	port, hits := healthzServer(t, 2)
	require.NoError(t, adminclient.AwaitReady(context.Background(), port, 10*time.Second))
	require.Equal(t, int32(3), hits.Load())
}

func TestAwaitReady_ReadyReturnsAtOnce(t *testing.T) {
	port, hits := healthzServer(t, 0)
	require.NoError(t, adminclient.AwaitReady(context.Background(), port, 10*time.Second))
	require.Equal(t, int32(1), hits.Load())
}

func TestAwaitReady_GivesUpAfterMaxWait(t *testing.T) {
	port, _ := healthzServer(t, 1<<30)
	err := adminclient.AwaitReady(context.Background(), port, 300*time.Millisecond)
	require.ErrorContains(t, err, "still starting up")
}

func TestAwaitReady_UnreachableDoesNotWait(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := portOf(t, ln.Addr())
	require.NoError(t, ln.Close())
	start := time.Now()
	require.NoError(t, adminclient.AwaitReady(context.Background(), port, 10*time.Second))
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestAwaitReady_UnhealthyDoesNotWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unhealthy","failed_probe":"hydra"}`))
	}))
	t.Cleanup(srv.Close)
	port := portOf(t, srv.Listener.Addr())
	require.NoError(t, adminclient.AwaitReady(context.Background(), port, 10*time.Second))
}
