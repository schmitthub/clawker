package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
)

// --- Schema round-trip: the registry/overlay nodes parse into typed
// fields exactly as declared. ---

func TestProjectSchema_HarnessRegistryPath(t *testing.T) {
	cfg, err := config.NewFromString(`
harnesses:
  codex:
    path: ./tools/codex-bundle
    mount_projects: false
`, "")
	require.NoError(t, err)

	entry, ok := cfg.Project().Harnesses["codex"]
	require.True(t, ok)
	assert.Equal(t, "./tools/codex-bundle", entry.Path)
	require.NotNil(t, entry.MountProjects)
	assert.False(t, *entry.MountProjects)
}

func TestProjectSchema_BuildHarnessOverlay(t *testing.T) {
	cfg, err := config.NewFromString(`
build:
  stacks: [go, my-rust]
  harnesses:
    claude:
      stacks: [bun]
      packages: [libnss3]
      inject:
        after_harness_install: ["echo hi"]
        before_entrypoint: ["echo bye"]
`, "")
	require.NoError(t, err)

	b := cfg.Project().Build
	assert.Equal(t, []string{"go", "my-rust"}, b.Stacks)
	overlay, ok := b.Harnesses["claude"]
	require.True(t, ok)
	assert.Equal(t, []string{"bun"}, overlay.Stacks)
	assert.Equal(t, []string{"libnss3"}, overlay.Packages)
	require.NotNil(t, overlay.Inject)
	assert.Equal(t, []string{"echo hi"}, overlay.Inject.AfterHarnessInstall)
	assert.Equal(t, []string{"echo bye"}, overlay.Inject.BeforeEntrypoint)
}

// --- Front-door validation: name rule, path shape, unknown fields. ---

// Conformance: E21 — a bad registered name fails the whole config load.
func TestValidateProjectRegistries_HarnessName(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  Claude_Code:
    path: ./tools/claude
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harnesses.Claude_Code")
}

// Conformance: E21 — a reserved harness name fails the whole config load.
func TestValidateProjectRegistries_HarnessReservedName(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  latest:
    path: ./tools/x
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved")
}

