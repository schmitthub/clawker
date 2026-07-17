package config_test

// External test package: testenv imports config, so the write+reload test
// cannot live in package config without an import cycle.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/testenv"
)

// TestPresets_StrictDecode decodes every preset with KnownFields enabled so
// any key the Project schema doesn't recognize fails the test. The store's
// normal load path silently accepts unknown fields, which lets mis-nested
// keys (e.g. pre_run at top level instead of under agent:) vanish without a
// trace — this is the tripwire for that class of bug.
func TestPresets_StrictDecode(t *testing.T) {
	for _, p := range config.Presets() {
		t.Run(p.Name, func(t *testing.T) {
			dec := yaml.NewDecoder(strings.NewReader(p.YAML))
			dec.KnownFields(true)
			var proj config.Project
			require.NoError(t, dec.Decode(&proj),
				"preset %q has unknown or mis-nested keys", p.Name)

			if proj.Security.Firewall != nil {
				for _, r := range proj.Security.Firewall.Rules {
					require.NoError(t, r.ValidatePortSpec(),
						"preset %q: invalid egress rule port spec", p.Name)
				}
			}
		})
	}
}

// TestPresets_WriteAndReload exercises the same path project init uses:
// NewProjectStoreFromPreset → WriteTo(.clawker.yaml) → full config load with
// walk-up discovery. It then asserts every key the preset specifies survives
// the write+reload round trip with its value intact. A preset field that
// parses but never lands in the discovered config (mis-nesting, dropped
// merge) fails here even though the lenient store load reports no error.
func TestPresets_WriteAndReload(t *testing.T) {
	for _, p := range config.Presets() {
		t.Run(p.Name, func(t *testing.T) {
			env := testenv.New(t)
			projDir := filepath.Join(env.Dirs.Base, "proj")
			require.NoError(t, os.MkdirAll(projDir, 0o755))

			store, err := config.NewProjectStoreFromPreset(p.YAML)
			require.NoError(t, err)
			require.NoError(t, store.WriteTo(filepath.Join(projDir, "."+consts.ProjectConfigFile)))

			t.Chdir(projDir)
			cfg, err := config.NewConfig(config.WithProjectRoot(projDir))
			require.NoError(t, err)

			reloaded, err := yaml.Marshal(cfg.Project())
			require.NoError(t, err)

			var want, got map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(p.YAML), &want))
			require.NoError(t, yaml.Unmarshal(reloaded, &got))
			assertYAMLSubset(t, want, got, p.Name)
		})
	}
}

// assertYAMLSubset asserts that every mapping key and sequence element in
// want is present in got with an equal value. got may contain extra keys
// (schema defaults merged at load time); want may not lose any.
func assertYAMLSubset(t *testing.T, want, got any, path string) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		require.True(t, ok, "%s: expected mapping, got %T", path, got)
		for k, wv := range w {
			gv, present := g[k]
			require.True(t, present, "%s.%s: preset key missing after write+reload", path, k)
			assertYAMLSubset(t, wv, gv, path+"."+k)
		}
	case []any:
		g, ok := got.([]any)
		require.True(t, ok, "%s: expected sequence, got %T", path, got)
		for _, wv := range w {
			require.Contains(t, g, wv, "%s: preset element missing after write+reload", path)
		}
	default:
		require.Equal(t, want, got, "%s: preset value changed after write+reload", path)
	}
}

func TestPresets_FieldAssertions(t *testing.T) {
	// Presets that have language-specific firewall domains (not Bare/C++).
	// Node's only domain (registry.npmjs.org) is in the required
	// firewall set — see internal/config/defaults.go — so the preset no
	// longer adds language-specific domains.
	presetsWithDomains := map[string]bool{
		"Python": true, "Go": true, "Rust": true,
		"Java": true, "Ruby": true, "C#/.NET": true,
	}

	for _, p := range config.Presets() {
		t.Run(p.Name, func(t *testing.T) {
			store, err := storage.New[config.Project](p.YAML,
				storage.WithDefaultsFromStruct[config.Project](),
			)
			require.NoError(t, err)

			snap := store.Read()

			assert.NotEmpty(t, snap.Build.Packages,
				"preset %q: build.packages must not be empty", p.Name)

			// ripgrep is the only package all presets add (git/curl are in
			// the Dockerfile template base and no longer listed in presets).
			assert.Contains(t, snap.Build.Packages, "ripgrep",
				"preset %q: build.packages must include ripgrep", p.Name)

			// Node users rely on dependencies being installed out of the box;
			// the Node preset must ship an npm install pre_run.
			if p.Name == "Node" {
				assert.Contains(t, snap.Agent.PreRun, "npm install",
					"preset %q: agent.pre_run must run npm install", p.Name)
			}

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
	for _, p := range config.Presets() {
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
	for _, p := range config.Presets() {
		t.Run(p.Name, func(t *testing.T) {
			store, err := storage.New[config.Project](p.YAML,
				storage.WithDefaultsFromStruct[config.Project](),
			)
			require.NoError(t, err)

			snap := store.Read()
			assert.Equal(t, "bind", snap.Workspace.DefaultMode,
				"preset %q: workspace.default_mode should be filled by schema default", p.Name)
		})
	}
}
