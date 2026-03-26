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
	// Presets that have language-specific firewall domains (not Bare/C++).
	presetsWithDomains := map[string]bool{
		"Python": true, "Go": true, "Rust": true, "TypeScript": true,
		"Java": true, "Ruby": true, "C#/.NET": true,
	}

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

			// ripgrep is the only package all presets add (git/curl are in
			// the Dockerfile template base and no longer listed in presets).
			assert.Contains(t, snap.Build.Packages, "ripgrep",
				"preset %q: build.packages must include ripgrep", p.Name)

			// VCS domains (github.com, etc.) are no longer in presets — they
			// come from the VCS wizard/flags. Only language-specific domains
			// remain (e.g., pypi.org, proxy.golang.org).
			if presetsWithDomains[p.Name] {
				require.NotNil(t, snap.Security.Firewall,
					"preset %q: security.firewall must not be nil", p.Name)
				assert.NotEmpty(t, snap.Security.Firewall.AddDomains,
					"preset %q: should have language-specific domains", p.Name)
				assert.NotContains(t, snap.Security.Firewall.AddDomains, "github.com",
					"preset %q: VCS domains should not be in presets", p.Name)
			}
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
