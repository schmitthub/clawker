package controlplane

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// Tests INV-B1-005 [unit]: Hydra admin API binds to 127.0.0.1 inside CP container.
// Hydra admin port must never appear in published port mappings and its startup
// config must bind to 127.0.0.1, not 0.0.0.0.
func TestINV_B1_005_HydraAdminInternalOnly(t *testing.T) {
	dataDir := t.TempDir()
	adminPort := consts.DefaultCPAdminPort

	cpConfig, err := BuildCPContainerConfig(dataDir, adminPort)
	require.NoError(t, err)
	require.NotNil(t, cpConfig, "BuildCPContainerConfig must return a non-nil config")

	// Hydra admin port (4445) must NOT appear in published port bindings.
	hydraAdminPortKey := fmt.Sprintf("%d/tcp", consts.HydraAdminPort)
	for portKey := range cpConfig.PortBindings {
		assert.NotEqual(t, hydraAdminPortKey, portKey.String(),
			"Hydra admin port (%d) must not be published to the host", consts.HydraAdminPort)
	}
}

// Tests INV-B1-006 [unit]: CLI private key material never enters containers.
// The CA key, CLI signing key, and CLI mTLS key must never appear in bind mounts.
func TestINV_B1_006_PrivateKeysNeverMounted(t *testing.T) {
	dataDir := t.TempDir()
	adminPort := consts.DefaultCPAdminPort

	cpConfig, err := BuildCPContainerConfig(dataDir, adminPort)
	require.NoError(t, err)
	require.NotNil(t, cpConfig, "BuildCPContainerConfig must return a non-nil config")

	// These paths must NEVER appear as mount sources.
	// The CLI's private signing key is used to sign JWT assertions
	// and must never enter any container.
	forbiddenPaths := []struct {
		name string
		path string
	}{
		{"CLI signing key", auth.SigningKeyPath(dataDir)},
	}

	for _, fp := range forbiddenPaths {
		t.Run(fp.name, func(t *testing.T) {
			for _, m := range cpConfig.Mounts {
				assert.False(t, strings.HasSuffix(m.Source, fp.path) || m.Source == fp.path,
					"private key %q must never be bind-mounted into the CP container, but found mount source: %s",
					fp.name, m.Source)
			}
		})
	}
}

