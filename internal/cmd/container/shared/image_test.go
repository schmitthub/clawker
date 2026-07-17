package shared

import (
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/stretchr/testify/require"
)

func TestValidatePlaceholderHarness_RejectsReservedBase(t *testing.T) {
	cfg := configmocks.NewFromString("", `
harnesses:
  claude: { default: true, path: /bundles/claude }
`)
	err := validatePlaceholderHarness(cfg, consts.ImageTagBase)
	require.ErrorContains(t, err, "reserved")
}
