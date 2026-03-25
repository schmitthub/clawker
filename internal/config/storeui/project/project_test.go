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
