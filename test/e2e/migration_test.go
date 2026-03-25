package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

func TestMigrateRunInstructions_CobraTriggered(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			ProjectManager: project.NewProjectManager,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// Write legacy YAML with old - cmd: "..." format.
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: buildpack-deps:bookworm-scm
  instructions:
    root_run:
      - cmd: |
          ARCH=$(dpkg --print-architecture) && \
            curl -fsSL "https://golang.org/dl/go1.26.1.linux-${ARCH}.tar.gz" | tar -C /usr/local -xzf -
    user_run:
      - cmd: curl -LsSf https://astral.sh/uv/install.sh | sh
      - cmd: uv tool install pre-commit --with pre-commit-uv
`)

	// Register the project first so walk-up discovery works.
	regRes := h.Run("project", "register", "--yes", "migration-test")
	require.NoError(t, regRes.Err, "register failed: stdout=%s stderr=%s", regRes.Stdout, regRes.Stderr)

	// "project info" triggers a fresh NewConfig with walk-up → discovers
	// the registered project config → migration fires → file rewritten.
	// No Docker needed — just reads config and project registry.
	infoRes := h.Run("project", "info", "migration-test")
	require.NoError(t, infoRes.Err, "project info failed: stdout=%s stderr=%s", infoRes.Stdout, infoRes.Stderr)

	// Verify the file on disk was rewritten without legacy cmd: keys.
	configPath := filepath.Join(setup.ProjectDir, ".clawker.yaml")
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	content := string(data)

	assert.NotContains(t, content, "cmd:", "migrated file should not contain legacy 'cmd:' keys")
	assert.Contains(t, content, "curl -LsSf", "migrated file should contain command strings")
	assert.Contains(t, content, "uv tool install", "migrated file should contain all commands")
}

func TestMigrateRunInstructions_AlreadyMigratedNoRewrite(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			ProjectManager: project.NewProjectManager,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// Already-migrated format — plain strings.
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: alpine
  instructions:
    user_run:
      - npm ci
      - pip install
`)

	regRes := h.Run("project", "register", "--yes", "no-migrate-test")
	require.NoError(t, regRes.Err, "register failed: stdout=%s stderr=%s", regRes.Stdout, regRes.Stderr)

	configPath := filepath.Join(setup.ProjectDir, ".clawker.yaml")
	beforeStat, err := os.Stat(configPath)
	require.NoError(t, err)

	infoRes := h.Run("project", "info", "no-migrate-test")
	require.NoError(t, infoRes.Err, "project info failed: stdout=%s stderr=%s", infoRes.Stdout, infoRes.Stderr)

	afterStat, err := os.Stat(configPath)
	require.NoError(t, err)
	assert.Equal(t, beforeStat.ModTime(), afterStat.ModTime(),
		"file should not be rewritten when already in new format")
}
