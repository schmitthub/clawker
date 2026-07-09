package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// Conformance: E21 — validation walks each layer so the error names the real file, not the merged tree.
// TestValidateProjectRegistries_FileProvenance proves the per-layer walk
// names the actual offending file, not just the virtual/seed layer.
func TestValidateProjectRegistries_FileProvenance(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, consts.ProjectConfigFile), []byte(`
stacks:
  Bad_Name:
    path: ./x
`), 0o644))

	store, err := storage.New[Project]("",
		storage.WithFilenames(consts.ProjectConfigFile),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)

	err = validateProjectRegistries(store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), consts.ProjectConfigFile)
	assert.Contains(t, err.Error(), "stacks.Bad_Name")
}

// TestValidateProjectRegistries_MalformedShapes drives every "must be a
// mapping / list / string" type-assertion branch in validate.go through the
// scenario that makes the per-layer walk load-bearing: a malformed value in
// a LOSING layer shadowed by a well-formed value in the winning layer. The
// merged tree then decodes cleanly (the winner's shape wins the merge), so
// the typed decode never sees the mistake — only the per-layer validation
// can surface it instead of silently ignoring that file's node.
func TestValidateProjectRegistries_MalformedShapes(t *testing.T) {
	// A fully valid winning layer that shadows every key the malformed
	// losing layers below misuse, so the merged tree always decodes.
	const winning = `
stacks:
  my-rust: { path: ./stacks/my-rust }
harnesses:
  codex: { path: ./tools/codex }
  claude:
    config: { strategy: copy }
build:
  stacks: [go]
  harnesses:
    claude:
      stacks: [bun]
      inject: { after_harness_install: ["echo x"] }
`

	cases := []struct {
		name    string
		losing  string
		wantErr string
	}{
		{"stacks node is a scalar", "stacks: foo\n", "stacks: must be a mapping"},
		{"stack entry is a scalar", "stacks:\n  my-rust: not-a-map\n", "stacks.my-rust: must be a mapping"},
		{
			"stack path is not a string",
			"stacks:\n  my-rust:\n    path: 123\n",
			"stacks.my-rust.path: must be a string",
		},
		{"harnesses node is a scalar", "harnesses: foo\n", "harnesses: must be a mapping"},
		{"harness entry is a scalar", "harnesses:\n  codex: nope\n", "harnesses.codex: must be a mapping"},
		{"build node is a scalar", "build: some-typo\n", "build: must be a mapping"},
		{"build.stacks is not a list", "build:\n  stacks: foo\n", "build.stacks: must be a list"},
		{"build.stacks item is not a string", "build:\n  stacks: [123]\n", "build.stacks[0]: must be a string"},
		{"build.harnesses node is a scalar", "build:\n  harnesses: foo\n", "build.harnesses: must be a mapping"},
		{
			"overlay entry is a scalar",
			"build:\n  harnesses:\n    claude: nope\n",
			"build.harnesses.claude: must be a mapping",
		},
		{
			"overlay inject is a scalar",
			"build:\n  harnesses:\n    claude:\n      inject: nope\n",
			"build.harnesses.claude.inject: must be a mapping",
		},
		{
			"harness config block is a scalar",
			"harnesses:\n  claude:\n    config: nope\n",
			"harnesses.claude.config: must be a mapping",
		},
		{
			"harness config strategy is not a string",
			"harnesses:\n  claude:\n    config:\n      strategy: 5\n",
			"harnesses.claude.config.strategy: must be a string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			winDir, loseDir := t.TempDir(), t.TempDir()
			require.NoError(
				t,
				os.WriteFile(filepath.Join(winDir, consts.ProjectConfigFile), []byte(winning), 0o644),
			)
			require.NoError(
				t,
				os.WriteFile(filepath.Join(loseDir, consts.ProjectConfigFile), []byte(tc.losing), 0o644),
			)

			// First path = highest priority: the valid layer wins the merge,
			// so store construction (typed decode of the merged tree) succeeds
			// and only the per-layer walk can flag the losing file.
			store, err := storage.New[Project]("",
				storage.WithFilenames(consts.ProjectConfigFile),
				storage.WithPaths(winDir, loseDir),
			)
			require.NoError(
				t,
				err,
				"malformed losing layer must not break store construction — that is the silent-shadow hazard",
			)

			err = validateProjectRegistries(store)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Contains(t, err.Error(), consts.ProjectConfigFile)
		})
	}
}

// TestValidateSettingsRegistries_FileProvenance proves the settings-side
// per-layer walk names the actual offending file for a bad monitoring.units
// registry name.
func TestValidateSettingsRegistries_FileProvenance(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, consts.SettingsFile), []byte(`
monitoring:
  units:
    Bad_Name:
      path: /abs/x
`), 0o644))

	store, err := storage.New[Settings]("",
		storage.WithFilenames(consts.SettingsFile),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)

	err = validateSettingsRegistries(store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), consts.SettingsFile)
	assert.Contains(t, err.Error(), "monitoring.units.Bad_Name")
}

