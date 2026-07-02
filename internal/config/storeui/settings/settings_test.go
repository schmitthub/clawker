package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/storeui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverrides_AllPathsMatchSettingsFields(t *testing.T) {
	fields := storeui.WalkFields(config.Settings{})
	fieldPaths := make(map[string]bool, len(fields))
	for _, f := range fields {
		fieldPaths[f.Path] = true
	}

	overrides := Overrides()
	for _, ov := range overrides {
		assert.True(t, fieldPaths[ov.Path],
			"override path %q does not match any field in config.Settings", ov.Path)
	}
}

func TestOverrides_NoOrphans(t *testing.T) {
	// Verify no duplicate paths in overrides.
	overrides := Overrides()
	seen := make(map[string]bool, len(overrides))
	for _, ov := range overrides {
		assert.False(t, seen[ov.Path],
			"duplicate override path %q", ov.Path)
		seen[ov.Path] = true
	}
}

func TestOverrides_HostProxyReadOnly(t *testing.T) {
	overrides := Overrides()
	hostProxyPaths := []string{
		"host_proxy.manager.port",
		"host_proxy.daemon.port",
		"host_proxy.daemon.poll_interval",
		"host_proxy.daemon.grace_period",
		"host_proxy.daemon.max_consecutive_errs",
	}

	overrideMap := make(map[string]*storeui.Override, len(overrides))
	for i := range overrides {
		overrideMap[overrides[i].Path] = &overrides[i]
	}

	for _, path := range hostProxyPaths {
		ov, ok := overrideMap[path]
		if assert.True(t, ok, "missing override for %q", path) {
			assert.NotNil(t, ov.ReadOnly, "override for %q should set ReadOnly", path)
			assert.True(t, *ov.ReadOnly, "override for %q should be read-only", path)
		}
	}
}

// The settings store discovers files in the config dir only (no walk-up).
// Offering a CWD save target would silently lose data: the write succeeds
// but the store never reads the file back.
func TestLayerTargets_NoCWDTarget(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := configmocks.NewIsolatedTestConfig(t)

	targets, err := LayerTargets(cfg.SettingsStore())
	require.NoError(t, err)
	require.NotEmpty(t, targets)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	for _, tgt := range targets {
		assert.NotEqual(t, "Project", tgt.Label,
			"settings editor must not offer a Project target — the settings store cannot rediscover CWD files")
		assert.False(t, strings.HasPrefix(tgt.Path, cwd+string(os.PathSeparator)),
			"settings target %q must not point under CWD", tgt.Path)
	}

	assert.Equal(t, "User", targets[0].Label)
	assert.Equal(t, filepath.Join(config.ConfigDir(), cfg.SettingsFileName()), targets[0].Path)
}
