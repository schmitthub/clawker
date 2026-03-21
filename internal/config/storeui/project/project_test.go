package project

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/storeui"
	"github.com/stretchr/testify/assert"
)

func TestOverrides_AllPathsMatchProjectFields(t *testing.T) {
	fields := storeui.WalkFields(config.Project{})
	fieldPaths := make(map[string]bool, len(fields))
	for _, f := range fields {
		fieldPaths[f.Path] = true
	}

	overrides := Overrides()
	for _, ov := range overrides {
		if ov.Hidden {
			// Hidden overrides may target parent struct paths that aren't leaf fields.
			// They must still match either a leaf path or a prefix of leaf paths.
			found := false
			for path := range fieldPaths {
				if path == ov.Path || len(path) > len(ov.Path) && path[:len(ov.Path)+1] == ov.Path+"." {
					found = true
					break
				}
			}
			assert.True(t, found,
				"hidden override path %q does not match any field or prefix in config.Project", ov.Path)
			continue
		}
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

func TestOverrides_ComplexTypesHidden(t *testing.T) {
	hiddenPaths := []string{
		"build.build_args",
		"build.instructions",
		"build.inject",
		"agent.env",
		"agent.claude_code",
		"security.firewall",
		"security.cap_add",
		"security.git_credentials",
	}

	overrides := Overrides()
	overrideMap := make(map[string]*storeui.Override, len(overrides))
	for i := range overrides {
		overrideMap[overrides[i].Path] = &overrides[i]
	}

	for _, path := range hiddenPaths {
		ov, ok := overrideMap[path]
		if assert.True(t, ok, "missing override for %q", path) {
			assert.True(t, ov.Hidden, "override for %q should be hidden", path)
		}
	}
}

func TestOverrides_WorkspaceModeIsSelect(t *testing.T) {
	overrides := Overrides()
	for _, ov := range overrides {
		if ov.Path == "workspace.default_mode" {
			assert.NotNil(t, ov.Kind)
			assert.Equal(t, storeui.KindSelect, *ov.Kind)
			assert.Equal(t, []string{"bind", "snapshot"}, ov.Options)
			return
		}
	}
	t.Fatal("missing override for workspace.default_mode")
}
