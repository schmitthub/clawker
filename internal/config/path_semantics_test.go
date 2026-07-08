package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
)

// TestExpandPaths pins the host-side expansion vocabulary and the
// container-side path normalization.
func TestExpandPaths(t *testing.T) {
	t.Run("host side resolves env default fallback", func(t *testing.T) {
		t.Setenv("CLAWKER_TEST_EXPAND", "")
		got, err := config.ExpandHostPath("${CLAWKER_TEST_EXPAND:-~/.codex}/prompts")
		require.NoError(t, err)
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, ".codex", "prompts"), got)

		t.Setenv("CLAWKER_TEST_EXPAND", "/opt/state")
		got, err = config.ExpandHostPath("${CLAWKER_TEST_EXPAND:-~/.codex}/prompts")
		require.NoError(t, err)
		assert.Equal(t, "/opt/state/prompts", got)

		got, err = config.ExpandHostPath("$CLAWKER_TEST_EXPAND/x")
		require.NoError(t, err)
		assert.Equal(t, "/opt/state/x", got)
	})

	t.Run("host side absolutizes relative results", func(t *testing.T) {
		// A relative env value (multi-account workflows) resolves against
		// the current working directory.
		got, err := config.ExpandHostPath("prompts")
		require.NoError(t, err)
		wd, err := os.Getwd()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(wd, "prompts"), got)
	})

	t.Run("container side normalizes", func(t *testing.T) {
		assert.Equal(t, ".codex/prompts", config.NormalizeContainerPath("./.codex/prompts/"))
		assert.Equal(t, ".codex", config.NormalizeContainerPath(".codex"))
	})

	t.Run("glob meta ignores env references", func(t *testing.T) {
		assert.False(t, config.HasGlobMeta("${CLAUDE_CONFIG_DIR:-~/.claude}/settings.json"))
		assert.True(t, config.HasGlobMeta("${CLAUDE_CONFIG_DIR:-~/.claude}/*.json"))
	})
}
