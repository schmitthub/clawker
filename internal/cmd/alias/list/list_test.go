package list

import (
	"encoding/json"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAliasRows(t *testing.T) {
	defaults := map[string]string{"dev": "run --rm"}
	aliases := map[string]string{
		"dev":  "run --rm", // untouched default
		"v":    "version",  // user-defined
		"team": "ps",       // user-defined
	}
	rows := buildAliasRows(aliases, defaults)

	require.Len(t, rows, 3)
	assert.Equal(t, "dev", rows[0].Name)
	assert.Equal(t, sourceDefault, rows[0].Source)
	assert.Equal(t, "team", rows[1].Name)
	assert.Equal(t, sourceUser, rows[1].Source)

	t.Run("overridden default reports user", func(t *testing.T) {
		rows := buildAliasRows(map[string]string{"dev": "version"}, defaults)
		assert.Equal(t, sourceUser, rows[0].Source)
	})

	t.Run("disabled default still reports default", func(t *testing.T) {
		rows := buildAliasRows(map[string]string{"dev": ""}, defaults)
		assert.Equal(t, sourceDefault, rows[0].Source)
	})
}

func TestListRun_JSON(t *testing.T) {
	tio, _, out, _ := iostreams.Test()
	cfg := configmocks.NewFromString("", "aliases:\n  v: version\n")
	f := &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
		Config:    func() (config.Config, error) { return cfg, nil },
	}
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"--json"})
	require.NoError(t, cmd.Execute())

	var rows []aliasRow
	require.NoError(t, json.Unmarshal(out.Bytes(), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, aliasRow{Name: "v", Expansion: "version", Source: sourceUser}, rows[0])
}

func TestListRun_Empty(t *testing.T) {
	tio, _, out, errOut := iostreams.Test()
	cfg := configmocks.NewFromString("", "")
	f := &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
		Config:    func() (config.Config, error) { return cfg, nil },
	}
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	assert.Empty(t, out.String())
	assert.Contains(t, errOut.String(), "No aliases configured")
}
