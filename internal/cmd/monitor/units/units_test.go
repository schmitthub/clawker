package units_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmd/monitor/units"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// makeUnitDir writes a minimal valid monitoring unit named unitName and
// returns its absolute path.
func makeUnitDir(t *testing.T, unitName string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), unitName)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "index-templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "monitoring.yaml"), fmt.Appendf(nil,
		"description: test unit\nlogs:\n  - index: %s\n    service_names: [%s]\n", unitName, unitName), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "index-templates", unitName+".json"),
		fmt.Appendf(nil, `{"index_patterns": [%q]}`, unitName), 0o644))
	return dir
}

// testEnv bundles the isolated file-backed config and output buffers a
// command test drives.
type testEnv struct {
	f      *cmdutil.Factory
	cfg    config.Config
	out    *bytes.Buffer
	errOut *bytes.Buffer
}

// testFactory builds a Factory around an isolated file-backed config
// (mutations supported) and returns the output buffers.
func testFactory(t *testing.T) testEnv {
	t.Helper()
	cfg := configmocks.NewIsolatedTestConfig(t)
	ios, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{} //nolint:exhaustruct // command under test reads only the fields below
	f.IOStreams = ios
	f.TUI = tui.NewTUI(ios)
	f.Config = func() (config.Config, error) { return cfg, nil }
	return testEnv{f: f, cfg: cfg, out: out, errOut: errOut}
}

func runCmd(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	cmd.SetArgs(args)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	return cmd.Execute()
}

func TestRegister(t *testing.T) {
	t.Run("registers inactive with absolute stored path", func(t *testing.T) {
		env := testFactory(t)
		f, cfg, out := env.f, env.cfg, env.out
		dir := makeUnitDir(t, "codex-usage")

		err := runCmd(t, units.NewCmdRegister(f, nil), dir)
		require.NoError(t, err)

		entry := cfg.Settings().Monitoring.Units["codex-usage"]
		assert.Equal(t, dir, entry.Path, "host-global registry stores absolute paths")
		assert.Nil(t, entry.Active, "registration never activates")
		assert.Contains(t, out.String(), "Registered monitoring unit 'codex-usage'")
		assert.Contains(t, out.String(), "Written to ")
		assert.Contains(t, out.String(), "enable with 'clawker monitor enable codex-usage'")
	})

	t.Run("invalid unit dir is rejected before any write", func(t *testing.T) {
		env := testFactory(t)
		f, cfg := env.f, env.cfg
		dir := t.TempDir() // no monitoring.yaml

		err := runCmd(t, units.NewCmdRegister(f, nil), dir)
		require.ErrorContains(t, err, "invalid monitoring unit directory")
		assert.Empty(t, cfg.Settings().Monitoring.Units, "failed validation must not write")
	})

	t.Run("built-in name is refused even with --force", func(t *testing.T) {
		env := testFactory(t)
		f, cfg := env.f, env.cfg
		dir := makeUnitDir(t, "claude-code")

		err := runCmd(t, units.NewCmdRegister(f, nil), dir, "--force")
		require.ErrorContains(t, err, "built-in monitoring unit")
		require.ErrorContains(t, err, "--name")
		assert.Empty(t, cfg.Settings().Monitoring.Units)
	})

	t.Run("duplicate needs --force and reports the old path", func(t *testing.T) {
		env := testFactory(t)
		f, out := env.f, env.out
		dir := makeUnitDir(t, "codex-usage")
		require.NoError(t, runCmd(t, units.NewCmdRegister(f, nil), dir))

		dir2 := makeUnitDir(t, "codex-usage")
		err := runCmd(t, units.NewCmdRegister(f, nil), dir2)
		require.ErrorContains(t, err, "already registered")

		out.Reset()
		require.NoError(t, runCmd(t, units.NewCmdRegister(f, nil), dir2, "--force"))
		assert.Contains(t, out.String(), "(was "+dir+")")
	})

	t.Run("--name overrides the dir-derived name", func(t *testing.T) {
		env := testFactory(t)
		f, cfg := env.f, env.cfg
		// Dir basename ("my-fork") differs from the registered name so a
		// command that ignored --name would register the wrong key. The
		// unit's index must match the REGISTERED name, so the manifest
		// declares index codex.
		dir := filepath.Join(t.TempDir(), "my-fork")
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "index-templates"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "monitoring.yaml"),
			[]byte("logs:\n  - index: codex\n    service_names: [codex]\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "index-templates", "codex.json"),
			[]byte(`{"index_patterns": ["codex"]}`), 0o644))

		require.NoError(t, runCmd(t, units.NewCmdRegister(f, nil), dir, "--name", "codex"))
		assert.Contains(t, cfg.Settings().Monitoring.Units, "codex")
		assert.NotContains(t, cfg.Settings().Monitoring.Units, "my-fork")
	})
}

