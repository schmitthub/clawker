package settings

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/storeui"
	"github.com/stretchr/testify/assert"
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
