package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
)

// --- Schema round-trip: the registry/overlay nodes parse into typed
// fields exactly as declared. ---

func TestProjectSchema_BuildHarnessOverlay(t *testing.T) {
	cfg, err := config.NewFromString(`
build:
  stacks: [go, my-rust]
  harnesses:
    claude:
      stacks: [bun]
      packages: [libnss3]
      inject:
        user_commands: ["echo hi"]
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
	assert.Equal(t, []string{"echo hi"}, overlay.Inject.UserCommands)
	assert.Equal(t, []string{"echo bye"}, overlay.Inject.BeforeEntrypoint)
}

// --- Front-door validation: name rule, path shape, unknown fields. ---

// Conformance: E21 — a bad registered name fails the whole config load.
func TestValidateProjectNodes_HarnessName(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  Claude_Code:
    mount_projects: false
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harnesses.Claude_Code")
}

// Conformance: E21 — a reserved harness name fails the whole config load.
func TestValidateProjectNodes_HarnessReservedName(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  latest:
    mount_projects: false
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved")
}

func TestValidateProjectNodes_BuildHarnessOverlayName(t *testing.T) {
	_, err := config.NewFromString(`
build:
  harnesses:
    Claude:
      packages: [foo]
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.harnesses.Claude")
}

// The build.harness selection key takes the shared harness reference rule:
// bare or qualified passes, a malformed name or non-string value fails the
// whole config load naming the offending file and key.
func TestValidateProjectNodes_BuildHarnessSelection(t *testing.T) {
	t.Run("bare name passes", func(t *testing.T) {
		_, err := config.NewFromString("build:\n  harness: opencode\n", "")
		require.NoError(t, err)
	})
	t.Run("qualified address passes", func(t *testing.T) {
		_, err := config.NewFromString("build:\n  harness: acme.tools.codex\n", "")
		require.NoError(t, err)
	})
	t.Run("bad name fails naming the key", func(t *testing.T) {
		_, err := config.NewFromString("build:\n  harness: Bad_Name\n", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "build.harness")
	})
	t.Run("reserved alias fails", func(t *testing.T) {
		_, err := config.NewFromString("build:\n  harness: default\n", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "build.harness")
	})
}

// Conformance: E21 — a bad build.stacks declared name fails the whole config load.
func TestValidateProjectNodes_BuildStacksName(t *testing.T) {
	_, err := config.NewFromString(`
build:
  stacks: [Bad_Name]
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.stacks")
}

// Conformance: E21 — a bad overlay declared name fails the whole config load.
func TestValidateProjectNodes_OverlayStacksName(t *testing.T) {
	_, err := config.NewFromString(`
build:
  harnesses:
    claude:
      stacks: [Bad_Name]
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.harnesses.claude.stacks")
}

// TestValidateProjectNodes_QualifiedSelectionKeys proves the front door
// accepts qualified namespace.bundle.component spellings everywhere a
// selection key appears — build.stacks entries, overlay stacks, harnesses:
// init-config keys, and build.harnesses: overlay keys — while still rejecting
// malformed dotted forms and reserved bare aliases. Under the old bare-only
// ValidateName/ValidateHarnessName rules every qualified case here failed
// config load, making bundled components unselectable end to end.
func TestValidateProjectNodes_QualifiedSelectionKeys(t *testing.T) {
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

// Conformance: E21 — an unknown field under the overlay node is rejected at load.
func TestValidateProjectNodes_UnknownFieldInHarnessOverlay(t *testing.T) {
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
func TestValidateProjectNodes_UnknownFieldInOverlayInject(t *testing.T) {
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
func TestValidateProjectNodes_UnknownFieldInHarnessEntry(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  claude:
    typo_field: true
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harnesses.claude.typo_field")
	assert.Contains(t, err.Error(), "unknown field")
}

func TestValidateProjectNodes_ValidConfigPasses(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  codex:
    mount_projects: true
build:
  stacks: [go, my-rust]
  harnesses:
    claude:
      stacks: [bun]
      packages: [libnss3]
      inject:
        user_commands: ["echo hi"]
`, "")
	require.NoError(t, err)
}

// TestValidateProjectNodes_NullNodesAccepted covers YAML null nodes —
// a key written with no content (a bare "build:" line, a placeholder
// harness entry) decodes to the zero struct and must NOT be rejected as a
// malformed mapping.
func TestValidateProjectNodes_NullNodesAccepted(t *testing.T) {
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
func TestValidateProjectNodes_HarnessConfigStrategy(t *testing.T) {
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

func TestNewProjectStoreFromPreset_ValidatesNodes(t *testing.T) {
	_, err := config.NewProjectStoreFromPreset(`
harnesses:
  Bad_Name:
    mount_projects: false
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harnesses.Bad_Name")
}