func TestRemove(t *testing.T) {
	t.Run("removes a registration", func(t *testing.T) {
		env := testFactory(t)
		f, cfg, out := env.f, env.cfg, env.out
		dir := makeUnitDir(t, "codex-usage")
		require.NoError(t, runCmd(t, units.NewCmdRegister(f, nil), dir))

		require.NoError(t, runCmd(t, units.NewCmdRemove(f, nil), "codex-usage"))
		assert.NotContains(t, cfg.Settings().Monitoring.Units, "codex-usage")
		assert.Contains(t, out.String(), "Removed monitoring unit registration 'codex-usage'")
	})

	t.Run("built-in cannot be removed", func(t *testing.T) {
		env := testFactory(t)
		f := env.f
		err := runCmd(t, units.NewCmdRemove(f, nil), "claude-code")
		require.ErrorContains(t, err, "built-in")
		require.ErrorContains(t, err, "clawker monitor disable")
	})

	t.Run("unknown name", func(t *testing.T) {
		env := testFactory(t)
		f := env.f
		err := runCmd(t, units.NewCmdRemove(f, nil), "nope")
		require.ErrorContains(t, err, "not registered")
	})

	t.Run("removing an active unit prints the persistence hint", func(t *testing.T) {
		env := testFactory(t)
		f, errOut := env.f, env.errOut
		dir := makeUnitDir(t, "codex-usage")
		require.NoError(t, runCmd(t, units.NewCmdRegister(f, nil), dir))
		require.NoError(t, runCmd(t, units.NewCmdEnable(f, nil), "codex-usage"))

		require.NoError(t, runCmd(t, units.NewCmdRemove(f, nil), "codex-usage"))
		assert.Contains(t, errOut.String(), "down --volumes")
	})
}

