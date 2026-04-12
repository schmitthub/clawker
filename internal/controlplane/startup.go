package controlplane

import (
	"net/http"
	"sync/atomic"
)

// CPStartupOrchestrator manages the control plane's subprocess startup
// sequence and health reporting.
type CPStartupOrchestrator struct {
	ready atomic.Bool
}

// NewCPStartupOrchestrator creates a new startup orchestrator.
func NewCPStartupOrchestrator() *CPStartupOrchestrator {
	return &CPStartupOrchestrator{}
}

// IsReady returns whether the CP has completed all startup steps.
func (o *CPStartupOrchestrator) IsReady() bool {
	return o.ready.Load()
}

// SetReady marks the CP as ready. Called after all startup steps
// (subprocesses, eBPF load, gRPC server) have succeeded.
func (o *CPStartupOrchestrator) SetReady() {
	o.ready.Store(true)
}

// HealthzHandler returns an http.Handler for the /healthz endpoint.
// Returns 200 only when IsReady() is true, 503 otherwise.
func (o *CPStartupOrchestrator) HealthzHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if o.ready.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
}