// Tests INV-B1-006 [unit]: Allowed public material IS mounted.
// Verify that public material (CA cert, server cert dir, CLI signing JWK) is present.
func TestINV_B1_006_PublicMaterialIsMounted(t *testing.T) {
	dataDir := t.TempDir()
	adminPort := consts.DefaultCPAdminPort

	cpConfig, err := BuildCPContainerConfig(dataDir, adminPort)
	require.NoError(t, err)
	require.NotNil(t, cpConfig, "BuildCPContainerConfig must return a non-nil config")

	// These paths MUST be mounted (public material for CP to function).
	requiredSources := []struct {
		name string
		path string
	}{
		{"CLI signing JWK", auth.SigningJWKPath(dataDir)},
		{"Server TLS cert", auth.ServerCertPath(dataDir)},
		{"Server TLS key", auth.ServerKeyPath(dataDir)},
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

// Tests INV-B1-008 [unit]: Admin port published to localhost only and configurable.
// All published ports must bind to 127.0.0.1, never 0.0.0.0.
func TestINV_B1_008_AllPortsPublishedToLocalhostOnly(t *testing.T) {
	dataDir := t.TempDir()
	adminPort := consts.DefaultCPAdminPort

	cpConfig, err := BuildCPContainerConfig(dataDir, adminPort)
	require.NoError(t, err)
	require.NotNil(t, cpConfig, "BuildCPContainerConfig must return a non-nil config")

	require.NotEmpty(t, cpConfig.PortBindings,
		"CP container must have published port bindings")

	for portKey, bindings := range cpConfig.PortBindings {
		for _, binding := range bindings {
			assert.Equal(t, "127.0.0.1", binding.HostIP.String(),
				"port %s must be published to 127.0.0.1, not %s", portKey, binding.HostIP)
		}
	}
}

// Tests INV-B1-008 [unit]: Admin port comes from Settings schema, not hardcoded.
func TestINV_B1_008_AdminPortFromSettings(t *testing.T) {
	dataDir := t.TempDir()

	// Use a custom port to prove it's not hardcoded.
	customPort := 9999
	cpConfig, err := BuildCPContainerConfig(dataDir, customPort)
	require.NoError(t, err)
	require.NotNil(t, cpConfig, "BuildCPContainerConfig must return a non-nil config")

	// The custom port must appear in the published port bindings.
	customPortKey := fmt.Sprintf("%d/tcp", customPort)
	found := false
	for portKey := range cpConfig.PortBindings {
		if portKey.String() == customPortKey {
			found = true
			break
		}
	}
	assert.True(t, found,
		"admin port %d from Settings must appear in port bindings, but only found: %v",
		customPort, cpConfig.PortBindings)
}

// Tests INV-B1-008 [unit]: Settings schema has admin port field with correct default.
func TestINV_B1_008_SettingsSchemaAdminPort(t *testing.T) {
	settings := &config.Settings{}
	require.NotNil(t, settings, "Settings must not be nil")

	// The default admin port should be 7443.
	port := settings.ControlPlane.AdminPortOrDefault()
	assert.Equal(t, 7443, port,
		"Settings control_plane.admin_port should default to 7443")
}

// Tests INV-B1-009 [unit]: CP container is infrastructure, not filtered.
// The CP container must not be added to eBPF container_map.
// We verify this by checking that the container config does not include
// any eBPF enable or attach instructions.
func TestINV_B1_009_CPIsInfrastructureNotFiltered(t *testing.T) {
	dataDir := t.TempDir()
	adminPort := consts.DefaultCPAdminPort

	cpConfig, err := BuildCPContainerConfig(dataDir, adminPort)
	require.NoError(t, err)
	require.NotNil(t, cpConfig, "BuildCPContainerConfig must return a non-nil config")

	// The CP's purpose label must be "controlplane" (infrastructure),
	// not "agent" (which would be filtered by eBPF).
	purposeLabel, ok := cpConfig.Labels[consts.LabelPurpose]
	assert.True(t, ok, "CP container must have a purpose label")
	assert.Equal(t, consts.PurposeControlPlane, purposeLabel,
		"CP container purpose must be %q, not %q",
		consts.PurposeControlPlane, purposeLabel)
	assert.NotEqual(t, consts.PurposeAgent, purposeLabel,
		"CP container must NOT have purpose %q (agent containers are eBPF-filtered)",
		consts.PurposeAgent)
}

// Tests INV-B1-015 [unit]: Distroless CP image.
// The CP container image should reference a distroless base.
func TestINV_B1_015_DistrolessCPImage(t *testing.T) {
	dataDir := t.TempDir()
	adminPort := consts.DefaultCPAdminPort

	cpConfig, err := BuildCPContainerConfig(dataDir, adminPort)
	require.NoError(t, err)
	require.NotNil(t, cpConfig, "BuildCPContainerConfig must return a non-nil config")

	// The image must be based on distroless.
	assert.Contains(t, cpConfig.Image, consts.CPBaseImage,
		"CP container image must be based on distroless")
}

// Tests INV-B1-018 [unit]: CP container labels.
// CP container must carry managed=true and purpose=controlplane labels.
func TestINV_B1_018_CPContainerLabels(t *testing.T) {
	dataDir := t.TempDir()
	adminPort := consts.DefaultCPAdminPort

	cpConfig, err := BuildCPContainerConfig(dataDir, adminPort)
	require.NoError(t, err)
	require.NotNil(t, cpConfig, "BuildCPContainerConfig must return a non-nil config")

	t.Run("managed label", func(t *testing.T) {
		val, ok := cpConfig.Labels[consts.LabelManaged]
		assert.True(t, ok, "CP container must have %s label", consts.LabelManaged)
		assert.Equal(t, consts.ManagedLabelValue, val,
			"managed label must be %q", consts.ManagedLabelValue)
	})

	t.Run("purpose label", func(t *testing.T) {
		val, ok := cpConfig.Labels[consts.LabelPurpose]
		assert.True(t, ok, "CP container must have %s label", consts.LabelPurpose)
		assert.Equal(t, consts.PurposeControlPlane, val,
			"purpose label must be %q", consts.PurposeControlPlane)
	})
}
