package controlplane

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/schmitthub/clawker/internal/config"
)

// CPStartupOrchestrator manages the control plane's subprocess startup
// sequence and health reporting. The /healthz endpoint actively probes
// all internal service ports — it only returns 200 when every service
// is responding.
type CPStartupOrchestrator struct {
	ready   atomic.Bool
	probes  []serviceProbe
	tlsCfg  *tls.Config
	timeout time.Duration
}

// serviceProbe defines a TCP or HTTPS endpoint to check.
type serviceProbe struct {
	name string
	addr string
	tls  bool
}

// NewCPStartupOrchestrator creates a new startup orchestrator. The probes
// are configured later via SetServiceProbes once TLS config and port
// values are available.
func NewCPStartupOrchestrator() *CPStartupOrchestrator {
	return &CPStartupOrchestrator{
		timeout: 2 * time.Second,
	}
}

// SetServiceProbes configures the aggregate health probes from the
// ControlPlaneSettings. Called during CP startup after TLS config is built.
// All Ory services use HTTPS; the gRPC admin port is probed via raw TCP
// (gRPC health check would require a client).
func (o *CPStartupOrchestrator) SetServiceProbes(cp config.ControlPlaneSettings, tlsCfg *tls.Config) {
	o.tlsCfg = tlsCfg
	o.probes = []serviceProbe{
		{name: "hydra-public", addr: fmt.Sprintf("127.0.0.1:%d", cp.HydraPublicPort), tls: true},
		{name: "hydra-admin", addr: fmt.Sprintf("127.0.0.1:%d", cp.HydraAdminPort), tls: true},
		{name: "kratos-public", addr: fmt.Sprintf("127.0.0.1:%d", cp.KratosPublicPort), tls: true},
		{name: "kratos-admin", addr: fmt.Sprintf("127.0.0.1:%d", cp.KratosAdminPort), tls: true},
		{name: "oathkeeper-proxy", addr: fmt.Sprintf("127.0.0.1:%d", cp.OathkeeperPort), tls: true},
		{name: "oathkeeper-api", addr: fmt.Sprintf("127.0.0.1:%d", cp.OathkeeperAPIPort), tls: true},
		{name: "grpc-admin", addr: fmt.Sprintf("127.0.0.1:%d", cp.AdminPort), tls: false},
	}
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
// Returns 200 only when SetReady was called AND all service probes pass.
// If any service is down, returns 503.
func (o *CPStartupOrchestrator) HealthzHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !o.ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		// Active aggregate probe — every service must respond.
		for _, p := range o.probes {
			if !o.probe(p) {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}

		w.WriteHeader(http.StatusOK)
	})
}

// probe checks if a single service endpoint is responding.
func (o *CPStartupOrchestrator) probe(p serviceProbe) bool {
	if p.tls && o.tlsCfg != nil {
		conn, err := tls.DialWithDialer(
			&net.Dialer{Timeout: o.timeout},
			"tcp", p.addr, o.tlsCfg,
		)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}
	conn, err := net.DialTimeout("tcp", p.addr, o.timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
