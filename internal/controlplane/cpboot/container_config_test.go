package cpboot

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// Tests INV-B1-005 [unit]: Hydra admin API binds to 127.0.0.1 inside CP container.
func TestINV_B1_005_HydraAdminInternalOnly(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()
	cp := cfg.Settings().ControlPlane

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)
	require.NotNil(t, cpConfig)

	hydraAdminPortKey := fmt.Sprintf("%d/tcp", cp.HydraAdminPort)
	for portKey := range cpConfig.PortBindings {
		assert.NotEqual(t, hydraAdminPortKey, portKey.String(),
			"Hydra admin port (%d) must not be published to the host", cp.HydraAdminPort)
	}
}

// Tests INV-B1-006 [unit]: CLI private key material never enters containers.
func TestINV_B1_006_PrivateKeysNeverMounted(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	signingKeyPath, err := consts.AuthCLISigningKeyPath()
	require.NoError(t, err)
	clientKeyPath, err := consts.AuthCLIClientKeyPath()
	require.NoError(t, err)

	for _, m := range cpConfig.Mounts {
		assert.False(t, strings.HasSuffix(m.Source, signingKeyPath) || m.Source == signingKeyPath,
			"CLI signing key must never be bind-mounted into the CP container, but found mount source: %s", m.Source)
		assert.False(t, strings.HasSuffix(m.Source, clientKeyPath) || m.Source == clientKeyPath,
			"CLI client key must never be bind-mounted into the CP container, but found mount source: %s", m.Source)
	}
}

// Tests INV-B1-006 [unit]: Allowed public material IS mounted.
func TestINV_B1_006_PublicMaterialIsMounted(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	jwkPath, err := consts.AuthCLISigningJWKPath()
	require.NoError(t, err)
	certPath, err := consts.AuthServerCertPath()
	require.NoError(t, err)
	keyPath, err := consts.AuthServerKeyPath()
	require.NoError(t, err)

	requiredSources := []struct {
		name string
		path string
	}{
		{"CLI signing JWK", jwkPath},
		{"Server TLS cert", certPath},
		{"Server TLS key", keyPath},
	}

	for _, rs := range requiredSources {
		t.Run(rs.name, func(t *testing.T) {
			found := false
			for _, m := range cpConfig.Mounts {
				if m.Source == rs.path {
					found = true
					assert.True(t, m.ReadOnly,
						"public material %q should be mounted read-only", rs.name)
					break
				}
			}
			assert.True(t, found,
				"public material %q must be bind-mounted into the CP container", rs.name)
		})
	}
}

// Tests INV-B1-008 [unit]: All ports published to localhost only.
func TestINV_B1_008_AllPortsPublishedToLocalhostOnly(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)
	require.NotEmpty(t, cpConfig.PortBindings)

	for portKey, bindings := range cpConfig.PortBindings {
		for _, binding := range bindings {
			assert.Equal(t, "127.0.0.1", binding.HostIP.String(),
				"port %s must be published to 127.0.0.1, not %s", portKey, binding.HostIP)
		}
	}
}

// Tests INV-B1-008 [unit]: Admin port comes from Settings, not hardcoded.
func TestINV_B1_008_AdminPortFromSettings(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewFromString("", `
control_plane:
  admin_port: 9999
`)

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	customPortKey := fmt.Sprintf("%d/tcp", 9999)
	found := false
	for portKey := range cpConfig.PortBindings {
		if portKey.String() == customPortKey {
			found = true
			break
		}
	}
	assert.True(t, found, "admin port 9999 must appear in port bindings")
}

// Tests INV-B1-009 [unit]: CP container is infrastructure, not filtered.
func TestINV_B1_009_CPIsInfrastructureNotFiltered(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	purposeLabel, ok := cpConfig.Labels[consts.LabelPurpose]
	assert.True(t, ok, "CP container must have a purpose label")
	assert.Equal(t, consts.PurposeControlPlane, purposeLabel)
}

// Tests INV-B1-015 [unit]: CP image tag matches consts.
func TestINV_B1_015_CPImageTag(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)
	assert.Equal(t, consts.CPImageTag, cpConfig.Image)
}

// Tests INV-B1-018 [unit]: CP container labels.
func TestINV_B1_018_CPContainerLabels(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	t.Run("managed label", func(t *testing.T) {
		val, ok := cpConfig.Labels[consts.LabelManaged]
		assert.True(t, ok)
		assert.Equal(t, consts.ManagedLabelValue, val)
	})

	t.Run("purpose label", func(t *testing.T) {
		val, ok := cpConfig.Labels[consts.LabelPurpose]
		assert.True(t, ok)
		assert.Equal(t, consts.PurposeControlPlane, val)
	})
}