func TestValidateProjectRegistries_BuildHarnessOverlayName(t *testing.T) {
	_, err := config.NewFromString(`
build:
  harnesses:
    Claude:
      packages: [foo]
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.harnesses.Claude")
}

// Conformance: E21 — a bad build.stacks declared name fails the whole config load.
func TestValidateProjectRegistries_BuildStacksName(t *testing.T) {
	_, err := config.NewFromString(`
build:
  stacks: [Bad_Name]
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.stacks")
}

// Conformance: E21 — a bad overlay declared name fails the whole config load.
func TestValidateProjectRegistries_OverlayStacksName(t *testing.T) {
	_, err := config.NewFromString(`
build:
  harnesses:
    claude:
      stacks: [Bad_Name]
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.harnesses.claude.stacks")
}

// TestValidateProjectRegistries_QualifiedSelectionKeys proves the front door
// accepts qualified namespace.bundle.component spellings everywhere a
// selection key appears — build.stacks entries, overlay stacks, harnesses:
// init-config keys, and build.harnesses: overlay keys — while still rejecting
// malformed dotted forms and reserved bare aliases. Under the old bare-only
// ValidateName/ValidateHarnessName rules every qualified case here failed
// config load, making bundled components unselectable end to end.
func TestValidateProjectRegistries_QualifiedSelectionKeys(t *testing.T) {
	t.Run("qualified keys accepted", func(t *testing.T) {
		_, err := config.NewFromString(`
harnesses:
  acme.tools.codex:
    env: {FOO: bar}
build:
  stacks: [node, acme.tools.rust]
  harnesses:
    acme.tools.codex:
      packages: [jq]
      stacks: [acme.tools.rust]
`, "")
		require.NoError(t, err)
	})

	t.Run("two-segment address rejected", func(t *testing.T) {
		_, err := config.NewFromString("build:\n  stacks: [acme.rust]\n", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "build.stacks")
	})

	t.Run("reserved bare alias overlay key still rejected", func(t *testing.T) {
		_, err := config.NewFromString(`
build:
  harnesses:
    latest:
      packages: [jq]
`, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reserved")
	})
}

// Conformance: E10 — a harness registry path rejects ~ home expansion at the load front-door.
func TestValidateProjectRegistries_PathRejectsTilde(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  codex:
    path: ~/tools/codex
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harnesses.codex.path")
	assert.Contains(t, err.Error(), "~")
}

// Conformance: E10 — a harness registry path rejects $VAR expansion at the load front-door.
func TestValidateProjectRegistries_PathRejectsEnvVar(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  codex:
    path: $HOME/tools/codex
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harnesses.codex.path")
}

// Conformance: E10 — a harness registry path accepts absolute (and relative) paths.
func TestValidateProjectRegistries_PathAcceptsAbsolute(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  codex:
    path: /opt/tools/codex
`, "")
	require.NoError(t, err)
}

// Conformance: E21 — an unknown field under the overlay node is rejected at load.
func TestValidateProjectRegistries_UnknownFieldInHarnessOverlay(t *testing.T) {
	_, err := config.NewFromString(`
build:
  harnesses:
    claude:
      typo_field: true
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.harnesses.claude.typo_field")
	assert.Contains(t, err.Error(), "unknown field")
}

// Conformance: E21 — an unknown field under the overlay inject node is rejected at load.
func TestValidateProjectRegistries_UnknownFieldInOverlayInject(t *testing.T) {
	_, err := config.NewFromString(`
build:
  harnesses:
    claude:
      inject:
        after_from: ["oops"]
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.harnesses.claude.inject.after_from")
	assert.Contains(t, err.Error(), "unknown field")
}

// Conformance: E21 — an unknown field under the harness registry node is rejected at load.
func TestValidateProjectRegistries_UnknownFieldInHarnessEntry(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  claude:
    typo_field: true
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harnesses.claude.typo_field")
	assert.Contains(t, err.Error(), "unknown field")
}

func TestValidateProjectRegistries_ValidConfigPasses(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  codex:
    path: ./tools/codex-bundle
build:
  stacks: [go, my-rust]
  harnesses:
    claude:
      stacks: [bun]
      packages: [libnss3]
      inject:
        after_harness_install: ["echo hi"]
`, "")
	require.NoError(t, err)
}

// TestValidateProjectRegistries_NullNodesAccepted covers YAML null nodes —
// a key written with no content (a bare "build:" line, a placeholder
// harness entry) decodes to the zero struct and must NOT be rejected as a
// malformed mapping.
func TestValidateProjectRegistries_NullNodesAccepted(t *testing.T) {
	for name, yaml := range map[string]string{
		"bare build key":       "build:\n",
		"bare harnesses key":   "harnesses:\n",
		"null harness entry":   "harnesses:\n  claude:\n",
		"null harness config":  "harnesses:\n  claude:\n    config:\n",
		"null strategy":        "harnesses:\n  claude:\n    config:\n      strategy:\n",
		"null build.stacks":    "build:\n  stacks:\n",
		"null build.harnesses": "build:\n  harnesses:\n",
		"null overlay entry":   "build:\n  harnesses:\n    claude:\n",
		"null overlay stacks":  "build:\n  harnesses:\n    claude:\n      stacks:\n",
		"null overlay inject":  "build:\n  harnesses:\n    claude:\n      inject:\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.NewFromString(yaml, "")
			require.NoError(t, err)
		})
	}
}

// Conformance: E21 — a bad config.strategy (and unknown field under it) is rejected at load.
func TestValidateProjectRegistries_HarnessConfigStrategy(t *testing.T) {
	t.Run("unknown strategy rejected", func(t *testing.T) {
		_, err := config.NewFromString(`
harnesses:
  claude:
    config:
      strategy: coppy
`, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "harnesses.claude.config.strategy")
		assert.Contains(t, err.Error(), "coppy")
	})

	t.Run("unknown field under config rejected", func(t *testing.T) {
		_, err := config.NewFromString(`
harnesses:
  claude:
    config:
      stratgy: copy
`, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "harnesses.claude.config.stratgy")
		assert.Contains(t, err.Error(), "unknown field")
	})

	t.Run("valid strategies pass", func(t *testing.T) {
		for _, strategy := range []string{config.ConfigStrategyCopy, config.ConfigStrategyFresh} {
			_, err := config.NewFromString("harnesses:\n  claude:\n    config:\n      strategy: "+strategy+"\n", "")
			require.NoError(t, err, "strategy %q must be accepted", strategy)
		}
	})
}

func TestNewProjectStoreFromPreset_ValidatesRegistries(t *testing.T) {
	_, err := config.NewProjectStoreFromPreset(`
harnesses:
  Bad_Name:
    path: ./x
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harnesses.Bad_Name")
}
