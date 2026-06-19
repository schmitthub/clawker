package otelcerts

import (
	"testing"

	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/require"
)

// TestNewCPProvisioner_FailureReturnsPlainNilInterface pins the
// degraded-mode landmine: on the failure path the OtelCertProvisioner
// return MUST be a plain interface-typed nil (so the firewall stack's
// `s.otelCerts == nil` guard fires cleanly), NOT a typed-nil
// (*Service)(nil) boxed into the interface — which would pass the guard
// yet dispatch on a nil receiver and panic, stranding eBPF.
//
// In a unit-test environment the container-FS consts paths
// (CPInfraCACertPath etc.) do not exist, so infracerts.Load fails first
// — the earliest failure arm — which is exactly the arm that must not
// box a typed nil. A regression that changed the failure return to
// `return s, s, ...` or boxed a typed-nil *Service would make this go
// red (the interface would compare non-nil).
func TestNewCPProvisioner_FailureReturnsPlainNilInterface(t *testing.T) {
	svc, prov, tlsCfg, err := NewCPProvisioner(logger.Nop())

	require.Error(t, err, "expected failure when the container-FS infra CA paths are absent")
	require.Nil(t, svc, "concrete *Service must be nil on the failure path")
	require.Nil(t, tlsCfg, "*tls.Config must be nil on the failure path")

	// The crux: the interface value itself must be nil, not a non-nil
	// interface boxing a typed-nil pointer. require.Nil unwraps the
	// interface, so assert on the comparison directly too.
	require.Nil(t, prov, "OtelCertProvisioner must be a plain interface-typed nil")
	require.True(t, prov == nil, "interface must compare == nil (no boxed typed-nil)")
}
