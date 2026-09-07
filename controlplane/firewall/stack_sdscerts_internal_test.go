package firewall

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// fakeSDSProvisioner is a deterministic SDSCertProvisioner for tests.
// Production wiring passes *sdscerts.Service; the write + pair-check
// contract is unit-tested in /controlplane/sdscerts. The tests here
// only assert firewall.Stack's dispatch + gating behavior.
type fakeSDSProvisioner struct {
	calls    int
	failNext bool
}

func (f *fakeSDSProvisioner) EnsureEnvoyClient() (string, string, string, error) {
	f.calls++
	if f.failNext {
		return "", "", "", errors.New("synthetic SDS mint failure")
	}
	return "/tmp/sds/envoy/client.pem", "/tmp/sds/envoy/client.key", "/tmp/sds/envoy/ca.pem", nil
}

// TestStack_ensureConfigs_SDSCertsReadyLifecycle pins the sdsCertsReady
// flag lifecycle in ensureConfigs: reset at entry, true only after a
// successful mint, false after a failure, re-flip on recovery. Same
// latch-bug rationale as the infraCertsReady twin.
func TestStack_ensureConfigs_SDSCertsReadyLifecycle(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)

	prov := new(fakeSDSProvisioner)
	store, err := NewRulesStore(cfg)
	require.NoError(t, err)
	s := NewStack(nil, cfg, logger.Nop(), store, nil, prov, nil)

	_, err = s.ensureConfigs()
	require.NoError(t, err)
	assert.True(t, s.sdsCertsReady, "first reload with healthy provisioner must set sdsCertsReady")

	prov.failNext = true
	_, err = s.ensureConfigs()
	require.NoError(t, err, "SDS mint failure must not cascade — ensureConfigs returns nil and degrades")
	assert.False(t, s.sdsCertsReady, "mint failure must leave sdsCertsReady=false so the spec drops the sds-tls mount")

	prov.failNext = false
	_, err = s.ensureConfigs()
	require.NoError(t, err)
	assert.True(t, s.sdsCertsReady, "recovery reload must re-flip sdsCertsReady to true")
}

// TestStack_ensureConfigs_SDSLaneIndependentOfTelemetryLane pins the
// design point of the dedicated lane: the SDS gate must NOT follow the
// telemetry lane's provisioning fate. A missing otel provisioner
// (monitoring cold) with a healthy SDS provisioner still enables
// on-demand minting — the coupling this test forbids is exactly the
// defect the lane split removed.
func TestStack_ensureConfigs_SDSLaneIndependentOfTelemetryLane(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)

	prov := new(fakeSDSProvisioner)
	store, err := NewRulesStore(cfg)
	require.NoError(t, err)
	s := NewStack(nil, cfg, logger.Nop(), store, nil, prov, nil)

	_, err = s.ensureConfigs()
	require.NoError(t, err)
	assert.False(t, s.infraCertsReady, "no otel provisioner — telemetry lane stays cold")
	assert.True(t, s.sdsCertsReady, "SDS lane must come up regardless of the telemetry lane")
}

// TestStack_sdsConfig_GatesOnSDSCertsReady wires Stack state through to
// the rendered SDSConfig. sdsCertsReady=false must yield Enabled=false —
// otherwise GenerateEnvoyConfig would wire the on-demand selector and
// sds_cluster against /etc/envoy/sds-tls files that were never mounted,
// stalling every wildcard handshake instead of falling back to the
// static certs.
func TestStack_sdsConfig_GatesOnSDSCertsReady(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)

	s := NewStack(nil, cfg, logger.Nop(), nil, nil, nil, nil)
	assert.False(t, s.sdsConfig().Enabled, "sdsCertsReady=false must disable the SDS lane")

	s.sdsCertsReady = true
	got := s.sdsConfig()
	assert.True(t, got.Enabled)
	assert.Equal(t, consts.ContainerCP, got.Address)
	assert.Equal(t, cfg.ControlPlaneSettings().SDSPort, got.Port)
}

// TestStack_ensureSDSClientCerts_NilProvisioner_NoOp pins degraded mode:
// no provisioner → clean no-op, no dispatch, spec omits the sds-tls
// mount (asserted via envoyContainerSpec in container_spec_test.go).
func TestStack_ensureSDSClientCerts_NilProvisioner_NoOp(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)
	s := NewStack(nil, cfg, logger.Nop(), nil, nil, nil, nil)

	require.NoError(t, s.ensureSDSClientCerts())
}
