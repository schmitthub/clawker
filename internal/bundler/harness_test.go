package bundler_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundler"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
)

func TestResolveHarnessName(t *testing.T) {
	cfg := configmocks.NewFromString("", "")

	t.Run("explicit wins", func(t *testing.T) {
		name, err := bundler.ResolveHarnessName(cfg, "codex")
		require.NoError(t, err)
		assert.Equal(t, "codex", name)
	})

	t.Run("no selection falls back to the built-in default", func(t *testing.T) {
		name, err := bundler.ResolveHarnessName(cfg, "")
		require.NoError(t, err)
		assert.Equal(t, bundler.DefaultHarnessName, name)
	})

	t.Run("explicit reserved alias is rejected", func(t *testing.T) {
		_, err := bundler.ResolveHarnessName(cfg, consts.ImageTagBase)
		require.ErrorContains(t, err, "reserved")
	})
}

func TestValidateHarnessKey_ReservedTags(t *testing.T) {
	for _, reserved := range []string{
		consts.ImageTagDefaultAlias,
		consts.ImageTagLatest,
		consts.ImageTagBase,
	} {
		t.Run(reserved, func(t *testing.T) {
			require.ErrorContains(t, bundler.ValidateHarnessKey(reserved), "reserved")
		})
	}
}

func TestKnownHarnessNames(t *testing.T) {
	cfg := configmocks.NewFromString(`
harnesses:
  mycustom:
    path: /opt/bundles/mycustom
`, "")

	names := bundler.KnownHarnessNames(cfg)
	// Shipped bundles are always known...
	for _, shipped := range bundler.ShippedHarnessNames() {
		assert.Contains(t, names, shipped)
	}
	// ...plus the project-registered one.
	assert.Contains(t, names, "mycustom")
	assert.True(t, bundler.IsKnownHarness(cfg, "mycustom"))
	assert.False(t, bundler.IsKnownHarness(cfg, "nope"))
}

func TestKnownHarnessNames_InitConfigWithoutPathIsNotRegistered(t *testing.T) {
	// A project harnesses.<name> entry that only carries per-harness init
	// config (no path) is NOT a bundle registration: a NON-shipped name with
	// such an entry stays unknown. This is the path-guard's load-bearing case —
	// without the Path check, "mystery" would wrongly become a known harness.
	cfg := configmocks.NewFromString(`
harnesses:
  mystery:
    mount_projects: false
`, "")
	assert.False(t, bundler.IsKnownHarness(cfg, "mystery"),
		"an init-config-only entry must not register a harness")
	assert.NotContains(t, bundler.KnownHarnessNames(cfg), "mystery")
	// A shipped name with the same shape stays known — via the shipped set.
	assert.True(t, bundler.IsKnownHarness(cfg, "claude"))
}

func TestLoadHarness_ShippedVirtualBase(t *testing.T) {
	// No project registry entry → shipped bundles load straight from embedded.
	cfg := configmocks.NewFromString("", "")
	b, err := bundler.LoadHarness(cfg, bundler.DefaultHarnessName)
	require.NoError(t, err)
	assert.Equal(t, bundler.DefaultHarnessName, b.Name)
}

func TestLoadHarness_ProjectRegistered(t *testing.T) {
	dir := t.TempDir()
	writeBundle(t, dir, "version:\n  resolver: none\n")
	cfg := configmocks.NewFromString(`
harnesses:
  mytool:
    path: `+dir+`
`, "")

	b, err := bundler.LoadHarness(cfg, "mytool")
	require.NoError(t, err)
	assert.Equal(t, "mytool", b.Name)
}

func TestLoadHarness_RegisteredPathWithoutBundle(t *testing.T) {
	cfg := configmocks.NewFromString(`
harnesses:
  claude:
    path: /nonexistent/bundle-dir
`, "")
	_, err := bundler.LoadHarness(cfg, "claude")
	require.ErrorContains(t, err, "no bundle at registered path")
}

func TestLoadHarness_Unregistered(t *testing.T) {
	// A name that is neither shipped nor project-registered is a hard error
	// naming the registration remedy.
	cfg := configmocks.NewFromString("", "")
	_, err := bundler.LoadHarness(cfg, "no-such-harness")
	require.ErrorContains(t, err, "is not registered")
	require.ErrorContains(t, err, "clawker harness register")
}
