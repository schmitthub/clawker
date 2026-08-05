package controlplane

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
)

// ---------------------------------------------------------------------------
// INV-B1-010: eBPF lifecycle ordering preserved
// ---------------------------------------------------------------------------

// Tests INV-B1-010 [unit]: IsReady() starts false and becomes true after SetReady().
func TestINV_B1_010_IsReadyAtomicBool(t *testing.T) {
	orchestrator := NewControlPlane()

	assert.False(t, orchestrator.IsReady(),
		"IsReady() must be false before SetReady() is called")

	orchestrator.SetReady()

	assert.True(t, orchestrator.IsReady(),
		"IsReady() must be true after SetReady() is called")
}

// ---------------------------------------------------------------------------
// Recovery queue: publish → poll → clear
// ---------------------------------------------------------------------------

// TestRecoveryQueue_SubscribeThenPublish pins the watcher-first order:
// a stream connected before the failure happens receives it when
// published, and ClearRecovery (assistance landed) closes the channel —
// the stream's clean end-of-file.
func TestRecoveryQueue_SubscribeThenPublish(t *testing.T) {
	orchestrator := NewControlPlane()
	ch, cancel := orchestrator.SubscribeRecovery()
	defer cancel()

	orchestrator.PublishRecovery("bpffs delegation needed")
	assert.Equal(t, "bpffs delegation needed", <-ch)

	orchestrator.ClearRecovery()
	_, open := <-ch
	assert.False(t, open, "ClearRecovery must close the watcher channel (clean EOF)")
}

// TestRecoveryQueue_SubscribeAfterPublish pins the queue semantics: the
// pending failure sits in the slot, so a watcher that connects AFTER it
// was published still receives it — the CLI's watch goroutine may
// establish the stream at any point during boot.
func TestRecoveryQueue_SubscribeAfterPublish(t *testing.T) {
	orchestrator := NewControlPlane()
	orchestrator.PublishRecovery("bpffs delegation needed")

	ch, cancel := orchestrator.SubscribeRecovery()
	defer cancel()
	assert.Equal(t, "bpffs delegation needed", <-ch)
}

// TestRecoveryQueue_ReadyEndsStreams pins that readiness is terminal for
// the recovery queue: SetReady closes every connected watcher channel,
// and a subscription made after ready is closed on arrival — the CLI
// sees clean end-of-stream, never a hang.
func TestRecoveryQueue_ReadyEndsStreams(t *testing.T) {
	orchestrator := NewControlPlane()
	ch, cancel := orchestrator.SubscribeRecovery()
	defer cancel()

	orchestrator.SetReady()
	_, open := <-ch
	assert.False(t, open, "SetReady must close connected watcher channels")

	late, lateCancel := orchestrator.SubscribeRecovery()
	defer lateCancel()
	_, open = <-late
	assert.False(t, open, "a post-ready subscription must be closed on arrival")
}

// TestRecoveryIdle_WatcherHoldsClock pins the idle-TTL inputs the
// recovery wait loop consumes: publish starts the clock at now (the CLI
// gets a full TTL to connect — a clock left at the zero time would read
// as hours idle and shut the CP down before the CLI ever connected), a
// connected watcher holds it at zero, and a disconnect restarts it
// rather than inheriting the pre-connect reading.
func TestRecoveryIdle_WatcherHoldsClock(t *testing.T) {
	orchestrator := NewControlPlane()

	orchestrator.PublishRecovery("waiting")
	assert.Less(t, orchestrator.RecoveryIdle(), consts.CPSOSIdleTTL,
		"publish must start the idle clock at now, not the zero time")

	ch, cancel := orchestrator.SubscribeRecovery()
	assert.Equal(t, time.Duration(0), orchestrator.RecoveryIdle(),
		"a connected watcher holds the clock at zero")

	cancel()
	assert.Less(t, orchestrator.RecoveryIdle(), consts.CPSOSIdleTTL,
		"a disconnect restarts the clock")
	assert.Equal(t, "waiting", <-ch, "the pending failure was delivered before disconnect")
}

