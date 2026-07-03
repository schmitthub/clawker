package firewall

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// fakeOtelProvisioner is a deterministic OtelCertProvisioner for tests.
// It records each EnsureClient call and can be configured to fail the
// next call without disturbing the recorded history.
//
// Production wiring passes *otelcerts.Service; the actual write +
// pair-check + perm-shape contract is unit-tested in
// /controlplane/otelcerts/otelcerts_test.go. The tests in this
// file only assert firewall.Stack's dispatch behavior:
//   - dispatches once per sibling service (envoy, coredns)
//   - flips infraCertsReady true on success
//   - leaves infraCertsReady false on mint failure
//   - skips dispatch entirely when the provisioner is nil
type fakeOtelProvisioner struct {
	calls    []string
	failNext bool
}

func (f *fakeOtelProvisioner) EnsureClient(svc string) (string, string, string, error) {
	f.calls = append(f.calls, svc)
	if f.failNext {
		return "", "", "", errors.New("synthetic mint failure")
	}
	return "/tmp/" + svc + "/client.pem",
		"/tmp/" + svc + "/client.key",
		"/tmp/" + svc + "/ca.pem", nil
}

// TestStack_ensureInfraClientCerts_DispatchesPerService pins the
// firewall-side contract: Stack calls EnsureClient once per sibling
// service in the fixed envoy → coredns order.
func TestStack_ensureInfraClientCerts_DispatchesPerService(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)
	prov := &fakeOtelProvisioner{}
	s := NewStack(nil, cfg, logger.Nop(), nil, prov)

	require.NoError(t, s.ensureInfraClientCerts())
	assert.Equal(t, []string{"envoy", "coredns"}, prov.calls)
}

// TestStack_ensureConfigs_InfraCertsReadyLifecycle pins the orchestrator
// behavior in ensureConfigs around the infraCertsReady flag:
//   - reset to false at entry
//   - flip to true only after a successful mint
//   - stay false after a mint failure
//   - re-flip true on the next successful reload
//
// Without this, a re-ordering bug that latches the flag after the first
// successful reload would mask a subsequent mint failure and silently
// wire Envoy/CoreDNS specs against stale certs.
func TestStack_ensureConfigs_InfraCertsReadyLifecycle(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)
	require.NoError(t, cfg.SettingsStore().Set("monitoring.otel_infra_port", 4319))

	prov := &fakeOtelProvisioner{}
	store, err := NewRulesStore(cfg)
	require.NoError(t, err)
	s := NewStack(nil, cfg, logger.Nop(), store, prov)

	_, err = s.ensureConfigs()
	require.NoError(t, err)
	assert.True(t, s.infraCertsReady, "first reload with healthy provisioner must set infraCertsReady")

	// Latch the flag, then simulate a transient mint failure. Expect
	// the flag to be reset to false BEFORE the call and stay false
	// because EnsureClient errors.
	prov.failNext = true
	_, err = s.ensureConfigs()
	require.NoError(t, err, "mint failure must not cascade — ensureConfigs returns nil and degrades")
	assert.False(t, s.infraCertsReady, "mint failure must leave infraCertsReady=false so containers drop mTLS bind+env")

	// Recover on the next reload. Flag must flip back to true.
	prov.failNext = false
	_, err = s.ensureConfigs()
	require.NoError(t, err)
	assert.True(t, s.infraCertsReady, "recovery reload after a mint failure must re-flip infraCertsReady to true")
}

// TestStack_alsConfig_GatesOnCertsReady wires Stack state through to the
// rendered ALSConfig. infraCertsReady=false must short-circuit before
// returning MTLS=true — otherwise GenerateEnvoyConfig would wire the
// otel_collector_als cluster against /etc/envoy/otel-tls/client.pem when
// no bind-mount exists, producing a running Envoy that fails YAML load /
// TLS handshake at restart. infraCertsReady=true must surface the
// settings-store port through ALSConfig.
//
// Port-range validation is enforced upstream by config.Port's UnmarshalYAML
// hook + WithDefaultsFromStruct backfill, so OtelInfraPort can never be
// out-of-range at runtime — no defensive port gate is needed here.
// `TestGenerateEnvoyConfig_OtelALSCluster_MTLS` covers rendering with a
// hardcoded ALSConfig.
func TestStack_alsConfig_GatesOnCertsReady(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)
	require.NoError(t, cfg.SettingsStore().Set("monitoring.otel_infra_port", 4319))

	s := NewStack(nil, cfg, logger.Nop(), nil, nil)
	assert.Equal(t, ALSConfig{}, s.alsConfig(), "infraCertsReady=false must short-circuit before returning MTLS=true")

	s.infraCertsReady = true
	assert.Equal(
		t,
		ALSConfig{Port: 4319, MTLS: true},
		s.alsConfig(),
		"ready must yield MTLS=true with the settings-store port",
	)
}

// TestStack_ensureInfraClientCerts_NilProvisioner_NoOp pins the
// degraded-mode invariant: when no provisioner is wired (intermediate
// load failed at startup, or monitoring stack not in scope), Stack comes
// up cleanly and never dispatches. Sibling Envoy/CoreDNS specs omit the
// mTLS mounts in this state.
func TestStack_ensureInfraClientCerts_NilProvisioner_NoOp(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)
	s := NewStack(nil, cfg, logger.Nop(), nil, nil)

	require.NoError(t, s.ensureInfraClientCerts())
}
