package harness_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	harnesscmd "github.com/schmitthub/clawker/internal/cmd/harness"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

// codex is registered (shadows the shipped codex); claude carries init config
// only (no path) and must NOT appear as a project registration.
const listProjectYAML = `harnesses:
  codex: { path: ./tools/codex }
  claude: { post_init: "echo hi" }
`

type listHarnessRow struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	Shadows string `json:"shadows"`
}

func TestHarnessList_JSON(t *testing.T) {
	cfg := configmocks.NewFromString(listProjectYAML, "")
	f, out := newTestFactory(t, cfg)

	cmd := harnesscmd.NewCmdHarnessList(f, nil)
	cmd.SetArgs([]string{"--json"})
	require.NoError(t, cmd.Execute())

	var rows []listHarnessRow
	require.NoError(t, json.Unmarshal(out.Bytes(), &rows))

	byName := map[string]listHarnessRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}

	codexRow, ok := byName["codex"]
	require.True(t, ok)
	assert.Equal(t, "project", codexRow.Source)
	assert.Equal(t, "shipped", codexRow.Shadows)
	assert.Equal(t, "./tools/codex", codexRow.Path)

	// claude has an init-config-only entry (no path) → shown as shipped, not
	// as a project registration.
	claudeRow, ok := byName["claude"]
	require.True(t, ok)
	assert.Equal(t, "shipped", claudeRow.Source)
	assert.Equal(t, "(built-in)", claudeRow.Path)
	assert.Empty(t, claudeRow.Shadows)
}

func TestHarnessList_Quiet(t *testing.T) {
	cfg := configmocks.NewFromString(listProjectYAML, "")
	f, out := newTestFactory(t, cfg)

	cmd := harnesscmd.NewCmdHarnessList(f, nil)
	cmd.SetArgs([]string{"-q"})
	require.NoError(t, cmd.Execute())

	names := strings.Fields(out.String())
	assert.Contains(t, names, "claude")
	assert.Contains(t, names, "codex")
}
