package otelcerts

import (
	"crypto/tls"
	"fmt"
	"os"

	fwhandler "github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/controlplane/infracerts"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// NewCPProvisioner builds the CP's trusted-lane OTel cert chain and
// returns the three handles the orchestrator wires up:
//
//   - the concrete *Service, retained by the caller for the
//     post-logger.New SetLogger wiring (LoadTLSConfig's closure reads
//     s.log lazily at handshake time, so a Service constructed before
//     logger.New still surfaces per-handshake mint failures once the
//     real logger is attached);
//   - the fwhandler.OtelCertProvisioner interface view, handed to the
//     firewall stack;
//   - the in-process *tls.Config ("cp" leaf), wired into the logger's
//     OTLP exporter — which has no hot-reconfig hook, so the TLSConfig
//     must exist at logger.New time.
//
// It folds the former cmd.go Phase 1 cert chain:
// infracerts.Load -> consts.OtelClientsDir -> read CLI root CA ->
// otelcerts.New -> LoadTLSConfig("cp").
//
// LANDMINE — degraded-mode signaling (see this package's CLAUDE.md and
// /controlplane/CLAUDE.md "CP crashing is a security incident"): on ANY
// failure the OtelCertProvisioner return MUST be a plain interface-typed
// nil, never a typed-nil (*Service)(nil) boxed into the interface. A
// boxed typed-nil passes the firewall stack's `s.otelCerts == nil` guard
// while still dispatching EnsureClient/LoadTLSConfig on a nil receiver —
// turning the intended degraded mode into a panic that strands eBPF. The
// named return `prov` is left at its interface zero value (plain nil) and
// only assigned `prov = svc` on the success path; the concrete *Service
// and *tls.Config returns are likewise left nil on failure so the caller
// drops OtelOptions entirely rather than half-init mTLS.
//
// All failures are returned as errors (fail-closed, never panic). The
// caller emits the structured event=otelcerts_unavailable line after
// logger.New so it lands in the operator log surface rather than stderr.
func NewCPProvisioner(log *logger.Logger) (svc *Service, prov fwhandler.OtelCertProvisioner, tlsCfg *tls.Config, err error) {
	issuer, err := infracerts.Load(consts.CPInfraCACertPath, consts.CPInfraCAKeyPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("infracerts load: %w", err)
	}

	otelDir, err := consts.OtelClientsDir()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolving otel-clients dir: %w", err)
	}

	rootCABytes, err := os.ReadFile(consts.CPCACertPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading CLI root CA at %s: %w", consts.CPCACertPath, err)
	}

	s, err := New(issuer, otelDir, rootCABytes, log)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("constructing otelcerts.Service: %w", err)
	}

	cfg, err := s.LoadTLSConfig("cp")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("building cp tls.Config: %w", err)
	}

	// Success path only: box the concrete *Service into the interface.
	return s, s, cfg, nil
}
