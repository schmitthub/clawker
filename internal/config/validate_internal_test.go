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
// TestValidateProjectNodes_FileProvenance proves the per-layer walk
// names the actual offending file, not just the virtual/seed layer.
func TestValidateProjectNodes_FileProvenance(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, consts.ProjectConfigFile), []byte(`
harnesses:
  Bad_Name:
    mount_projects: false
`), 0o644))

	store, err := storage.New[Project]("",
		storage.WithFilenames(consts.ProjectConfigFile),
		storage.WithPaths(dir),
	)
	require.NoError(t, err)

	err = validateProjectNodes(store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), consts.ProjectConfigFile)
	assert.Contains(t, err.Error(), "harnesses.Bad_Name")
}

// TestValidateProjectNodes_MalformedShapes drives every "must be a
// mapping / list / string" type-assertion branch in validate.go through the
// scenario that makes the per-layer walk load-bearing: a malformed value in
// a LOSING layer shadowed by a well-formed value in the winning layer. The
// merged tree then decodes cleanly (the winner's shape wins the merge), so
// the typed decode never sees the mistake — only the per-layer validation
// can surface it instead of silently ignoring that file's node.
func TestValidateProjectNodes_MalformedShapes(t *testing.T) {
	// A fully valid winning layer that shadows every key the malformed
	// losing layers below misuse, so the merged tree always decodes.
	const winning = `
harnesses:
  codex: { mount_projects: true }
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

			err = validateProjectNodes(store)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Contains(t, err.Error(), consts.ProjectConfigFile)
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
		{"harness registry entry", reflect.TypeFor[HarnessConfig](), knownHarnessConfigFields()},
		{"harness build overlay", reflect.TypeFor[HarnessBuildOverlay](), knownHarnessOverlayFields()},
		{"harness overlay inject", reflect.TypeFor[HarnessOverlayInject](), knownHarnessOverlayInjectFields()},
		{"harness config options", reflect.TypeFor[HarnessConfigOptions](), knownHarnessConfigOptionsFields()},
		{"bundle source", reflect.TypeFor[BundleSource](), knownBundleSourceFields()},
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
