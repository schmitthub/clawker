package controlplane

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
)

// ---------------------------------------------------------------------------
// INV-B1-017: No eBPF regression on published ports
// ---------------------------------------------------------------------------

// Tests INV-B1-017 [unit]: CP container publishes all four required ports.
// The CLI must be able to reach the gRPC admin port (7443), Hydra public
// port (4444), Oathkeeper HTTP port (4456), and healthz port (8080).
// This is the structural prerequisite — the integration test verifies
// actual connectivity with eBPF active.
func TestINV_B1_017_AllRequiredPortsPublished(t *testing.T) {
	dataDir := t.TempDir()
	adminPort := consts.DefaultCPAdminPort

	cpConfig, err := BuildCPContainerConfig(dataDir, adminPort)
	require.NoError(t, err)
	require.NotNil(t, cpConfig, "BuildCPContainerConfig must return a non-nil config")

	requiredPorts := []struct {
		name string
		port int
	}{
		{"gRPC admin API", consts.DefaultCPAdminPort},
		{"Hydra public (token endpoint)", consts.HydraPublicPort},
		{"Oathkeeper HTTP proxy", consts.OathkeeperHTTPPort},
		{"healthz", consts.CPHealthPort},
	}

	for _, rp := range requiredPorts {
		t.Run(rp.name, func(t *testing.T) {
			portKey := fmt.Sprintf("%d/tcp", rp.port)
			found := false
			for pk := range cpConfig.PortBindings {
				if pk.String() == portKey {
					found = true
					break
				}
			}
			assert.True(t, found,
				"%s port (%d) must be published in CP container config",
				rp.name, rp.port)
		})
	}
}

// Tests INV-B1-017 [unit]: CP container is NOT in container_map.
// This is a regression guard — if the CP were in container_map, eBPF
// would filter its outbound traffic and potentially block published ports.
// Cross-references INV-B1-009.
func TestINV_B1_017_CPNotInContainerMap(t *testing.T) {
	dataDir := t.TempDir()
	adminPort := consts.DefaultCPAdminPort

	cpConfig, err := BuildCPContainerConfig(dataDir, adminPort)
	require.NoError(t, err)
	require.NotNil(t, cpConfig, "BuildCPContainerConfig must return a non-nil config")

	// The purpose label must be "controlplane", which means the container
	// is infrastructure and NOT added to eBPF container_map (same as Envoy/CoreDNS).
	purposeLabel := cpConfig.Labels[consts.LabelPurpose]
	assert.Equal(t, consts.PurposeControlPlane, purposeLabel,
		"CP must be infrastructure (purpose=%q), not a filtered agent",
		consts.PurposeControlPlane)
	assert.NotEqual(t, consts.PurposeAgent, purposeLabel,
		"CP must NOT be an agent — agents are filtered by eBPF")
}
