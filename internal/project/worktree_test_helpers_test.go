package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

func newFSConfigFromProjectTestdata(t *testing.T) (config.Config, string, string) {
	t.Helper()
	cfg, _ := configmocks.NewIsolatedTestConfig(t)
	configDir := os.Getenv(cfg.ConfigDirEnvVar())
	registryPath := filepath.Join(configDir, "projects.yaml")
	return cfg, registryPath, ""
}
