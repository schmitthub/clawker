package bundler_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// looseHarnessEnv isolates the XDG dirs, anchors a temp project root, and
// returns a config over it plus the root — loose harness fixtures go under
// root/.clawker/harnesses/ via writeLooseHarness.
func looseHarnessEnv(t *testing.T) (*configmocks.ConfigMock, string) {
	t.Helper()
	env := testenv.New(t)
	root := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(root, 0o755))
	cfg := configmocks.NewFromString("", "")
	cfg.ProjectRootFunc = func() string { return root }
	return cfg, root
}

// Harness selection is explicit; an empty selector resolves to the
// build.harness selection key when set, else the built-in default (claude).
func TestResolveHarnessName(t *testing.T) {
	blank := configmocks.NewFromString("", "")

	t.Run("explicit wins", func(t *testing.T) {
		name, err := bundler.ResolveHarnessName(blank, "codex")
		require.NoError(t, err)
		assert.Equal(t, "codex", name)
	})

	t.Run("qualified selector wins", func(t *testing.T) {
		name, err := bundler.ResolveHarnessName(blank, "acme.tools.codex")
		require.NoError(t, err)
		assert.Equal(t, "acme.tools.codex", name)
	})

	t.Run("no selection falls back to the built-in default", func(t *testing.T) {
		name, err := bundler.ResolveHarnessName(blank, "")
		require.NoError(t, err)
		assert.Equal(t, bundler.DefaultHarnessName, name)
	})

	t.Run("explicit reserved alias is rejected", func(t *testing.T) {
		_, err := bundler.ResolveHarnessName(blank, consts.ImageTagBase)
		require.ErrorContains(t, err, "reserved")
	})

	t.Run("no selection resolves the configured default", func(t *testing.T) {
		cfg := configmocks.NewFromString("build:\n  harness: opencode\n", "")
		name, err := bundler.ResolveHarnessName(cfg, "")
		require.NoError(t, err)
		assert.Equal(t, "opencode", name)
	})

	t.Run("qualified configured default resolves", func(t *testing.T) {
		cfg := configmocks.NewFromString("build:\n  harness: acme.tools.codex\n", "")
		name, err := bundler.ResolveHarnessName(cfg, "")
		require.NoError(t, err)
		assert.Equal(t, "acme.tools.codex", name)
	})

	t.Run("explicit selection beats the configured default", func(t *testing.T) {
		cfg := configmocks.NewFromString("build:\n  harness: opencode\n", "")
		name, err := bundler.ResolveHarnessName(cfg, "codex")
		require.NoError(t, err)
		assert.Equal(t, "codex", name)
	})

	t.Run("nil project falls back to the built-in default", func(t *testing.T) {
		cfg := configmocks.NewFromString("", "")
		cfg.ProjectFunc = func() *config.Project { return nil }
		name, err := bundler.ResolveHarnessName(cfg, "")
		require.NoError(t, err)
		assert.Equal(t, bundler.DefaultHarnessName, name)
	})

	t.Run("invalid configured default errors naming build.harness", func(t *testing.T) {
		// The config front door rejects a bad name at load, so an invalid
		// value can only arrive through an unvalidated in-memory mutation
		// (store Set has no validation hook) — inject it the same way.
		cfg := configmocks.NewFromString("", "")
		proj := *cfg.Project()
		proj.Build.Harness = "Bad_Name"
		cfg.ProjectFunc = func() *config.Project { return &proj }
		_, err := bundler.ResolveHarnessName(cfg, "")
		require.ErrorContains(t, err, "build.harness")
	})
}

func TestValidateHarnessSelector(t *testing.T) {
	t.Run("reserved bare aliases rejected", func(t *testing.T) {
		for _, reserved := range []string{
			consts.ImageTagDefaultAlias,
			consts.ImageTagLatest,
			consts.ImageTagBase,
		} {
			require.ErrorContains(t, bundler.ValidateHarnessSelector(reserved), "reserved")
		}
	})

	t.Run("bare name accepted", func(t *testing.T) {
		require.NoError(t, bundler.ValidateHarnessSelector("codex"))
	})

	t.Run("qualified address accepted — reserved-alias rule is bare-only", func(t *testing.T) {
		require.NoError(t, bundler.ValidateHarnessSelector("acme.tools.codex"))
		// A dotted address can never collide with a bare tag alias.
		require.NoError(t, bundler.ValidateHarnessSelector("acme.tools."+consts.ImageTagLatest))
	})

	t.Run("malformed address rejected", func(t *testing.T) {
		require.Error(t, bundler.ValidateHarnessSelector("a.b"))
	})
}

func TestKnownHarnessNames(t *testing.T) {
	cfg, root := looseHarnessEnv(t)
	writeLooseHarness(t, root, "mycustom", "version:\n  resolver: none\n")

	names := bundler.KnownHarnessNames(cfg)
	// Floor harnesses are always known...
	for _, shipped := range bundler.ShippedHarnessNames() {
		assert.Contains(t, names, shipped)
	}
	// ...plus the loose project one.
	assert.Contains(t, names, "mycustom")
	assert.True(t, bundler.IsKnownHarness(cfg, "mycustom"))
	assert.False(t, bundler.IsKnownHarness(cfg, "nope"))
}

// A bare harness with no loose override loads straight from the embedded floor.
func TestLoadHarness_Floor(t *testing.T) {
	cfg, _ := looseHarnessEnv(t)
	b, err := bundler.LoadHarness(cfg, bundler.DefaultHarnessName)
	require.NoError(t, err)
	assert.Equal(t, bundler.DefaultHarnessName, b.Name)
}

// A loose project harness resolves by its bare name and its Name is that
// selection spelling.
func TestLoadHarness_LooseProject(t *testing.T) {
	cfg, root := looseHarnessEnv(t)
	writeLooseHarness(t, root, "mytool", "version:\n  resolver: none\n")

	b, err := bundler.LoadHarness(cfg, "mytool")
	require.NoError(t, err)
	assert.Equal(t, "mytool", b.Name)
}

// A harness convention dir with no harness.yaml resolves (the dir exists) but
// fails to load — a loud, named error, never a silent skip.
func TestLoadHarness_LooseDirWithoutManifest(t *testing.T) {
	cfg, root := looseHarnessEnv(t)
	dir := filepath.Join(root, consts.DotClawkerDir, bundle.ComponentHarness.Dir(), "broken")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	_, err := bundler.LoadHarness(cfg, "broken")
	require.Error(t, err)
	assert.Contains(t, err.Error(), bundler.HarnessManifestFile)
}

// A name that resolves on no tier is a hard, loud error.
func TestLoadHarness_Unknown(t *testing.T) {
	cfg, _ := looseHarnessEnv(t)
	_, err := bundler.LoadHarness(cfg, "no-such-harness")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
