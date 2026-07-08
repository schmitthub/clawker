package shared_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// envAgentCfg builds an AgentConfig carrying only the env spec under test.
func envAgentCfg(envFile, fromEnv []string, env map[string]string) config.AgentConfig {
	return config.AgentConfig{
		EnvFile:         envFile,
		FromEnv:         fromEnv,
		Env:             env,
		Editor:          "",
		Visual:          "",
		ClaudeCode:      nil,
		EnableSharedDir: nil,
		PostInit:        "",
		PreRun:          "",
	}
}

// envHarnessCfg builds a HarnessConfig carrying only the env spec under test.
func envHarnessCfg(envFile, fromEnv []string, env map[string]string) *config.HarnessConfig {
	return &config.HarnessConfig{
		Config:        config.HarnessConfigOptions{Strategy: ""},
		MountProjects: nil,
		EnvFile:       envFile,
		FromEnv:       fromEnv,
		Env:           env,
		PostInit:      "",
		PreRun:        "",
		Path:          "",
	}
}

// TestResolveAgentEnv_HarnessLayering pins the two-spec composition contract:
// the harness spec layers over the agent base (harness wins on collision),
// each spec keeps its internal env_file < from_env < env precedence, and a
// nil harness config applies the base only.
func TestResolveAgentEnv_HarnessLayering(t *testing.T) {
	log := logger.Nop()

	t.Run("harness env overrides agent env on collision", func(t *testing.T) {
		agent := envAgentCfg(nil, nil, map[string]string{"SHARED": "agent", "BASE_ONLY": "base"})
		harness := envHarnessCfg(nil, nil, map[string]string{"SHARED": "harness", "HARNESS_ONLY": "extra"})

		got, warnings, err := shared.ResolveAgentEnv(agent, harness, "codex", t.TempDir(), log)
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.Equal(t, map[string]string{
			"SHARED":       "harness",
			"BASE_ONLY":    "base",
			"HARNESS_ONLY": "extra",
		}, got)
	})

	t.Run("agent env_file is the lowest base layer", func(t *testing.T) {
		dir := t.TempDir()
		envPath := filepath.Join(dir, ".env")
		require.NoError(t, os.WriteFile(envPath, []byte("KEY=file\nFILE_ONLY=kept\n"), 0o600))

		agent := envAgentCfg([]string{".env"}, nil, map[string]string{"KEY": "env"})

		got, _, err := shared.ResolveAgentEnv(agent, nil, "claude", dir, log)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"KEY": "env", "FILE_ONLY": "kept"}, got)
	})

	t.Run("nil harness config applies base spec only", func(t *testing.T) {
		agent := envAgentCfg(nil, nil, map[string]string{"BASE": "v"})

		got, warnings, err := shared.ResolveAgentEnv(agent, nil, "codex", t.TempDir(), log)
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.Equal(t, map[string]string{"BASE": "v"}, got)
	})

	t.Run("harness env_file layers over agent env", func(t *testing.T) {
		dir := t.TempDir()
		envPath := filepath.Join(dir, ".env.codex")
		require.NoError(t, os.WriteFile(envPath, []byte("SHARED=from-file\n"), 0o600))

		agent := envAgentCfg(nil, nil, map[string]string{"SHARED": "agent"})
		harness := envHarnessCfg([]string{".env.codex"}, nil, nil)

		got, _, err := shared.ResolveAgentEnv(agent, harness, "codex", dir, log)
		require.NoError(t, err)
		assert.Equal(t, "from-file", got["SHARED"])
	})

	t.Run("harness env beats harness env_file within the spec", func(t *testing.T) {
		dir := t.TempDir()
		envPath := filepath.Join(dir, ".env.codex")
		require.NoError(t, os.WriteFile(envPath, []byte("KEY=file\n"), 0o600))

		harness := envHarnessCfg([]string{".env.codex"}, nil, map[string]string{"KEY": "explicit"})

		got, _, err := shared.ResolveAgentEnv(envAgentCfg(nil, nil, nil), harness, "codex", dir, log)
		require.NoError(t, err)
		assert.Equal(t, "explicit", got["KEY"])
	})

	t.Run("warnings and errors carry the harness scope", func(t *testing.T) {
		harness := envHarnessCfg(nil, []string{"CLAWKER_TEST_DEFINITELY_UNSET_VAR"}, nil)

		_, warnings, err := shared.ResolveAgentEnv(envAgentCfg(nil, nil, nil), harness, "codex", t.TempDir(), log)
		require.NoError(t, err)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "harnesses.codex.from_env")

		badFile := envHarnessCfg([]string{"does-not-exist.env"}, nil, nil)
		_, _, err = shared.ResolveAgentEnv(envAgentCfg(nil, nil, nil), badFile, "codex", t.TempDir(), log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "harnesses.codex.env_file")
	})

	t.Run("agent-scope diagnostics unchanged", func(t *testing.T) {
		agent := envAgentCfg(nil, []string{"CLAWKER_TEST_DEFINITELY_UNSET_VAR"}, nil)

		_, warnings, err := shared.ResolveAgentEnv(agent, nil, "claude", t.TempDir(), log)
		require.NoError(t, err)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "agent.from_env")
	})
}
