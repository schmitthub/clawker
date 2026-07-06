package bundler_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/harness"
)

func TestResolveHarnessName(t *testing.T) {
	t.Run("explicit wins over registry default", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
harnesses:
  codex: { default: true }
`)
		name, err := bundler.ResolveHarnessName(cfg, "claude")
		require.NoError(t, err)
		assert.Equal(t, "claude", name)
	})

	t.Run("registry default flag wins over builtin", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
harnesses:
  codex: { default: true }
`)
		name, err := bundler.ResolveHarnessName(cfg, "")
		require.NoError(t, err)
		assert.Equal(t, "codex", name)
	})

	t.Run("multiple defaults is a configuration error", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
harnesses:
  zeta: { default: true }
  alpha: { default: true }
`)
		_, err := bundler.ResolveHarnessName(cfg, "")
		require.ErrorContains(t, err, "multiple harnesses marked default in settings: alpha, zeta")
	})

	t.Run("no default flag falls back to builtin", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
harnesses:
  codex: { path: /opt/bundles/codex }
`)
		name, err := bundler.ResolveHarnessName(cfg, "")
		require.NoError(t, err)
		assert.Equal(t, bundler.DefaultHarnessName, name)
	})
}

func TestEnsureHarnesses_SeedsRegistryAndBundles(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)

	require.NoError(t, bundler.EnsureHarnesses(cfg))

	// Bundle files materialized to the seeded default location, and the
	// registry entry records that path explicitly.
	bundleDir, err := bundler.HarnessBundleDir(cfg, bundler.DefaultHarnessName)
	require.NoError(t, err)
	assert.Equal(t, bundler.ShippedBundleDefaultDir(bundler.DefaultHarnessName), bundleDir)
	assert.FileExists(t, filepath.Join(bundleDir, harness.ManifestFile))
	assert.FileExists(t, filepath.Join(bundleDir, harness.TemplateFile))

	// Registry seeded: every shipped harness has an entry WITH an explicit
	// path, the built-in default carries the flag, and resolution now reads
	// it from settings.
	reg := cfg.Settings().Harnesses
	for _, name := range bundler.ShippedHarnessNames() {
		assert.Contains(t, reg, name)
		assert.NotEmpty(t, reg[name].Path, "every seeded entry carries an explicit bundle path")
	}
	assert.True(t, reg[bundler.DefaultHarnessName].Default)

	name, err := bundler.ResolveHarnessName(cfg, "")
	require.NoError(t, err)
	assert.Equal(t, bundler.DefaultHarnessName, name)

	// Idempotent: a second ensure rewrites nothing.
	settingsPath, err := consts.SettingsFilePath()
	require.NoError(t, err)
	before, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	require.NoError(t, bundler.EnsureHarnesses(cfg))
	after, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestEnsureHarnesses_NeverClobbersUserEntries(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)

	// User already prefers a custom harness before the first ensure.
	require.NoError(t, cfg.SettingsStore().Set("harnesses", map[string]config.HarnessSettings{
		"codex": {Default: true, Path: "/opt/bundles/codex"},
	}))
	require.NoError(t, cfg.SettingsStore().Write())

	require.NoError(t, bundler.EnsureHarnesses(cfg))

	reg := cfg.Settings().Harnesses
	// User entry untouched.
	assert.Equal(t, config.HarnessSettings{Default: true, Path: "/opt/bundles/codex"}, reg["codex"])
	// Shipped entry seeded WITHOUT the default flag — codex already holds it.
	assert.Contains(t, reg, bundler.DefaultHarnessName)
	assert.False(t, reg[bundler.DefaultHarnessName].Default)

	name, err := bundler.ResolveHarnessName(cfg, "")
	require.NoError(t, err)
	assert.Equal(t, "codex", name)
}

// TestEnsureHarnesses_BackfillsMissingPath: a shipped entry that predates
// the explicit-path requirement (path: "") is healed to the seeded default
// dir; a user-relocated path is never touched.
func TestEnsureHarnesses_BackfillsMissingPath(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)

	require.NoError(t, cfg.SettingsStore().Set("harnesses", map[string]config.HarnessSettings{
		bundler.DefaultHarnessName: {Default: true, Path: ""},
	}))
	require.NoError(t, cfg.SettingsStore().Write())

	require.NoError(t, bundler.EnsureHarnesses(cfg))

	reg := cfg.Settings().Harnesses
	assert.Equal(t,
		bundler.ShippedBundleDefaultDir(bundler.DefaultHarnessName),
		reg[bundler.DefaultHarnessName].Path,
		"empty path on a shipped entry is backfilled")
	assert.True(t, reg[bundler.DefaultHarnessName].Default, "flag preserved through backfill")
}

// TestLoadHarness_RegistryOnly: once ANY registry exists, resolution is
// registry-only — an unregistered shipped name is an error, never an
// embedded-fallback load; with no registry at all the bootstrap seam loads
// shipped bundles from the embedded assets.
func TestLoadHarness_RegistryOnly(t *testing.T) {
	t.Run("registry present, shipped name unregistered", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
harnesses:
  other: { default: true, path: /opt/bundles/other }
`)
		_, err := bundler.LoadHarness(cfg, bundler.DefaultHarnessName)
		require.ErrorContains(t, err, "is not registered")
	})

	t.Run("no registry, shipped name loads embedded", func(t *testing.T) {
		cfg := configmocks.NewBlankConfig()
		b, err := bundler.LoadHarness(cfg, bundler.DefaultHarnessName)
		require.NoError(t, err)
		assert.Equal(t, bundler.DefaultHarnessName, b.Name)
	})

	t.Run("registered path without a bundle errors actionably", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
harnesses:
  claude: { default: true, path: /nonexistent/bundle-dir }
`)
		_, err := bundler.LoadHarness(cfg, "claude")
		require.ErrorContains(t, err, "no bundle at registered path")
	})
}

func TestHarnessBundleDir(t *testing.T) {
	cfg := configmocks.NewFromString("", `
harnesses:
  custom: { path: /opt/bundles/custom }
  relocated: { default: true }
`)

	dir, err := bundler.HarnessBundleDir(cfg, "custom")
	require.NoError(t, err)
	assert.Equal(t, "/opt/bundles/custom", dir)

	// Every entry carries an explicit path — a path-less entry and an
	// unregistered name are both hard errors, never fallback resolution.
	_, err = bundler.HarnessBundleDir(cfg, "relocated")
	require.ErrorContains(t, err, "has no bundle path")
	_, err = bundler.HarnessBundleDir(cfg, "unregistered")
	require.ErrorContains(t, err, "is not registered")
}
