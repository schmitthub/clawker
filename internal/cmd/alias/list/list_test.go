package list

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newListEnv builds a config whose project store layers a real file (the
// user config-dir clawker.yaml) over the shipped defaults, so SOURCE rows
// carry actual provenance.
func newListEnv(t *testing.T, userAliasesYAML string) (config.Config, string) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("CLAWKER_CONFIG_DIR", configDir)
	path := filepath.Join(configDir, "clawker.yaml")
	if userAliasesYAML != "" {
		require.NoError(t, os.WriteFile(path, []byte(userAliasesYAML), 0o644))
	}

	store, err := storage.New[config.Project](storage.GenerateDefaultsYAML[config.Project](),
		storage.WithFilenames("clawker.yaml"),
		storage.WithConfigDir(),
	)
	require.NoError(t, err)

	mock := configmocks.NewBlankConfig()
	mock.ProjectStoreFunc = func() *storage.Store[config.Project] { return store }
	mock.ProjectFunc = func() *config.Project { return store.Read() }
	return mock, path
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
	require.Len(t, rows, 3)
	assert.Equal(t, aliasRow{Name: "go", Expansion: "run --rm -it --agent $1 @ --dangerously-skip-permissions", Source: sourceDefault}, rows[0])
	assert.Equal(t, aliasRow{Name: "v", Expansion: "version", Source: path}, rows[1])
	assert.Equal(t, aliasRow{Name: "wt", Expansion: "run --rm -it --agent $1 --worktree $2 @ --dangerously-skip-permissions", Source: sourceDefault}, rows[2])
}

func TestListRun_OverriddenDefaultReportsFile(t *testing.T) {
	cfg, path := newListEnv(t, "aliases:\n  go: version\n")
	stdout, _, err := executeList(t, cfg, "--json")
	require.NoError(t, err)

	var rows []aliasRow
	require.NoError(t, json.Unmarshal([]byte(stdout), &rows))
	require.Len(t, rows, 2)
	assert.Equal(t, aliasRow{Name: "go", Expansion: "version", Source: path}, rows[0])
}

func TestListRun_DisabledDefaultReportsDisablingFile(t *testing.T) {
	cfg, path := newListEnv(t, "aliases:\n  go: \"\"\n")
	stdout, _, err := executeList(t, cfg, "--json")
	require.NoError(t, err)

	var rows []aliasRow
	require.NoError(t, json.Unmarshal([]byte(stdout), &rows))
	require.Len(t, rows, 2)
	assert.Equal(t, aliasRow{Name: "go", Expansion: "", Source: path}, rows[0],
		"disabled default stays listed; SOURCE is the file holding the disabling entry")
}
