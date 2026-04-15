package cpboot

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// Tests INV-B1-017 [unit]: CP container publishes all four required ports.
func TestINV_B1_017_AllRequiredPortsPublished(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()
	cp := cfg.Settings().ControlPlane

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)
	require.NotNil(t, cpConfig)

	requiredPorts := []struct {
		name string
		port int
	}{
		{"gRPC admin API", cp.AdminPort},
		{"Hydra public (token endpoint)", cp.HydraPublicPort},
		{"Oathkeeper HTTP proxy", cp.OathkeeperPort},
		{"healthz", cp.HealthPort},
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
				"%s port (%d) must be published", rp.name, rp.port)
		})
	}
}

// Tests INV-B1-017 [unit]: CP container is NOT in container_map.
func TestINV_B1_017_CPNotInContainerMap(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()

	cpConfig, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	purposeLabel := cpConfig.Labels[consts.LabelPurpose]
	assert.Equal(t, consts.PurposeControlPlane, purposeLabel)
	assert.NotEqual(t, consts.PurposeAgent, purposeLabel)
}
