package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
)

// --- Schema round-trip: the new registry/overlay nodes parse into typed
// fields exactly as declared. ---

func TestProjectSchema_StackRegistry(t *testing.T) {
	cfg, err := config.NewFromString(`
stacks:
  my-rust:
    path: ./stacks/my-rust
`, "")
	require.NoError(t, err)

	entry, ok := cfg.Project().Stacks["my-rust"]
	require.True(t, ok)
	assert.Equal(t, "./stacks/my-rust", entry.Path)
}

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

func TestValidateProjectRegistries_StackName(t *testing.T) {
	_, err := config.NewFromString(`
stacks:
  My_Rust:
    path: ./stacks/my-rust
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stacks.My_Rust")
}

func TestValidateProjectRegistries_HarnessName(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  Claude_Code:
    path: ./tools/claude
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harnesses.Claude_Code")
}

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

func TestValidateProjectRegistries_BuildStacksName(t *testing.T) {
	_, err := config.NewFromString(`
build:
  stacks: [Bad_Name]
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.stacks")
}

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

func TestValidateProjectRegistries_StackMissingPath(t *testing.T) {
	_, err := config.NewFromString(`
stacks:
  my-rust: {}
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stacks.my-rust")
	assert.Contains(t, err.Error(), "path")
}

func TestValidateProjectRegistries_PathRejectsTilde(t *testing.T) {
	_, err := config.NewFromString(`
stacks:
  my-rust:
    path: ~/stacks/my-rust
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stacks.my-rust.path")
	assert.Contains(t, err.Error(), "~")
}

func TestValidateProjectRegistries_PathRejectsEnvVar(t *testing.T) {
	_, err := config.NewFromString(`
harnesses:
  codex:
    path: $HOME/tools/codex
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harnesses.codex.path")
}

func TestValidateProjectRegistries_PathAcceptsAbsolute(t *testing.T) {
	_, err := config.NewFromString(`
stacks:
  my-rust:
    path: /opt/stacks/my-rust
`, "")
	require.NoError(t, err)
}

func TestValidateProjectRegistries_UnknownFieldInStackEntry(t *testing.T) {
	_, err := config.NewFromString(`
stacks:
  my-rust:
    path: ./stacks/my-rust
    version: "1.2.3"
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stacks.my-rust.version")
	assert.Contains(t, err.Error(), "unknown field")
}

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
stacks:
  my-rust:
    path: ./stacks/my-rust
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
		"bare stacks key":      "stacks:\n",
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

	t.Run("null stack entry still requires path", func(t *testing.T) {
		_, err := config.NewFromString("stacks:\n  my-rust:\n", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stacks.my-rust")
		assert.Contains(t, err.Error(), "path")
	})
}

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
stacks:
  Bad_Name:
    path: ./x
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stacks.Bad_Name")
}
