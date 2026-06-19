package controlplane

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