// TestValidateSettingsRegistries_Table drives the monitoring.units front
// door: name rule, known-field rejection, absolute-path requirement,
// ~/$VAR rejection, and active type check. Malformed-shape rows pair the
// bad losing layer with a valid winning layer (mirroring
// TestValidateProjectRegistries_MalformedShapes): the winner's shape wins
// the merge so store construction succeeds and only the per-layer walk
// can surface the losing file's mistake.
func TestValidateSettingsRegistries_Table(t *testing.T) {
	const winning = `
monitoring:
  units:
    x: { path: /abs/units/x, active: true }
`
	cases := []struct {
		name    string
		yaml    string
		wantErr string // empty = valid
	}{
		{
			"valid path entry",
			"monitoring:\n  units:\n    codex-usage:\n      path: /abs/units/codex-usage\n",
			"",
		},
		{"valid flag-only entry", "monitoring:\n  units:\n    claude-code:\n      active: true\n", ""},
		{"absent node", "monitoring: {}\n", ""},
		{"units node is a scalar", "monitoring:\n  units: foo\n", "monitoring.units: must be a mapping"},
		{"entry is a scalar", "monitoring:\n  units:\n    x: nope\n", "monitoring.units.x: must be a mapping"},
		{"bad name", "monitoring:\n  units:\n    Bad_Name: {path: /a}\n", "monitoring.units.Bad_Name"},
		{
			"unknown field",
			"monitoring:\n  units:\n    x:\n      pth: /a\n",
			"monitoring.units.x.pth: unknown field",
		},
		{
			"path not a string",
			"monitoring:\n  units:\n    x:\n      path: 5\n",
			"monitoring.units.x.path: must be a string",
		},
		{
			"path empty",
			"monitoring:\n  units:\n    x:\n      path: \"\"\n",
			"monitoring.units.x.path: must not be empty",
		},
		{"path relative", "monitoring:\n  units:\n    x:\n      path: ./units/x\n", "must be an absolute path"},
		{"path tilde", "monitoring:\n  units:\n    x:\n      path: ~/units/x\n", "must not use ~"},
		{"path env var", "monitoring:\n  units:\n    x:\n      path: $HOME/units/x\n", "must not use $VAR"},
		{
			"active not a bool",
			"monitoring:\n  units:\n    x:\n      active: yes please\n",
			"monitoring.units.x.active: must be a boolean",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			winDir, loseDir := t.TempDir(), t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(winDir, consts.SettingsFile), []byte(winning), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(loseDir, consts.SettingsFile), []byte(tc.yaml), 0o644))
			store, err := storage.New[Settings]("",
				storage.WithFilenames(consts.SettingsFile),
				storage.WithPaths(winDir, loseDir),
			)
			require.NoError(t, err,
				"malformed losing layer must not break store construction — that is the silent-shadow hazard")

			err = validateSettingsRegistries(store)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Contains(t, err.Error(), consts.SettingsFile)
		})
	}
}

// TestKnownFieldSets_MatchSchemaTags guards the hand-maintained known-field
// allowlists in validate.go against drift from the schema structs' yaml
// tags: a field added to a struct without updating its allowlist would make
// every config using it fail to load with "unknown field".
func TestKnownFieldSets_MatchSchemaTags(t *testing.T) {
	cases := []struct {
		name   string
		schema reflect.Type
		known  map[string]bool
	}{
		{"stack registry entry", reflect.TypeFor[StackRegistryEntry](), knownStackRegistryFields()},
		{"harness registry entry", reflect.TypeFor[HarnessConfig](), knownHarnessRegistryFields()},
		{"harness build overlay", reflect.TypeFor[HarnessBuildOverlay](), knownHarnessOverlayFields()},
		{"harness overlay inject", reflect.TypeFor[HarnessOverlayInject](), knownHarnessOverlayInjectFields()},
		{"harness config options", reflect.TypeFor[HarnessConfigOptions](), knownHarnessConfigOptionsFields()},
		{"monitoring unit entry", reflect.TypeFor[MonitoringUnitEntry](), knownMonitoringUnitFields()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, yamlTagKeys(t, tc.schema), tc.known,
				"known-field allowlist in validate.go must match the struct's yaml tags")
		})
	}
}

// yamlTagKeys reflects the top-level yaml key set of a schema struct.
func yamlTagKeys(t *testing.T, rt reflect.Type) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	for i := range rt.NumField() {
		tag := rt.Field(i).Tag.Get("yaml")
		require.NotEmpty(t, tag, "schema field %s.%s must carry a yaml tag", rt.Name(), rt.Field(i).Name)
		name, _, _ := strings.Cut(tag, ",")
		require.NotEmpty(t, name, "schema field %s.%s yaml tag must name a key", rt.Name(), rt.Field(i).Name)
		keys[name] = true
	}
	return keys
}
