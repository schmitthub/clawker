package shared_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmd/project/shared"
	"github.com/schmitthub/clawker/internal/config"
)

// setupIsolatedProjectDir creates an isolated config environment, optionally
// registers the project in the registry, places a config file, sets CWD,
// and returns a fresh Config.
//
// If registered is true, a registry entry is written so that the storage
// engine's walk-up discovery finds the project root. If false, no registry
// is written — this exercises the filesystem fallback probe.
//
// placement controls where the config file is written relative to projectDir:
//
//	"flat"      → projectDir/.clawker.yaml
//	"flat-yml"  → projectDir/.clawker.yml
//	"dir"       → projectDir/.clawker/clawker.yaml
//	"dir-local" → projectDir/.clawker/clawker.local.yaml
//	"none"      → no config file written
func setupIsolatedProjectDir(t *testing.T, placement string, registered bool) (cfg config.Config, projectDir string) {
	t.Helper()

	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	dataDir := filepath.Join(base, "data")
	stateDir := filepath.Join(base, "state")
	projectDir = filepath.Join(base, "project")

	for _, dir := range []string{configDir, dataDir, stateDir, projectDir} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	t.Setenv("CLAWKER_CONFIG_DIR", configDir)
	t.Setenv("CLAWKER_DATA_DIR", dataDir)
	t.Setenv("CLAWKER_STATE_DIR", stateDir)

	if registered {
		registryContent := "projects:\n  - name: test-project\n    root: " + projectDir + "\n"
		require.NoError(t, os.WriteFile(filepath.Join(dataDir, "registry.yaml"), []byte(registryContent), 0o644))
	}

	minimalYAML := "build:\n  image: alpine\n"
	switch placement {
	case "flat":
		require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".clawker.yaml"), []byte(minimalYAML), 0o644))
	case "flat-yml":
		require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".clawker.yml"), []byte(minimalYAML), 0o644))
	case "dir":
		clawkerDir := filepath.Join(projectDir, ".clawker")
		require.NoError(t, os.MkdirAll(clawkerDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(clawkerDir, "clawker.yaml"), []byte(minimalYAML), 0o644))
	case "dir-local":
		clawkerDir := filepath.Join(projectDir, ".clawker")
		require.NoError(t, os.MkdirAll(clawkerDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(clawkerDir, "clawker.local.yaml"), []byte(minimalYAML), 0o644))
	case "none":
		// no file
	default:
		t.Fatalf("unknown placement: %s", placement)
	}

	t.Chdir(projectDir)

	cfg, err := config.NewConfig()
	require.NoError(t, err)

	return cfg, projectDir
}

func TestHasLocalProjectConfig(t *testing.T) {
	tests := []struct {
		name       string
		placement  string
		registered bool
		checkDir   string // "project" = project dir, "other" = unrelated dir
		want       bool
	}{
		// Registered project — storage layer discovers via walk-up.
		{
			name:       "registered/flat .clawker.yaml",
			placement:  "flat",
			registered: true,
			checkDir:   "project",
			want:       true,
		},
		{
			name:       "registered/flat .clawker.yml",
			placement:  "flat-yml",
			registered: true,
			checkDir:   "project",
			want:       true,
		},
		{
			name:       "registered/dir form .clawker/clawker.yaml",
			placement:  "dir",
			registered: true,
			checkDir:   "project",
			want:       true,
		},
		{
			name:       "registered/dir form .clawker/clawker.local.yaml",
			placement:  "dir-local",
			registered: true,
			checkDir:   "project",
			want:       true,
		},
		{
			name:       "registered/no config",
			placement:  "none",
			registered: true,
			checkDir:   "project",
			want:       false,
		},
		{
			name:       "registered/wrong directory",
			placement:  "flat",
			registered: true,
			checkDir:   "other",
			want:       false,
		},

		// Unregistered project — filesystem fallback probe.
		{
			name:       "unregistered/flat .clawker.yaml",
			placement:  "flat",
			registered: false,
			checkDir:   "project",
			want:       true,
		},
		{
			name:       "unregistered/flat .clawker.yml",
			placement:  "flat-yml",
			registered: false,
			checkDir:   "project",
			want:       true,
		},
		{
			name:       "unregistered/dir form .clawker/clawker.yaml",
			placement:  "dir",
			registered: false,
			checkDir:   "project",
			want:       true,
		},
		{
			name:       "unregistered/dir form .clawker/clawker.local.yaml",
			placement:  "dir-local",
			registered: false,
			checkDir:   "project",
			want:       true,
		},
		{
			name:       "unregistered/no config",
			placement:  "none",
			registered: false,
			checkDir:   "project",
			want:       false,
		},
		{
			name:       "unregistered/wrong directory",
			placement:  "flat",
			registered: false,
			checkDir:   "other",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, projectDir := setupIsolatedProjectDir(t, tt.placement, tt.registered)

			dir := projectDir
			if tt.checkDir == "other" {
				dir = t.TempDir()
			}

			got := shared.HasLocalProjectConfig(cfg, dir)
			assert.Equal(t, tt.want, got)
		})
	}
}
