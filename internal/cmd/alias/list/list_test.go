package list

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// newListEnv builds a real config whose project store layers a real file (the
// user config-dir clawker.yaml) over the shipped defaults, so SOURCE rows
// carry actual provenance. Walk-up is disabled (no project root), so the
// config-dir file and the defaults are the whole layer stack.
func newListEnv(t *testing.T, userAliasesYAML string) (config.Config, string) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("CLAWKER_CONFIG_DIR", configDir)
	path := filepath.Join(configDir, "clawker.yaml")
	if userAliasesYAML != "" {
		require.NoError(t, os.WriteFile(path, []byte(userAliasesYAML), 0o644))
	}

	cfg, err := config.NewConfig()
	require.NoError(t, err)
	return cfg, path
}

func executeList(t *testing.T, cfg config.Config, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	tio, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
		Config:    func() (config.Config, error) { return cfg, nil },
	}
	cmd := NewCmdList(f, nil)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestListRun_JSON(t *testing.T) {
	cfg, path := newListEnv(t, "aliases:\n  v: version\n")
	stdout, _, err := executeList(t, cfg, "--json")
	require.NoError(t, err)

	var rows []aliasRow
	require.NoError(t, json.Unmarshal([]byte(stdout), &rows))
	require.Len(t, rows, 5)
	assert.Equal(
		t,
		aliasRow{
			Name:      "claude",
			Expansion: "run --rm -it --agent $1 @:claude --dangerously-skip-permissions",
			Source:    sourceDefault,
		},
		rows[0],
	)
	assert.Equal(
		t,
		aliasRow{Name: "codex", Expansion: "run --rm -it --agent $1 @:codex --yolo", Source: sourceDefault},
		rows[1],
	)
	assert.Equal(t, aliasRow{Name: "go", Expansion: "run --rm -it --agent $1 @", Source: sourceDefault}, rows[2])
	assert.Equal(t, aliasRow{Name: "v", Expansion: "version", Source: path}, rows[3])
	assert.Equal(
		t,
		aliasRow{Name: "wt", Expansion: "run --rm -it --agent $1 --worktree $2 @", Source: sourceDefault},
		rows[4],
	)
}

func TestListRun_OverriddenDefaultReportsFile(t *testing.T) {
	cfg, path := newListEnv(t, "aliases:\n  go: version\n")
	stdout, _, err := executeList(t, cfg, "--json")
	require.NoError(t, err)

	var rows []aliasRow
	require.NoError(t, json.Unmarshal([]byte(stdout), &rows))
	require.Len(t, rows, 4)
	assert.Equal(t, aliasRow{Name: "go", Expansion: "version", Source: path}, rows[2])
}

func TestListRun_DisabledDefaultReportsDisablingFile(t *testing.T) {
	cfg, path := newListEnv(t, "aliases:\n  go: \"\"\n")
	stdout, _, err := executeList(t, cfg, "--json")
	require.NoError(t, err)

	var rows []aliasRow
	require.NoError(t, json.Unmarshal([]byte(stdout), &rows))
	require.Len(t, rows, 4)
	assert.Equal(t, aliasRow{Name: "go", Expansion: "", Source: path}, rows[2],
		"disabled default stays listed; SOURCE is the file holding the disabling entry")
}
