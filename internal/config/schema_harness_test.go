package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
)

// TestHarnessConfigFor pins the resolution order: project harnesses map
// entry → legacy agent.claude_code (built-in default harness only) → nil
// (defaults). The legacy block must never leak onto another harness.
func TestHarnessConfigFor(t *testing.T) {
	t.Run("map entry wins over legacy block", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
harnesses:
  claude:
    mount_projects: false
agent:
  claude_code:
    mount_projects: true
`, "")
		hc := cfg.HarnessConfigFor(consts.DefaultHarnessName)
		assert.False(t, hc.MountProjectsEnabled())
	})

	t.Run("legacy block covers the built-in default harness", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
agent:
  claude_code:
    mount_projects: false
`, "")
		hc := cfg.HarnessConfigFor(consts.DefaultHarnessName)
		assert.False(t, hc.MountProjectsEnabled())
	})

	t.Run("legacy block never leaks onto another harness", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
agent:
  claude_code:
    mount_projects: false
`, "")
		hc := cfg.HarnessConfigFor("codex")
		assert.Nil(t, hc)
		// Nil-tolerant accessors yield defaults.
		assert.True(t, hc.MountProjectsEnabled())
		assert.Equal(t, "copy", hc.ConfigStrategy())
	})

	t.Run("qualified harness name is addressed exactly", func(t *testing.T) {
		// A qualified name (namespace.bundle.component) is a legal map key and
		// contains dots. The store addresses it as one segment, so it can never
		// be reparsed into nested nodes — the dotted-key corruption class is
		// structurally gone.
		cfg := configmocks.NewFromString(`
harnesses:
  acme.tools.codex:
    mount_projects: false
`, "")
		hc := cfg.HarnessConfigFor("acme.tools.codex")
		require.NotNil(t, hc)
		assert.False(t, hc.MountProjectsEnabled())
		assert.Nil(t, cfg.HarnessConfigFor("acme"), "no partial-segment match")
	})

	t.Run("per-harness entries are independent", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
harnesses:
  codex:
    config: { strategy: fresh }
`, "")
		assert.Equal(t, "fresh", cfg.HarnessConfigFor("codex").ConfigStrategy())
		assert.Nil(t, cfg.HarnessConfigFor(consts.DefaultHarnessName))
	})
}

// TestPostInitFor pins hook composition: the harness-agnostic
// agent.post_init base runs first, the selected harness's post_init is
// appended, blank layers are skipped, and another harness's entry never
// applies.
func TestPostInitFor(t *testing.T) {
	t.Run("composes base then harness", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
agent:
  post_init: "npm install"
harnesses:
  codex:
    post_init: "codex login status"
`, "")
		assert.Equal(t, "npm install\ncodex login status", cfg.PostInitFor("codex"))
	})

	t.Run("harness only", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
harnesses:
  codex:
    post_init: "codex login status"
`, "")
		assert.Equal(t, "codex login status", cfg.PostInitFor("codex"))
	})

	t.Run("base only when harness has no hook", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
agent:
  post_init: "npm install"
harnesses:
  codex: {}
`, "")
		assert.Equal(t, "npm install", cfg.PostInitFor("codex"))
	})

	t.Run("other harness's hook never applies", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
agent:
  post_init: "npm install"
harnesses:
  claude:
    post_init: "claude mcp add foo"
`, "")
		assert.Equal(t, "npm install", cfg.PostInitFor("codex"))
	})

	t.Run("legacy claude_code block composes for the built-in default", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
agent:
  post_init: "npm install"
  claude_code:
    post_init: "claude mcp add foo"
`, "")
		assert.Equal(t, "npm install\nclaude mcp add foo", cfg.PostInitFor(consts.DefaultHarnessName))
	})

	t.Run("both blank yields empty", func(t *testing.T) {
		cfg := configmocks.NewFromString(``, "")
		assert.Empty(t, cfg.PostInitFor("codex"))
	})
}

// TestPreRunFor pins the same composition contract for the every-start
// pre_run hook.
func TestPreRunFor(t *testing.T) {
	t.Run("composes base then harness", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
agent:
  pre_run: "npm ci"
harnesses:
  codex:
    pre_run: "codex --version"
`, "")
		assert.Equal(t, "npm ci\ncodex --version", cfg.PreRunFor("codex"))
	})

	t.Run("harness only", func(t *testing.T) {
		cfg := configmocks.NewFromString(`
harnesses:
  codex:
    pre_run: "codex --version"
`, "")
		assert.Equal(t, "codex --version", cfg.PreRunFor("codex"))
	})
}