// Tests INV-B1-020 [unit]: Config dir is bind-mounted read-only.
func TestINV_B1_020_ConfigDirMounted(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	found := false
	for _, m := range cpConfig.Mounts {
		if m.Target == consts.CPClawkerConfigDir {
			found = true
			assert.True(t, m.ReadOnly, "config dir must be mounted read-only")
			break
		}
	}
	assert.True(t, found, "config dir must be bind-mounted into the CP container")
}

// Tests that Docker socket is bind-mounted read-only for container state verification.
func TestCPContainerConfig_DockerSocketMounted(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	found := false
	for _, m := range cpConfig.Mounts {
		if m.Target == "/var/run/docker.sock" {
			found = true
			assert.Equal(t, "/var/run/docker.sock", m.Source,
				"Docker socket source must be /var/run/docker.sock")
			assert.True(t, m.ReadOnly,
				"Docker socket must be mounted read-only")
			break
		}
	}
	assert.True(t, found,
		"Docker socket must be bind-mounted into the CP container")
}

// Tests INV-B1-020 [unit]: CLAWKER_CONFIG_DIR env var is set.
func TestINV_B1_020_ConfigDirEnvVar(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	envVar := cfg.ConfigDirEnvVar() + "=" + consts.CPClawkerConfigDir
	assert.Contains(t, cpConfig.Env, envVar,
		"container env must set %s", cfg.ConfigDirEnvVar())
}

// TestCPContainer_OtelClientCertMounts — both halves of the OTEL
// client mTLS pair are bind-mounted RO into the CP at the canonical
// in-container paths so the daemon's OTLP push can present them.
func TestCPContainer_OtelClientCertMounts(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	wants := map[string]bool{
		consts.CPClientCertPath: false,
		consts.CPClientKeyPath:  false,
	}
	for _, m := range cpConfig.Mounts {
		if _, ok := wants[m.Target]; ok {
			wants[m.Target] = true
			assert.True(t, m.ReadOnly, "OTEL client material at %s must be RO", m.Target)
		}
	}
	for target, found := range wants {
		assert.True(t, found, "missing CP OTEL mount: %s", target)
	}
}

// TestCPContainer_ExtraHostsHostGateway — host.docker.internal is
// always remapped via host-gateway. CP relies on it to reach the
// host-loopback OTLP receiver; agent containers cannot use the same
// route because the BPF firewall redirects gateway traffic for
// non-hostproxy ports to Envoy (CP is exempt).
func TestCPContainer_ExtraHostsHostGateway(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)
	require.Contains(t, cpConfig.ExtraHosts, "host.docker.internal:host-gateway")
}

// TestCPContainer_OtelLogsEnv_Emitted — when monitoring.otel_infra_port
// is non-zero (default), the OTLP endpoint env var lands in the
// container config. The transport-specific env vars
// (OTEL_EXPORTER_OTLP_PROTOCOL, OTEL_EXPORTER_OTLP_LOGS_ENDPOINT with
// the /v1/logs HTTP path) are deliberately absent: the CP wires
// otlploggrpc in-process and the collector's otlp/infra receiver only
// opens the grpc: protocol — setting them would be misleading. Client
// cert/key/CA env vars are also absent — the CP-side exporter wires
// its TLSConfig in-process via internal/controlplane/otelcerts.
// Reading CLI-root-direct cert paths from env would silently undo the
// trust-anchor split (agents hold CLI-root-direct leaves and could
// forge service.name=clawker-cp).
func TestCPContainer_OtelLogsEnv_Emitted(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	wantPresent := map[string]bool{
		"OTEL_EXPORTER_OTLP_ENDPOINT": false,
	}
	wantAbsent := []string{
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE",
		"OTEL_EXPORTER_OTLP_CLIENT_KEY",
		"OTEL_EXPORTER_OTLP_CERTIFICATE",
	}
	for _, e := range cpConfig.Env {
		for k := range wantPresent {
			if strings.HasPrefix(e, k+"=") {
				wantPresent[k] = true
			}
		}
		for _, k := range wantAbsent {
			assert.False(t, strings.HasPrefix(e, k+"="),
				"%s must NOT be injected via env — CP wires OTLP/gRPC + TLSConfig in-process", k)
		}
	}
	for k, found := range wantPresent {
		assert.True(t, found, "missing OTEL env var %s in CP container env", k)
	}
}