// ---------------------------------------------------------------------------
// INV-B1-013: CP health via HTTP endpoint with hard prerequisites
// ---------------------------------------------------------------------------

// Tests INV-B1-013 [unit]: /healthz transitions 503 -> 200 across the
// single SetReady() boundary — the "no partial states" requirement. This
// is the consolidated 503-before / 200-after case for the HealthzHandler
// path; the atomic-bool and eBPF-gating cases live in their own tests.
func TestINV_B1_013_HealthzOnlyAfterFullInit(t *testing.T) {
	orchestrator := NewControlPlane()
	handler := orchestrator.HealthzHandler()
	require.NotNil(t, handler, "healthz handler must not be nil")

	// Pre-init: must be 503.
	t.Run("pre-init is 503", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	// Post-init: must be 200.
	orchestrator.SetReady()
	t.Run("post-init is 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

// ---------------------------------------------------------------------------
// INV-B1-010: eBPF Load() gates healthz readiness
// ---------------------------------------------------------------------------

// Tests INV-B1-010 [unit]: /healthz returns 503 while eBPF Load() is in progress,
// then 200 after Load completes. This verifies that the startup orchestrator
// correctly gates healthz behind eBPF initialization.
func TestINV_B1_010_EBPFLoadGatesHealthz(t *testing.T) {
	orchestrator := NewControlPlane()
	handler := orchestrator.HealthzHandler()
	require.NotNil(t, handler, "healthz handler must not be nil")

	// loadBlock is a channel that simulates eBPF Load() blocking.
	loadBlock := make(chan struct{})
	var loadWg sync.WaitGroup
	loadWg.Add(1)

	// Simulate the orchestrator starting eBPF Load() in a goroutine.
	go func() {
		defer loadWg.Done()
		// Block until signaled (simulates eBPF Load() taking time).
		<-loadBlock
		// After Load() completes, mark ready.
		orchestrator.SetReady()
	}()

	// While Load() is blocked, /healthz must return 503.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"/healthz must return 503 while eBPF Load() is in progress")

	// Unblock Load().
	close(loadBlock)
	loadWg.Wait()

	// After Load() completes, /healthz must return 200.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, rec.Code,
		"/healthz must return 200 after eBPF Load() completes")
}

// ---------------------------------------------------------------------------
// grpc-admin probe: serving, not merely bound
// ---------------------------------------------------------------------------

// TestGRPCAdminProbe_RequiresServing pins the false-healthy fix. The admin
// listener socket is bound at gRPC stack construction and only starts serving
// after the startup gates, so a bare TCP dial completes out of the accept
// backlog either way — /healthz reported healthy while every admin RPC hung.
// The probe now gates the dial on the stack's serving signal, and fails
// closed until that signal is installed.
func TestGRPCAdminProbe_RequiresServing(t *testing.T) {
	lis, err := net.Listen("tcp", consts.Localhost+":0")
	require.NoError(t, err)
	t.Cleanup(func() {
		if cerr := lis.Close(); cerr != nil {
			t.Logf("close listener: %v", cerr)
		}
	})

	orchestrator := NewControlPlane()
	p := serviceProbe{name: "grpc-admin", addr: lis.Addr().String(), tls: false, check: orchestrator.adminServing}

	// Sanity: the dial the probe used to rely on alone succeeds against the
	// bound-but-unserved listener — that is the whole false-healthy hazard.
	conn, err := net.DialTimeout("tcp", p.addr, orchestrator.timeout)
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	assert.False(t, orchestrator.probe(p), "must fail closed before the serving check is installed")

	orchestrator.SetAdminServingCheck(func() bool { return false })
	assert.False(t, orchestrator.probe(p), "bound but not serving must not report healthy")

	orchestrator.SetAdminServingCheck(func() bool { return true })
	assert.True(t, orchestrator.probe(p), "serving and reachable must report healthy")
}
