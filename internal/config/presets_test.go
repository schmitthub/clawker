package config

import (
	"testing"

	"github.com/schmitthub/clawker/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPresets_AllParseSuccessfully(t *testing.T) {
	for _, p := range Presets() {
		t.Run(p.Name, func(t *testing.T) {
			store, err := storage.NewFromString[Project](p.YAML,
				storage.WithDefaultsFromStruct[Project](),
			)
			require.NoError(t, err, "preset %q failed to parse", p.Name)

			snap := store.Read()
			require.NotNil(t, snap, "preset %q produced nil snapshot", p.Name)
		})
	}
}

func TestPresets_FieldAssertions(t *testing.T) {
	for _, p := range Presets() {
		t.Run(p.Name, func(t *testing.T) {
			store, err := storage.NewFromString[Project](p.YAML,
				storage.WithDefaultsFromStruct[Project](),
			)
			require.NoError(t, err)

			snap := store.Read()

			assert.NotEmpty(t, snap.Build.Image,
				"preset %q: build.image must not be empty", p.Name)
			assert.NotEmpty(t, snap.Build.Packages,
				"preset %q: build.packages must not be empty", p.Name)
			assert.Contains(t, snap.Build.Packages, "git",
				"preset %q: build.packages must include git", p.Name)
			assert.Contains(t, snap.Build.Packages, "curl",
				"preset %q: build.packages must include curl", p.Name)
			assert.Contains(t, snap.Build.Packages, "ripgrep",
				"preset %q: build.packages must include ripgrep", p.Name)

			require.NotNil(t, snap.Security.Firewall,
				"preset %q: security.firewall must not be nil", p.Name)
			assert.NotEmpty(t, snap.Security.Firewall.AddDomains,
				"preset %q: security.firewall.add_domains must not be empty", p.Name)
			assert.Contains(t, snap.Security.Firewall.AddDomains, "github.com",
				"preset %q: firewall domains must include github.com", p.Name)
			assert.Contains(t, snap.Security.Firewall.AddDomains, "api.github.com",
				"preset %q: firewall domains must include api.github.com", p.Name)
		})
	}
}

func TestPresets_AutoCustomizeContract(t *testing.T) {
	var autoCount int
	for _, p := range Presets() {
		if p.AutoCustomize {
			autoCount++
			assert.Equal(t, "Build from scratch", p.Name,
				"only Build from scratch should have AutoCustomize=true")
		}
	}
	assert.Equal(t, 1, autoCount,
		"exactly one preset should have AutoCustomize=true")
}

func TestPresets_SchemaDefaultsFillGaps(t *testing.T) {
	for _, p := range Presets() {
		t.Run(p.Name, func(t *testing.T) {
			store, err := storage.NewFromString[Project](p.YAML,
				storage.WithDefaultsFromStruct[Project](),
			)
			require.NoError(t, err)

			snap := store.Read()
			assert.Equal(t, "bind", snap.Workspace.DefaultMode,
				"preset %q: workspace.default_mode should be filled by schema default", p.Name)
		})
	}
}
