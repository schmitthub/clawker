package project_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustConfig(t *testing.T, yaml string) config.Config {
	t.Helper()
	cfg := configmocks.NewFromString(yaml)
	return cfg
}

func newFSConfigFromProjectTestdata(t *testing.T) (config.Config, string, string) {
	t.Helper()
	cfg, _ := configmocks.NewIsolatedTestConfig(t)
	configDir := os.Getenv(cfg.ConfigDirEnvVar())
	registryPath := filepath.Join(configDir, cfg.ProjectRegistryFileName())
	return cfg, registryPath, ""
}

func TestProjectManager_Register_UninitializedManager(t *testing.T) {
	mgr := project.NewProjectManager(nil)

	p, err := mgr.Register(context.Background(), "My Project", "/tmp/myproject")
	require.Error(t, err)
	assert.Nil(t, p)
}

func TestProjectManager_Register_UsesRootIdentity(t *testing.T) {
	cfg, registryPath, _ := newFSConfigFromProjectTestdata(t)
	mgr := project.NewProjectManager(cfg)

	projectRoot := filepath.Join(t.TempDir(), "myproject")
	require.NoError(t, os.MkdirAll(projectRoot, 0o755))

	registeredProject, err := mgr.Register(context.Background(), "My Project", projectRoot)
	require.NoError(t, err)
	require.NotNil(t, registeredProject)

	record, err := registeredProject.Record()
	require.NoError(t, err)
	assert.Equal(t, "My Project", record.Name)
	assert.Equal(t, projectRoot, record.Root)

	_, err = mgr.Register(context.Background(), "Renamed Project", projectRoot)
	require.Error(t, err)
	assert.ErrorIs(t, err, project.ErrProjectExists)

	b, err := os.ReadFile(registryPath)
	require.NoError(t, err)
	assert.Contains(t, string(b), "- name: My Project")
	assert.Contains(t, string(b), "root: "+projectRoot)
	assert.NotContains(t, string(b), "my-project:")
}

func TestRegistry_RemoveByRoot(t *testing.T) {
	cfg, _ := configmocks.NewIsolatedTestConfig(t)
	require.NoError(t, cfg.Set("projects", []any{
		map[string]any{"name": "one", "root": "/tmp/one"},
		map[string]any{"name": "two", "root": "/tmp/two"},
	}))

	registry := project.NewRegistryForTest(cfg)
	require.NoError(t, registry.RemoveByRoot("/tmp/one"))

	entries := registry.Projects()
	require.Len(t, entries, 1)
	assert.Equal(t, "two", entries[0].Name)
	assert.Equal(t, "/tmp/two", entries[0].Root)
}