func TestEnableDisable(t *testing.T) {
	t.Run("enable built-in writes the flag and recipe", func(t *testing.T) {
		env := testFactory(t)
		f, cfg, out := env.f, env.cfg, env.out

		require.NoError(t, runCmd(t, units.NewCmdEnable(f, nil), "claude-code"))
		entry := cfg.Settings().Monitoring.Units["claude-code"]
		require.NotNil(t, entry.Active)
		assert.True(t, *entry.Active)
		assert.Empty(t, entry.Path, "flag-only entry for a built-in")
		assert.Contains(t, out.String(), "clawker monitor init && clawker monitor up")
	})

	t.Run("enable unknown unit lists known names", func(t *testing.T) {
		env := testFactory(t)
		f := env.f
		err := runCmd(t, units.NewCmdEnable(f, nil), "nope")
		require.ErrorContains(t, err, "not registered or built-in")
		require.ErrorContains(t, err, "claude-code")
	})

	t.Run("enable blocks on index collision with an active unit", func(t *testing.T) {
		env := testFactory(t)
		f := env.f
		// Two units claiming the same index "codex" under different
		// registered names: codex (index codex) and a second unit dir
		// whose manifest also declares index codex via --name trickery is
		// impossible (index must be name-prefixed), so collide on the
		// service name instead: both route service.name=codex... which is
		// also impossible cross-name. The real collision case is a fork
		// registered under a different name with IDENTICAL lanes — build
		// that directly.
		dirA := makeUnitDir(t, "codex")
		require.NoError(t, runCmd(t, units.NewCmdRegister(f, nil), dirA))
		require.NoError(t, runCmd(t, units.NewCmdEnable(f, nil), "codex"))

		// Fork: unit named codex-fork whose lane claims index
		// codex-fork but routes service.name codex-fork... no collision.
		// To collide, its lane must claim "codex-fork" prefixed index —
		// so craft a manifest with index codex-fork and service codex.
		dirB := filepath.Join(t.TempDir(), "codex-fork")
		require.NoError(t, os.MkdirAll(filepath.Join(dirB, "index-templates"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dirB, "monitoring.yaml"),
			[]byte("logs:\n  - index: codex-fork\n    service_names: [codex]\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dirB, "index-templates", "codex-fork.json"),
			[]byte(`{"index_patterns": ["codex-fork"]}`), 0o644))
		require.NoError(t, runCmd(t, units.NewCmdRegister(f, nil), dirB))

		err := runCmd(t, units.NewCmdEnable(f, nil), "codex-fork")
		require.ErrorContains(t, err, `service name "codex"`)
		require.ErrorContains(t, err, "disable one first")
	})

	t.Run("disable writes the flag and prints persistence hint", func(t *testing.T) {
		env := testFactory(t)
		f, cfg, out := env.f, env.cfg, env.out
		require.NoError(t, runCmd(t, units.NewCmdEnable(f, nil), "claude-code"))
		out.Reset()

		require.NoError(t, runCmd(t, units.NewCmdDisable(f, nil), "claude-code"))
		entry := cfg.Settings().Monitoring.Units["claude-code"]
		require.NotNil(t, entry.Active)
		assert.False(t, *entry.Active)
		assert.Contains(t, out.String(), "down --volumes")
	})

	t.Run("disable unknown unit", func(t *testing.T) {
		env := testFactory(t)
		f := env.f
		err := runCmd(t, units.NewCmdDisable(f, nil), "nope")
		require.ErrorContains(t, err, "not registered or built-in")
	})
}

func TestList(t *testing.T) {
	t.Run("quiet lists names incl. built-in", func(t *testing.T) {
		env := testFactory(t)
		f, out := env.f, env.out
		dir := makeUnitDir(t, "codex-usage")
		require.NoError(t, runCmd(t, units.NewCmdRegister(f, nil), dir))
		out.Reset()

		require.NoError(t, runCmd(t, units.NewCmdList(f, nil), "-q"))
		assert.Contains(t, out.String(), "claude-code")
		assert.Contains(t, out.String(), "codex-usage")
	})

	t.Run("json rows carry path, source, active", func(t *testing.T) {
		env := testFactory(t)
		f, out := env.f, env.out
		dir := makeUnitDir(t, "codex-usage")
		require.NoError(t, runCmd(t, units.NewCmdRegister(f, nil), dir))
		require.NoError(t, runCmd(t, units.NewCmdEnable(f, nil), "codex-usage"))
		out.Reset()

		require.NoError(t, runCmd(t, units.NewCmdList(f, nil), "--json"))
		var rows []map[string]string
		require.NoError(t, json.Unmarshal(out.Bytes(), &rows))

		byName := map[string]map[string]string{}
		for _, r := range rows {
			byName[r["name"]] = r
		}
		require.Contains(t, byName, "claude-code")
		assert.Equal(t, "(built-in)", byName["claude-code"]["path"])
		assert.Equal(t, "-", byName["claude-code"]["active"])
		require.Contains(t, byName, "codex-usage")
		assert.Equal(t, dir, byName["codex-usage"]["path"])
		assert.Equal(t, "yes", byName["codex-usage"]["active"])
	})

	t.Run("missing registered path gets a marker", func(t *testing.T) {
		env := testFactory(t)
		f, out := env.f, env.out
		dir := makeUnitDir(t, "codex-usage")
		require.NoError(t, runCmd(t, units.NewCmdRegister(f, nil), dir))
		require.NoError(t, os.RemoveAll(dir))
		out.Reset()

		require.NoError(t, runCmd(t, units.NewCmdList(f, nil), "--json"))
		assert.Contains(t, out.String(), "(missing)")
	})
}
