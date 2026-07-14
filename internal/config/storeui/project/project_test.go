package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/storeui"
	"github.com/schmitthub/clawker/internal/testenv"
)

func TestOverrides_AllPathsMatchProjectFields(t *testing.T) {
	fields := storeui.WalkFields(config.Project{})
	fieldPaths := make(map[string]bool, len(fields))
	for _, f := range fields {
		fieldPaths[f.Path] = true
	}

	overrides := Overrides(configmocks.NewBlankConfig())
	for _, ov := range overrides {
		assert.True(t, fieldPaths[ov.Path],
			"override path %q does not match any field in config.Project", ov.Path)
	}
}

func TestOverrides_NoOrphans(t *testing.T) {
	overrides := Overrides(configmocks.NewBlankConfig())
	seen := make(map[string]bool, len(overrides))
	for _, ov := range overrides {
		assert.False(t, seen[ov.Path],
			"duplicate override path %q", ov.Path)
		seen[ov.Path] = true
	}
}

func TestOverrides_NoHiddenFields(t *testing.T) {
	overrides := Overrides(configmocks.NewBlankConfig())
	for _, ov := range overrides {
		assert.False(t, ov.Hidden,
			"override %q should not be hidden — all fields must be editable", ov.Path)
	}
}

func TestOverrides_SelectFields(t *testing.T) {
	overrides := Overrides(configmocks.NewBlankConfig())
	overrideMap := make(map[string]*storeui.Override, len(overrides))
	for i := range overrides {
		overrideMap[overrides[i].Path] = &overrides[i]
	}

	tests := []struct {
		path    string
		options []string
	}{
		{"workspace.default_mode", []string{"bind", "snapshot"}},
		{"build.harness", bundler.KnownHarnessNames(configmocks.NewBlankConfig())},
	}
	require.NotEmpty(t, bundler.KnownHarnessNames(configmocks.NewBlankConfig()),
		"harness enumeration must not be empty — the select would render optionless")

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

// The harnesses map replaced the deprecated agent.claude_code block as the
// per-harness init surface. The generic struct-map editor handles it natively
// (YAML mapping blob), so the editor exposes it with no override — and no
// override may target the deprecated agent.claude_code paths anymore.
func TestHarnessesMapEditableNatively(t *testing.T) {
	var proj config.Project
	fields := storeui.WalkFields(proj)
	var harnessesField *storeui.Field
	for i := range fields {
		if fields[i].Path == "harnesses" {
			harnessesField = &fields[i]
			break
		}
	}
	require.NotNil(t, harnessesField, "harnesses map must walk as an editable field")
	assert.Equal(t, storeui.KindStructMap, harnessesField.Kind)

	for _, ov := range Overrides(configmocks.NewBlankConfig()) {
		assert.NotContains(t, ov.Path, "agent.claude_code",
			"deprecated agent.claude_code paths must not carry editor overrides")
	}
}

// Inside a project the walk-up store offers a CWD "Project" target.
func TestLayerTargets_InProjectOffersProject(t *testing.T) {
	env := testenv.New(t)
	projectDir := filepath.Join(env.Dirs.Base, "proj")
	require.NoError(t, os.MkdirAll(projectDir, 0o755))
	t.Chdir(projectDir)

	cfg, err := config.NewConfig(config.WithProjectRoot(projectDir))
	require.NoError(t, err)

	targets, err := LayerTargets(cfg.ProjectStore())
	require.NoError(t, err)
	require.NotEmpty(t, targets)

	assert.Equal(t, "Project", targets[0].Label)
	assert.Equal(t, filepath.Join(projectDir, "."+cfg.ProjectConfigFileName()), targets[0].Path)
	assert.Equal(t, "User", targets[1].Label)
	assert.Equal(t, filepath.Join(config.ConfigDir(), cfg.ProjectConfigFileName()), targets[1].Path)
}

// The user-reported regression layout: a flat .clawker.yaml main file beside
// a .clawker/ directory holding a dotted local override. Both files are in
// play — the flat main is the "Project" target (no phantom .clawker/
// candidate), and the local override is discovered and relabeled "Local".
func TestLayerTargets_MixedPlacementLocalOverride(t *testing.T) {
	env := testenv.New(t)
	projectDir := filepath.Join(env.Dirs.Base, "proj")
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".clawker"), 0o755))
	mainPath := filepath.Join(projectDir, ".clawker.yaml")
	localPath := filepath.Join(projectDir, ".clawker", ".clawker.local.yaml")
	require.NoError(t, os.WriteFile(mainPath, []byte("build:\n  packages: [git]\n"), 0o600))
	require.NoError(t, os.WriteFile(localPath, []byte("workspace:\n  default_mode: snapshot\n"), 0o600))
	t.Chdir(projectDir)

	cfg, err := config.NewConfig(config.WithProjectRoot(projectDir))
	require.NoError(t, err)

	// The local override layer must be in play (it wins the merge).
	assert.Equal(t, "snapshot", cfg.Project().Workspace.DefaultMode)

	targets, err := LayerTargets(cfg.ProjectStore())
	require.NoError(t, err)
	require.Len(t, targets, 3)

	assert.Equal(t, storeui.LabelProject, targets[0].Label)
	assert.Equal(t, mainPath, targets[0].Path)
	assert.Equal(t, storeui.LabelUser, targets[1].Label)
	assert.Equal(t, storeui.LabelLocal, targets[2].Label)
	assert.Equal(t, localPath, targets[2].Path)
}

// Outside a project (no walk-up anchor) the store cannot rediscover CWD
// files, so no "Project" target may be offered.
func TestLayerTargets_NoProjectRootExcludesProject(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := configmocks.NewIsolatedTestConfig(t)

	targets, err := LayerTargets(cfg.ProjectStore())
	require.NoError(t, err)
	require.NotEmpty(t, targets)

	for _, tgt := range targets {
		assert.NotEqual(t, "Project", tgt.Label,
			"project editor outside a project must not offer a Project target")
	}
	assert.Equal(t, "User", targets[0].Label)
}
