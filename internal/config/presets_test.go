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

			// Every top-level key the preset declares must still resolve out
			// of the reloaded store, with its value intact. Reading key by key
			// (instead of marshalling a whole-schema snapshot) is the same
			// assertion expressed against the values consumers actually read.
			var want map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(p.YAML), &want))
			for key, wantVal := range want {
				gotVal, gErr := storage.Get[any](cfg.ProjectStore(), key)
				require.NoErrorf(t, gErr, "%s: preset key %q missing after write+reload", p.Name, key)
				assertYAMLSubset(t, wantVal, gotVal, p.Name+"."+key)
			}
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
			// NewProjectStoreFromPreset is the constructor project init uses,
			// and it carries NO defaults layer — so every value asserted below
			// has to come from the preset itself. A schema default could
			// otherwise satisfy these assertions on a preset that declares
			// nothing (build.packages defaults to ripgrep).
			store, err := config.NewProjectStoreFromPreset(p.YAML)
			require.NoError(t, err)

			packages, err := storage.Get[[]string](store, "build", "packages")
			require.NoError(t, err)
			assert.NotEmpty(t, packages,
				"preset %q: build.packages must not be empty", p.Name)

			// ripgrep is the only package all presets add (git/curl are in
			// the Dockerfile template base and no longer listed in presets).
			assert.Contains(t, packages, "ripgrep",
				"preset %q: build.packages must include ripgrep", p.Name)

			// Node users rely on dependencies being installed out of the box;
			// the Node preset must ship an npm install pre_run.
			if p.Name == "Node" {
				preRun, pErr := storage.Get[string](store, "agent", "pre_run")
				require.NoError(t, pErr)
				assert.Contains(t, preRun, "npm install",
					"preset %q: agent.pre_run must run npm install", p.Name)
			}

			// VCS domains (github.com, etc.) are no longer in presets — they
			// come from the VCS wizard/flags. Only language-specific domains
			// remain (e.g., pypi.org, proxy.golang.org).
			if presetsWithDomains[p.Name] {
				domains, dErr := storage.Get[[]string](store, "security", "firewall", "add_domains")
				require.NoErrorf(t, dErr, "preset %q: security.firewall.add_domains must be set", p.Name)
				assert.NotEmpty(t, domains,
					"preset %q: should have language-specific domains", p.Name)
				assert.NotContains(t, domains, "github.com",
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
			// The defaults layer only exists on the full load path, so the gap
			// filling is asserted where a user actually sees it: the preset on
			// disk, read back through NewConfig's accessors.
			f := newProjectFixture(t)
			f.writeProject(t, p.YAML)
			cfg := f.load(t)

			assert.Equal(t, config.ModeBind, cfg.WorkspaceDefaultMode(),
				"preset %q: workspace.default_mode should be filled by schema default", p.Name)
		})
	}
}
