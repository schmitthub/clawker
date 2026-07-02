package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/storeui"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverrides_AllPathsMatchProjectFields(t *testing.T) {
	fields := storeui.WalkFields(config.Project{})
	fieldPaths := make(map[string]bool, len(fields))
	for _, f := range fields {
		fieldPaths[f.Path] = true
	}

	overrides := Overrides()
	for _, ov := range overrides {
		assert.True(t, fieldPaths[ov.Path],
			"override path %q does not match any field in config.Project", ov.Path)
	}
}

func TestOverrides_NoOrphans(t *testing.T) {
	overrides := Overrides()
	seen := make(map[string]bool, len(overrides))
	for _, ov := range overrides {
		assert.False(t, seen[ov.Path],
			"duplicate override path %q", ov.Path)
		seen[ov.Path] = true
	}
}

func TestOverrides_NoHiddenFields(t *testing.T) {
	overrides := Overrides()
	for _, ov := range overrides {
		assert.False(t, ov.Hidden,
			"override %q should not be hidden — all fields must be editable", ov.Path)
	}
}

func TestOverrides_SelectFields(t *testing.T) {
	overrides := Overrides()
	overrideMap := make(map[string]*storeui.Override, len(overrides))
	for i := range overrides {
		overrideMap[overrides[i].Path] = &overrides[i]
	}

	tests := []struct {
		path    string
		options []string
	}{
		{"workspace.default_mode", []string{"bind", "snapshot"}},
		{"agent.claude_code.config.strategy", []string{"copy", "fresh"}},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			ov, ok := overrideMap[tt.path]
			if !assert.True(t, ok, "missing override for %q", tt.path) {
				return
			}
			assert.NotNil(t, ov.Kind)
			assert.Equal(t, storeui.KindSelect, *ov.Kind)
			assert.Equal(t, tt.options, ov.Options)
		})
	}
}

// Inside a project the walk-up store offers a CWD "Local" target.
func TestLayerTargets_InProjectOffersLocal(t *testing.T) {
	env := testenv.New(t)
	projectDir := filepath.Join(env.Dirs.Base, "proj")
	require.NoError(t, os.MkdirAll(projectDir, 0o755))
	t.Chdir(projectDir)

	cfg, err := config.NewConfig(config.WithProjectRoot(projectDir))
	require.NoError(t, err)

	targets, err := LayerTargets(cfg.ProjectStore())
	require.NoError(t, err)
	require.NotEmpty(t, targets)

	assert.Equal(t, "Local", targets[0].Label)
	assert.Equal(t, filepath.Join(projectDir, "."+cfg.ProjectConfigFileName()), targets[0].Path)
	assert.Equal(t, "User", targets[1].Label)
	assert.Equal(t, filepath.Join(config.ConfigDir(), cfg.ProjectConfigFileName()), targets[1].Path)
}

// Outside a project (no walk-up anchor) the store cannot rediscover CWD
// files, so no "Local" target may be offered.
func TestLayerTargets_NoProjectRootExcludesLocal(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := configmocks.NewIsolatedTestConfig(t)

	targets, err := LayerTargets(cfg.ProjectStore())
	require.NoError(t, err)
	require.NotEmpty(t, targets)

	for _, tgt := range targets {
		assert.NotEqual(t, "Local", tgt.Label,
			"project editor outside a project must not offer a Local target")
	}
	assert.Equal(t, "User", targets[0].Label)
}
