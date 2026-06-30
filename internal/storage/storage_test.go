package storage

import (
	"bytes"
	"fmt"
	"maps"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- deterministic seeds for oracle/golden tests
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	treepkg "github.com/a8m/tree"
	"github.com/a8m/tree/ostree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// --- Test schema types ---

type testConfig struct {
	Name     string            `yaml:"name"`
	Version  int               `yaml:"version"`
	Build    testBuild         `yaml:"build"`
	Packages []string          `yaml:"packages" merge:"union"`
	Plugins  []string          `yaml:"plugins"  merge:"overwrite"`
	Tags     []string          `yaml:"tags"`
	Env      map[string]string `yaml:"env"`
}

func (t testConfig) Fields() FieldSet { return NormalizeFields(t) }

type testBuild struct {
	Image  string `yaml:"image"`
	Target string `yaml:"target"`
}

// Test types for merge union edge cases (promoted from local types to support Schema constraint).
type testUnionMapItem struct {
	Name string `yaml:"name"`
}

type testUnionMapCfg struct {
	Items []testUnionMapItem `yaml:"items" merge:"union"`
}

func (t testUnionMapCfg) Fields() FieldSet { return NormalizeFields(t) }

type testUnionImplicitCfg struct {
	Items []string `yaml:",omitempty" merge:"union"`
}

func (t testUnionImplicitCfg) Fields() FieldSet { return NormalizeFields(t) }

// --- Test data helpers ---

func testFullData() string {
	return `
name: myproject
version: 1
build:
  image: node:20
  target: production
packages:
  - git
  - curl
plugins:
  - eslint
  - prettier
tags:
  - stable
  - latest
env:
  APP_ENV: production
  LOG_LEVEL: info
`
}

func testPartialData() string {
	return `
name: myproject
build:
  image: node:20
packages:
  - git
`
}

func testOverrideData() string {
	return `
name: override-project
version: 2
build:
  image: alpine:3.19
packages:
  - ripgrep
plugins:
  - semgrep
tags:
  - dev
env:
  APP_ENV: development
  DEBUG: "true"
`
}

func testDefaultsData() string {
	return `
name: default
version: 0
build:
  image: ubuntu:22.04
  target: dev
packages:
  - bash
plugins:
  - base-plugin
tags:
  - default
env:
  APP_ENV: default
`
}

func testInvalidData() string {
	return `
name: [invalid
  yaml: {{broken
`
}

func TestStore_Load(t *testing.T) {
	tempDir := t.TempDir()

	fullPath := filepath.Join(tempDir, "full.yaml")
	partialPath := filepath.Join(tempDir, "partial.yaml")
	invalidPath := filepath.Join(tempDir, "invalid.yaml")
	emptyPath := filepath.Join(tempDir, "empty.yaml")

	err := os.WriteFile(fullPath, []byte(testFullData()), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(partialPath, []byte(testPartialData()), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(invalidPath, []byte(testInvalidData()), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(emptyPath, []byte(""), 0o644)
	require.NoError(t, err)

	tests := []struct {
		name         string
		path         string
		wantName     string
		wantVersion  int
		wantImage    string
		wantPackages []any
		wantErr      bool
	}{
		{
			name:         "full data loads all fields",
			path:         fullPath,
			wantName:     "myproject",
			wantVersion:  1,
			wantImage:    "node:20",
			wantPackages: []any{"git", "curl"},
		},
		{
			name:         "partial data loads specified fields",
			path:         partialPath,
			wantName:     "myproject",
			wantImage:    "node:20",
			wantPackages: []any{"git"},
		},
		{
			name:    "invalid YAML returns error",
			path:    invalidPath,
			wantErr: true,
		},
		{
			name: "empty file returns empty map",
			path: emptyPath,
		},
		{
			name:    "missing file returns error",
			path:    filepath.Join(tempDir, "nonexistent.yaml"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, loadErr := loadNode(tt.path)
			if tt.wantErr {
				assert.Error(t, loadErr)
				return
			}
			require.NoError(t, loadErr)
			result := nodeToMap(node)

			if tt.wantName != "" {
				assert.Equal(t, tt.wantName, result["name"])
			}
			if tt.wantVersion != 0 {
				assert.Equal(t, tt.wantVersion, result["version"])
			}
			if tt.wantImage != "" {
				build, ok := result["build"].(map[string]any)
				require.True(t, ok, "build should be a map")
				assert.Equal(t, tt.wantImage, build["image"])
			}
			if tt.wantPackages != nil {
				assert.Equal(t, tt.wantPackages, result["packages"])
			}
		})
	}
}

// mustLoadTestMap writes YAML data to a file and returns the raw map + path.
func mustLoadTestMap(t *testing.T, dir, name, data string) (map[string]any, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
	node, err := loadNode(path)
	require.NoError(t, err)
	return nodeToMap(node), path
}

// mustNode encodes a Go value (map/slice/scalar) into a yaml.Node, for building
// layer nodes from inline literals in tests.
func mustNode(t *testing.T, v any) *yaml.Node {
	t.Helper()
	var n yaml.Node
	require.NoError(t, n.Encode(v))
	return &n
}

// mustLoadTestNode writes YAML data to a file and returns its parsed node tree +
// path — the node-native equivalent used to construct layers in merge tests.
func mustLoadTestNode(t *testing.T, dir, name, data string) (*yaml.Node, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
	node, err := loadNode(path)
	require.NoError(t, err)
	return node, path
}

// mustReadConfig loads a YAML file and unmarshals to testConfig for assertions.
func mustReadConfig(t *testing.T, path string) *testConfig {
	t.Helper()
	node, err := loadNode(path)
	require.NoError(t, err)
	cfg, err := decodeNode[testConfig](node)
	require.NoError(t, err)
	return cfg
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

// testSchemaURL is a stand-in JSON Schema URL for WithSchemaURL header tests.
const testSchemaURL = "https://example.test/clawker.schema.json"

func testSchemaHeader() string { return "# yaml-language-server: $schema=" + testSchemaURL }

func TestStore_Merge(t *testing.T) {
	tempDir := t.TempDir()
	tags := buildTagRegistry[testConfig]()

	defaults, _ := mustLoadTestNode(t, tempDir, "defaults.yaml", testDefaultsData())
	full, fullPath := mustLoadTestNode(t, tempDir, "full.yaml", testFullData())
	override, overridePath := mustLoadTestNode(t, tempDir, "override.yaml", testOverrideData())
	partial, partialPath := mustLoadTestNode(t, tempDir, "partial.yaml", testPartialData())

	tests := []struct {
		name         string
		base         *yaml.Node
		layers       []layer
		wantName     string
		wantVersion  int
		wantImage    string
		wantTarget   string
		wantPackages []string
		wantPlugins  []string
		wantTags     []string
		wantEnv      map[string]string
		wantProv     map[string]int
	}{
		{
			name:         "no layers returns defaults",
			base:         defaults,
			wantName:     "default",
			wantVersion:  0,
			wantImage:    "ubuntu:22.04",
			wantTarget:   "dev",
			wantPackages: []string{"bash"},
			wantPlugins:  []string{"base-plugin"},
			wantTags:     []string{"default"},
			wantEnv:      map[string]string{"APP_ENV": "default"},
		},
		{
			name: "single layer overrides defaults",
			base: defaults,
			layers: []layer{
				{path: fullPath, filename: "full.yaml", node: full},
			},
			wantName:     "myproject",
			wantVersion:  1,
			wantImage:    "node:20",
			wantTarget:   "production",
			wantPackages: []string{"bash", "git", "curl"},
			wantPlugins:  []string{"eslint", "prettier"},
			wantTags:     []string{"stable", "latest"},
			wantEnv:      map[string]string{"APP_ENV": "production", "LOG_LEVEL": "info"},
		},
		{
			name: "higher priority layer wins scalars",
			base: defaults,
			layers: []layer{
				{path: overridePath, filename: "override.yaml", node: override},
				{path: fullPath, filename: "full.yaml", node: full},
			},
			wantName:    "override-project",
			wantVersion: 2,
			wantImage:   "alpine:3.19",
			wantTarget:  "production",
			// union: defaults(bash) + full(git,curl) + override(ripgrep)
			wantPackages: []string{"bash", "git", "curl", "ripgrep"},
			// overwrite: override wins
			wantPlugins: []string{"semgrep"},
			// untagged: override wins
			wantTags: []string{"dev"},
			// map overwrite: highest-priority layer replaces entire map
			wantEnv: map[string]string{"APP_ENV": "development", "DEBUG": "true"},
			wantProv: map[string]int{
				"name":         0, // from override (highest priority)
				"version":      0,
				"tags":         0,
				"plugins":      0,
				"build.target": 1, // from full (override had no target)
			},
		},
		{
			name: "nil base with single layer",
			layers: []layer{
				{path: partialPath, filename: "partial.yaml", node: partial},
			},
			wantName:     "myproject",
			wantImage:    "node:20",
			wantPackages: []string{"git"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeLayers := append([]layer{}, tt.layers...)
			if tt.base != nil {
				mergeLayers = append(mergeLayers, layer{path: "", filename: "", node: tt.base})
			}
			result, prov := merge(mergeLayers, tags)
			require.NotNil(t, result)

			// Unmarshal the merged map for typed assertions.
			cfg, err := decodeNode[testConfig](result)
			require.NoError(t, err)

			assert.Equal(t, tt.wantName, cfg.Name)
			assert.Equal(t, tt.wantVersion, cfg.Version)
			assert.Equal(t, tt.wantImage, cfg.Build.Image)
			assert.Equal(t, tt.wantTarget, cfg.Build.Target)

			if tt.wantPackages != nil {
				assert.Equal(t, tt.wantPackages, cfg.Packages)
			}
			if tt.wantPlugins != nil {
				assert.Equal(t, tt.wantPlugins, cfg.Plugins)
			}
			if tt.wantTags != nil {
				assert.Equal(t, tt.wantTags, cfg.Tags)
			}
			if tt.wantEnv != nil {
				assert.Equal(t, tt.wantEnv, cfg.Env)
			}
			for key, wantIdx := range tt.wantProv {
				assert.Equal(t, wantIdx, prov[key], "provenance for %s", key)
			}
		})
	}
}

func TestStore_Write(t *testing.T) {
	t.Run("set and write persists dirty fields to disk", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte(testFullData()), 0o644))

		store, err := NewStore[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
		require.NoError(t, err)

		require.NoError(t, store.Set("name", "updated"))
		require.NoError(t, store.Set("version", 99))
		require.NoError(t, store.Write())

		result := mustReadConfig(t, cfgPath)
		assert.Equal(t, "updated", result.Name)
		assert.Equal(t, 99, result.Version)
		assert.Equal(t, "node:20", result.Build.Image, "unchanged fields should survive")
	})

	t.Run("write is no-op when clean", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte(testFullData()), 0o644))

		store, err := NewStore[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
		require.NoError(t, err)

		// No Set — nothing dirty, write should not modify file.
		origData, _ := os.ReadFile(cfgPath)
		require.NoError(t, store.Write())
		afterData, _ := os.ReadFile(cfgPath)
		assert.Equal(t, origData, afterData, "file should not change when clean")
	})

	t.Run("write fails without paths", func(t *testing.T) {
		store, err := NewFromString[testConfig](testFullData())
		require.NoError(t, err)

		require.NoError(t, store.Set("name", "nope"))
		assert.Error(t, store.Write())
	})

	t.Run("write with lock", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte(testFullData()), 0o644))

		store, err := NewStore[testConfig](WithFilenames("config.yaml"), WithPaths(dir), WithLock())
		require.NoError(t, err)

		require.NoError(t, store.Set("name", "locked-write"))
		require.NoError(t, store.Write())

		result := mustReadConfig(t, cfgPath)
		assert.Equal(t, "locked-write", result.Name)
		assert.Equal(t, 1, result.Version, "unchanged fields should survive")
		assert.Equal(t, "node:20", result.Build.Image, "unchanged fields should survive")
	})
}

// TestStore_SchemaHeader covers the WithSchemaURL yaml-language-server header:
// stamped as the first line, never duplicated on re-write, absent when no URL is
// set, and emitted without clobbering pre-existing user comments.
func TestStore_SchemaHeader(t *testing.T) {
	newStore := func(dir, url string) *Store[testConfig] {
		opts := []Option{WithFilenames("config.yaml"), WithPaths(dir)}
		if url != "" {
			opts = append(opts, WithSchemaURL(url))
		}
		s, err := NewStore[testConfig](opts...)
		require.NoError(t, err)
		return s
	}

	t.Run("stamped as first line", func(t *testing.T) {
		dir := t.TempDir()
		s := newStore(dir, testSchemaURL)
		require.NoError(t, s.Set("name", "demo"))
		require.NoError(t, s.Write())

		got := mustReadFile(t, filepath.Join(dir, "config.yaml"))
		assert.Equal(t, testSchemaHeader(), strings.SplitN(got, "\n", 2)[0],
			"schema header must be the first line\nfile:\n%s", got)
		assert.Contains(t, got, "name: demo")
	})

	t.Run("not duplicated on re-write", func(t *testing.T) {
		dir := t.TempDir()
		s := newStore(dir, testSchemaURL)
		require.NoError(t, s.Set("name", "demo"))
		require.NoError(t, s.Write())

		// Fresh store discovers + re-reads the already-stamped file.
		s2 := newStore(dir, testSchemaURL)
		require.NoError(t, s2.Set("version", 2))
		require.NoError(t, s2.Write())

		got := mustReadFile(t, filepath.Join(dir, "config.yaml"))
		assert.Equal(t, 1, strings.Count(got, testSchemaHeader()),
			"header must appear exactly once after re-write\nfile:\n%s", got)
		assert.Equal(t, testSchemaHeader(), strings.SplitN(got, "\n", 2)[0])

		reloaded := newStore(dir, testSchemaURL).Read()
		assert.Equal(t, "demo", reloaded.Name)
		assert.Equal(t, 2, reloaded.Version)
	})

	t.Run("preserves user comments", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("name: original # keep me\n"), 0o644))

		s := newStore(dir, testSchemaURL)
		require.NoError(t, s.Set("version", 7))
		require.NoError(t, s.Write())

		got := mustReadFile(t, path)
		assert.Contains(t, got, "keep me",
			"user comment on an untouched key must survive a field-merge write\nfile:\n%s", got)
		assert.Contains(t, got, "version: 7")
		assert.Equal(t, testSchemaHeader(), strings.SplitN(got, "\n", 2)[0])
	})

	t.Run("absent when no schema URL", func(t *testing.T) {
		dir := t.TempDir()
		s := newStore(dir, "")
		require.NoError(t, s.Set("name", "demo"))
		require.NoError(t, s.Write())

		got := mustReadFile(t, filepath.Join(dir, "config.yaml"))
		assert.NotContains(t, got, "yaml-language-server",
			"no header should be written when schema URL is empty")
	})
}

// TestStore_CommentIsolationAcrossLayers is the load-bearing proof of the
// node-native engine: when a value owned by file B (by provenance) is changed
// and written, B's own comments are preserved, B gains the change, and NO
// comment from any other layer leaks into B — while the other file is left
// byte-for-byte untouched.
func TestStore_CommentIsolationAcrossLayers(t *testing.T) {
	root := t.TempDir()
	hiDir := filepath.Join(root, "hi")
	loDir := filepath.Join(root, "lo")
	require.NoError(t, os.MkdirAll(hiDir, 0o755))
	require.NoError(t, os.MkdirAll(loDir, 0o755))

	// Low-priority file B owns name + version, with its own comments.
	const baseYAML = `# file: base (low priority)
name: base-name # B name comment
version: 1 # B version comment
`
	// High-priority file A owns build.image, with its own comments.
	const localYAML = `# file: local (high priority)
build:
  image: local-img # A image comment
`
	basePath := filepath.Join(loDir, "config.yaml")
	localPath := filepath.Join(hiDir, "config.yaml")
	require.NoError(t, os.WriteFile(basePath, []byte(baseYAML), 0o644))
	require.NoError(t, os.WriteFile(localPath, []byte(localYAML), 0o644))

	localBefore := mustReadFile(t, localPath)

	// hiDir is higher priority than loDir.
	store, err := NewStore[testConfig](WithFilenames("config.yaml"), WithPaths(hiDir, loDir))
	require.NoError(t, err)

	// Sanity: version is owned by the base (low) file.
	prov, ok := store.Provenance("version")
	require.True(t, ok)
	require.Equal(t, basePath, prov.Path, "version must be provenance-owned by the base file")

	// Change a value owned by B, then write. Provenance routes it to B.
	require.NoError(t, store.Set("version", 2))
	require.NoError(t, store.Write())

	got := mustReadFile(t, basePath)

	// B keeps its own structure, head comment, and field comments.
	assert.Contains(t, got, "file: base", "B's head comment must survive")
	assert.Contains(t, got, "B name comment", "B's untouched-field comment must survive")
	assert.Contains(t, got, "B version comment", "B's comment on the CHANGED field must survive")
	assert.Contains(t, got, "version: 2", "B must reflect the new value")

	// No comment, key, or value from the other layer (A) leaked into B.
	assert.NotContains(t, got, "A image comment", "A's comment must NOT appear in B")
	assert.NotContains(t, got, "file: local", "A's head comment must NOT appear in B")
	assert.NotContains(t, got, "local-img", "A's value must NOT appear in B")

	// A (not a write target) is byte-for-byte untouched.
	assert.Equal(t, localBefore, mustReadFile(t, localPath), "the non-target file must be untouched")
}

// TestStore_AddedFieldKeepsTargetCommentsOnly proves that adding a NEW field
// that routes (by provenance walk-up) to one file does not drag another layer's
// comments along.
func TestStore_AddedFieldKeepsTargetCommentsOnly(t *testing.T) {
	root := t.TempDir()
	hiDir := filepath.Join(root, "hi")
	loDir := filepath.Join(root, "lo")
	require.NoError(t, os.MkdirAll(hiDir, 0o755))
	require.NoError(t, os.MkdirAll(loDir, 0o755))

	const baseYAML = `name: base-name # keep me
version: 1
`
	const localYAML = `build:
  image: local-img # local only
`
	basePath := filepath.Join(loDir, "config.yaml")
	localPath := filepath.Join(hiDir, "config.yaml")
	require.NoError(t, os.WriteFile(basePath, []byte(baseYAML), 0o644))
	require.NoError(t, os.WriteFile(localPath, []byte(localYAML), 0o644))

	baseBefore := mustReadFile(t, basePath)

	store, err := NewStore[testConfig](WithFilenames("config.yaml"), WithPaths(hiDir, loDir))
	require.NoError(t, err)

	// build.target is unset in both files; build.image is owned by the local
	// (high) file, so a new build.target routes to the local file (walk-up to
	// the owning layer of build.*).
	require.NoError(t, store.Set("build.target", "prod"))
	require.NoError(t, store.Write())

	got := mustReadFile(t, localPath)
	assert.Contains(t, got, "target: prod")
	assert.Contains(t, got, "local only", "local file's own comment preserved")
	assert.NotContains(t, got, "keep me", "base file's comment must not leak into local")

	// Base file untouched.
	assert.Equal(t, baseBefore, mustReadFile(t, basePath), "base file must be untouched")
}

func TestStore_WriteProvenance(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	localPath := filepath.Join(dir, "local.yaml")

	// Simulate two discovered layers with different fields.
	err := os.WriteFile(globalPath, []byte(testDefaultsData()), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(localPath, []byte(testOverrideData()), 0o644)
	require.NoError(t, err)

	globalData, err := loadNode(globalPath)
	require.NoError(t, err)
	localData, err := loadNode(localPath)
	require.NoError(t, err)

	layers := []layer{
		{path: localPath, filename: "local.yaml", node: localData},
		{path: globalPath, filename: "global.yaml", node: globalData},
	}

	tags := buildTagRegistry[testConfig]()
	basePath := filepath.Join(dir, "base.yaml")
	require.NoError(t, os.WriteFile(basePath, []byte(testPartialData()), 0o644))
	base, err := loadNode(basePath)
	require.NoError(t, err)

	tree, prov := merge(append(layers, layer{path: "", filename: "", node: base}), tags)

	// Deserialize for Set.
	value, err := decodeNode[testConfig](tree)
	require.NoError(t, err)

	store := &Store[testConfig]{
		tree:   tree,
		layers: layers,
		prov:   prov,
		tags:   tags,
		opts:   options{filenames: []string{"global.yaml", "local.yaml"}},
	}
	store.value.Store(value)

	require.NoError(t, store.Set("name", "provenance-test"))
	require.NoError(t, store.Write())

	// name came from local layer (highest priority) — verify it was written there.
	localResult := mustReadConfig(t, localPath)
	assert.Equal(t, "provenance-test", localResult.Name)

	// global layer should also be written (it owns fields routed to it).
	globalResult := mustReadConfig(t, globalPath)
	assert.NotEmpty(t, globalResult.Build.Target) // target came from defaults/global
}

func TestStore_WriteProvenance_RoutesTopLevelKeysToOwningLayer(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	localPath := filepath.Join(dir, "local.yaml")

	globalYAML := `
name: global
tags:
  - from-global
`
	localYAML := `
name: local
version: 2
`

	require.NoError(t, os.WriteFile(globalPath, []byte(globalYAML), 0o644))
	require.NoError(t, os.WriteFile(localPath, []byte(localYAML), 0o644))

	globalData, err := loadNode(globalPath)
	require.NoError(t, err)
	localData, err := loadNode(localPath)
	require.NoError(t, err)

	layers := []layer{
		{path: localPath, filename: "local.yaml", node: localData},
		{path: globalPath, filename: "global.yaml", node: globalData},
	}

	tags := buildTagRegistry[testConfig]()
	tree, prov := merge(layers, tags)

	value, err := decodeNode[testConfig](tree)
	require.NoError(t, err)

	store := &Store[testConfig]{
		tree:   tree,
		layers: layers,
		prov:   prov,
		tags:   tags,
		opts:   options{filenames: []string{"global.yaml", "local.yaml"}},
	}
	store.value.Store(value)

	require.NoError(t, store.Set("name", "local-updated"))
	require.NoError(t, store.Set("tags", []string{"global-updated"}))
	require.NoError(t, store.Write())

	localResult := mustReadConfig(t, localPath)
	globalResult := mustReadConfig(t, globalPath)

	assert.Equal(t, "local-updated", localResult.Name)
	assert.Nil(t, localResult.Tags, "tags should not be routed to local layer")

	assert.Equal(t, "global", globalResult.Name, "name should not be routed to global layer")
	assert.Equal(t, []string{"global-updated"}, globalResult.Tags)
}

// TestStore_WriteProvenance_NewMapEntryRoutesToParentLayer verifies that
// adding a new entry to a map[string]string field routes the write to the
// layer that owns the parent map, not to defaultWritePath. This is a
// regression test for new map entries falling through layerPathForKey
// because they have no individual provenance — the ancestor walk-up in
// layerPathForKey resolves them to the parent field's layer.
func TestStore_WriteProvenance_NewMapEntryRoutesToParentLayer(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "local.yaml")
	globalPath := filepath.Join(dir, "global.yaml")

	// Local layer owns the env map with one existing entry.
	localYAML := `
name: local-project
env:
  BAR: "1"
`
	// Global layer has other fields but no env.
	globalYAML := `
version: 2
build:
  image: ubuntu
`
	require.NoError(t, os.WriteFile(localPath, []byte(localYAML), 0o644))
	require.NoError(t, os.WriteFile(globalPath, []byte(globalYAML), 0o644))

	localData, err := loadNode(localPath)
	require.NoError(t, err)
	globalData, err := loadNode(globalPath)
	require.NoError(t, err)

	layers := []layer{
		{path: localPath, filename: "local.yaml", node: localData},
		{path: globalPath, filename: "global.yaml", node: globalData},
	}

	tags := buildTagRegistry[testConfig]()
	tree, prov := merge(layers, tags)

	value, err := decodeNode[testConfig](tree)
	require.NoError(t, err)

	store := &Store[testConfig]{
		tree:   tree,
		layers: layers,
		prov:   prov,
		tags:   tags,
		opts:   options{filenames: []string{"local.yaml", "global.yaml"}},
	}
	store.value.Store(value)

	// Add a NEW map entry — FOO has no provenance because it's not in any layer file.
	require.NoError(t, store.Set("env.FOO", "2"))
	require.NoError(t, store.Write())

	// FOO should be written to the local layer (which owns env), not the global layer.
	localResult := mustReadConfig(t, localPath)
	globalResult := mustReadConfig(t, globalPath)

	assert.Equal(
		t,
		"2",
		localResult.Env["FOO"],
		"new map entry should be written to the layer that owns the parent map",
	)
	assert.Equal(t, "1", localResult.Env["BAR"], "existing map entry should be preserved")
	assert.Empty(t, globalResult.Env, "new map entry should NOT be written to the global layer")
}

func TestStore_WriteFilename(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	localPath := filepath.Join(dir, "config.local.yaml")

	// Create a store with two filenames configured.
	err := os.WriteFile(configPath, []byte(testFullData()), 0o644)
	require.NoError(t, err)

	configData, err := loadNode(configPath)
	require.NoError(t, err)

	tags := buildTagRegistry[testConfig]()
	tree, prov := merge([]layer{
		{path: configPath, filename: "config.yaml", node: configData},
	}, tags)

	value, err := decodeNode[testConfig](tree)
	require.NoError(t, err)

	store := &Store[testConfig]{
		tree: tree,
		layers: []layer{
			{path: configPath, filename: "config.yaml", node: configData},
		},
		prov: prov,
		tags: tags,
		opts: options{
			filenames: []string{"config.yaml", "config.local.yaml"},
			paths:     []string{dir},
		},
	}
	store.value.Store(value)

	require.NoError(t, store.Set("name", "targeted-write"))

	// Write to explicit path — should create config.local.yaml.
	require.NoError(t, store.Write(ToPath(localPath)))

	localResult := mustReadConfig(t, localPath)
	assert.Equal(t, "targeted-write", localResult.Name)
	assert.Zero(t, localResult.Version, "only dirty fields should be written to target")
}

func TestValidateDirectories(t *testing.T) {
	t.Run("no collision with distinct dirs", func(t *testing.T) {
		base := t.TempDir()
		t.Setenv("CLAWKER_CONFIG_DIR", filepath.Join(base, "config"))
		t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
		t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))
		t.Setenv("CLAWKER_CACHE_DIR", filepath.Join(base, "cache"))

		assert.NoError(t, ValidateDirectories())
	})

	t.Run("collision config and data", func(t *testing.T) {
		base := t.TempDir()
		shared := filepath.Join(base, "shared")
		t.Setenv("CLAWKER_CONFIG_DIR", shared)
		t.Setenv("CLAWKER_DATA_DIR", shared)
		t.Setenv("CLAWKER_STATE_DIR", filepath.Join(base, "state"))
		t.Setenv("CLAWKER_CACHE_DIR", filepath.Join(base, "cache"))

		err := ValidateDirectories()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config and data")
		assert.Contains(t, err.Error(), "directory collision")
	})

	t.Run("collision state and cache", func(t *testing.T) {
		base := t.TempDir()
		shared := filepath.Join(base, "shared")
		t.Setenv("CLAWKER_CONFIG_DIR", filepath.Join(base, "config"))
		t.Setenv("CLAWKER_DATA_DIR", filepath.Join(base, "data"))
		t.Setenv("CLAWKER_STATE_DIR", shared)
		t.Setenv("CLAWKER_CACHE_DIR", shared)

		err := ValidateDirectories()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "state and cache")
	})

	t.Run("multiple collisions reported", func(t *testing.T) {
		base := t.TempDir()
		shared := filepath.Join(base, "oops")
		t.Setenv("CLAWKER_CONFIG_DIR", shared)
		t.Setenv("CLAWKER_DATA_DIR", shared)
		t.Setenv("CLAWKER_STATE_DIR", shared)
		t.Setenv("CLAWKER_CACHE_DIR", filepath.Join(base, "cache"))

		err := ValidateDirectories()
		require.Error(t, err)
		// config collides with data, then data (now the seen entry) collides with state
		assert.Contains(t, err.Error(), "config and data")
		assert.Contains(t, err.Error(), "data and state")
	})

	t.Run("no collision with XDG defaults", func(t *testing.T) {
		base := t.TempDir()
		// Clear all CLAWKER overrides, set XDG roots to same base —
		// the resolver appends different suffixes so they won't collide.
		t.Setenv("CLAWKER_CONFIG_DIR", "")
		t.Setenv("CLAWKER_DATA_DIR", "")
		t.Setenv("CLAWKER_STATE_DIR", "")
		t.Setenv("CLAWKER_CACHE_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("XDG_CACHE_HOME", "")
		t.Setenv("HOME", base)

		assert.NoError(t, ValidateDirectories())
	})
}

func TestStore_Dirs(t *testing.T) {
	tests := []struct {
		name       string
		placement  string // "flat", "flat-yml", "dir", "none"
		wantLayers int
		wantName   string
	}{
		{
			name:       "flat dotfile form",
			placement:  "flat",
			wantLayers: 1,
			wantName:   "myproject",
		},
		{
			name:       "flat dotfile .yml extension",
			placement:  "flat-yml",
			wantLayers: 1,
			wantName:   "myproject",
		},
		{
			name:       "dir form .clawker/config.yaml",
			placement:  "dir",
			wantLayers: 1,
			wantName:   "myproject",
		},
		{
			name:       "dir form .clawker/config.yml",
			placement:  "dir-yml",
			wantLayers: 1,
			wantName:   "myproject",
		},
		{
			name:       "no config file present",
			placement:  "none",
			wantLayers: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			yaml := "name: myproject\nbuild:\n  image: node:20\n"

			switch tt.placement {
			case "flat":
				require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".config.yaml"), []byte(yaml), 0o644))
			case "flat-yml":
				require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".config.yml"), []byte(yaml), 0o644))
			case "dir":
				clawkerDir := filepath.Join(projectDir, ".clawker")
				require.NoError(t, os.MkdirAll(clawkerDir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(clawkerDir, "config.yaml"), []byte(yaml), 0o644))
			case "dir-yml":
				clawkerDir := filepath.Join(projectDir, ".clawker")
				require.NoError(t, os.MkdirAll(clawkerDir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(clawkerDir, "config.yml"), []byte(yaml), 0o644))
			case "none":
				// no file
			}

			store, err := NewStore[testConfig](
				WithFilenames("config.yaml"),
				WithDirs(projectDir),
			)
			require.NoError(t, err)

			layers := store.Layers()
			assert.Len(t, layers, tt.wantLayers)

			if tt.wantName != "" {
				assert.Equal(t, tt.wantName, store.Read().Name)
			}
		})
	}
}

// TestWalkType_RecordsFieldKinds verifies that walkType populates FieldKind
// for all leaf fields in the registry, not just merge-tagged ones.
func TestWalkType_RecordsFieldKinds(t *testing.T) {
	reg := buildTagRegistry[testConfig]()

	tests := []struct {
		path string
		kind FieldKind
	}{
		{"name", KindText},
		{"version", KindInt},
		{"build.image", KindText},
		{"build.target", KindText},
		{"packages", KindStringSlice},
		{"plugins", KindStringSlice},
		{"tags", KindStringSlice},
		{"env", KindMap},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			meta, ok := reg[tt.path]
			require.True(t, ok, "path %q should be in registry", tt.path)
			assert.Equal(t, tt.kind, meta.kind, "path %q kind mismatch", tt.path)
		})
	}

	// Verify merge tags are still recorded alongside kinds.
	assert.Equal(t, "union", reg["packages"].mergeTag)
	assert.Equal(t, "overwrite", reg["plugins"].mergeTag)
	assert.Empty(t, reg["name"].mergeTag, "untagged field should have empty merge tag")
}

func TestStore_Dirs_MergePrecedence(t *testing.T) {
	// Two directories: high-priority overrides low-priority via merge order.
	highDir := t.TempDir()
	lowDir := t.TempDir()

	// Low-priority has full data.
	require.NoError(t, os.WriteFile(
		filepath.Join(lowDir, ".config.yaml"),
		[]byte(testFullData()),
		0o644,
	))

	// High-priority overrides name and version.
	require.NoError(t, os.WriteFile(
		filepath.Join(highDir, ".config.yaml"),
		[]byte("name: override\nversion: 99\n"),
		0o644,
	))

	store, err := NewStore[testConfig](
		WithFilenames("config.yaml"),
		WithDirs(highDir, lowDir),
	)
	require.NoError(t, err)

	assert.Len(t, store.Layers(), 2)

	// High-priority dir wins for scalar fields.
	assert.Equal(t, "override", store.Read().Name)
	assert.Equal(t, 99, store.Read().Version)

	// Low-priority dir provides fields not set in high-priority.
	assert.Equal(t, "node:20", store.Read().Build.Image)
}

func TestBuildTagRegistry_PointerToStruct(t *testing.T) {
	// NormalizeFields must dereference pointer types before the struct check.
	// Without this, passing *T (instead of T) silently returns an empty
	// field set — merge tags are lost and union slices fall back to overwrite.

	type inner struct {
		Items []string `yaml:"items" merge:"union" desc:"items"`
	}
	type outer struct {
		Name  string `yaml:"name"  desc:"name"`
		Inner inner  `yaml:"inner"`
	}

	// Value type — baseline.
	valFields := NormalizeFields(outer{})
	items := valFields.Get("inner.items")
	require.NotNil(t, items, "value type: inner.items must be in field set")
	assert.Equal(t, "union", items.MergeTag())
	assert.Equal(t, KindStringSlice, items.Kind())

	// Pointer type — must produce identical field set.
	ptrFields := NormalizeFields(&outer{})
	ptrItems := ptrFields.Get("inner.items")
	require.NotNil(t, ptrItems, "pointer type: inner.items must be in field set")
	assert.Equal(t, "union", ptrItems.MergeTag())
	assert.Equal(t, KindStringSlice, ptrItems.Kind())

	// Both field sets must have the same length.
	assert.Equal(t, valFields.Len(), ptrFields.Len(), "value and pointer field sets must match")
}

func TestStore_WalkUpLayerMerge(t *testing.T) {
	// Property-based walk-up test. Each run randomizes the placement matrix:
	//   - Whether each level uses dir form (.clawker/) or flat dotfile form
	//   - Which filenames (config.yaml, config.local.yaml, both, or neither) exist
	//   - Whether decoy files exist (flat dotfiles alongside .clawker/ dir — must be ignored)
	//
	// The seed is logged so failures are reproducible via: go test -run ... -seed=<N>
	//
	// Invariants asserted regardless of placement:
	//   1. Walk-up layers are CWD-first; explicit path is last
	//   2. Dir form (.clawker/) silences flat dotfiles at the same level
	//   3. First filename in WithFilenames wins at same depth
	//   4. Scalars: highest-priority discovered layer wins
	//   5. Union slices: all discovered layers contribute
	//   6. Map merge: keys accumulate, conflicts won by highest priority
	//   7. Decoy files never appear in layers
	//   8. LayerInfo.Data matches the file content without re-reading from disk
	//   9. Provenance() returns the correct layer for each winning field
	//  10. ProvenanceMap() covers all non-default fields

	seed := time.Now().UnixNano()
	t.Logf("seed=%d (reproduce with: rng := rand.New(rand.NewPCG(0, %d)))", seed, uint64(seed))
	rng := rand.New(rand.NewPCG(0, uint64(seed)))

	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	levels := []string{
		projectDir,
		filepath.Join(projectDir, "level1"),
		filepath.Join(projectDir, "level1", "level2"),
		filepath.Join(projectDir, "level1", "level2", "level3"),
	}
	levelNames := []string{"project", "level1", "level2", "level3"}
	userConfigDir := filepath.Join(root, "user", "config")
	require.NoError(t, os.MkdirAll(userConfigDir, 0o755))

	// --- Value pools ---
	imagePool := []string{"go:1.22", "node:20", "python:3", "rust:1.80", "ruby:3.3"}
	pkgPool := []string{"git", "curl", "jq", "vim", "tmux", "rg", "fd", "bat", "fzf", "htop"}
	envKeyPool := []string{"A", "B", "C", "D", "E", "F", "G", "H"}

	pickN := func(pool []string, n int) []string {
		if n > len(pool) {
			n = len(pool)
		}
		perm := rng.Perm(len(pool))
		out := make([]string, n)
		for i := range n {
			out[i] = pool[perm[i]]
		}
		sort.Strings(out)
		return out
	}

	// genContent holds the randomized values for one config file.
	type genContent struct {
		version  int
		image    string
		packages []string          // static "pkg-<level>" + random picks
		env      map[string]string // static "<LEVEL>=yes" + random keys
	}

	genLayer := func(level string) genContent {
		c := genContent{}
		if rng.IntN(2) == 0 {
			c.version = rng.IntN(999) + 1
		}
		if rng.IntN(2) == 0 {
			c.image = imagePool[rng.IntN(len(imagePool))]
		}
		if rng.IntN(5) > 0 { // 80% — exercises union merge
			c.packages = append([]string{"pkg-" + level}, pickN(pkgPool, rng.IntN(3))...)
		}
		if rng.IntN(2) == 0 { // 50% — exercises map merge
			staticKey := strings.ToUpper(level)
			c.env = map[string]string{staticKey: "yes"}
			if rng.IntN(2) == 0 {
				rk := pickN(envKeyPool, 1)[0]
				c.env[rk] = level
			}
		}
		return c
	}

	toYAML := func(level string, c genContent) string {
		var b strings.Builder
		fmt.Fprintf(&b, "name: %s\n", level)
		if c.version > 0 {
			fmt.Fprintf(&b, "version: %d\n", c.version)
		}
		if c.image != "" {
			fmt.Fprintf(&b, "build:\n  image: %s\n", c.image)
		}
		if len(c.packages) > 0 {
			b.WriteString("packages:\n")
			for _, p := range c.packages {
				fmt.Fprintf(&b, "  - %s\n", p)
			}
		}
		if len(c.env) > 0 {
			b.WriteString("env:\n")
			keys := make([]string, 0, len(c.env))
			for k := range c.env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&b, "  %s: %s\n", k, c.env[k])
			}
		}
		return b.String()
	}

	// --- Randomize placement per level ---
	type levelPlacement struct {
		name       string
		dir        string
		useDirForm bool
		hasMain    bool
		hasLocal   bool
		hasDecoy   bool
		mainGen    genContent
		localGen   genContent
	}

	placements := make([]levelPlacement, len(levels))
	for i, dir := range levels {
		isDeepest := i == len(levels)-1
		p := levelPlacement{
			name:       levelNames[i],
			dir:        dir,
			useDirForm: rng.IntN(2) == 0,
			hasMain:    rng.IntN(3) > 0, // 2/3 chance
			hasLocal:   rng.IntN(3) > 0, // 2/3 chance
		}
		if isDeepest {
			// Deepest level must have both files to exercise filename priority.
			p.hasMain = true
			p.hasLocal = true
		} else if !p.hasMain && !p.hasLocal {
			p.hasMain = true
		}
		if p.useDirForm {
			p.hasDecoy = rng.IntN(2) == 0
		}
		if p.hasMain {
			p.mainGen = genLayer(p.name)
		}
		if p.hasLocal {
			p.localGen = genLayer(p.name)
		}
		placements[i] = p
	}

	// --- Create files ---
	writeFile := func(path, content string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	// Map each file path back to (level, type, placement) for table rendering.
	type fileTag struct {
		level, kind, place string // kind: "main"/"local"/"-", place: "root"/"dir"/"-"
	}
	pathTag := make(map[string]fileTag)

	var wantIgnored []string

	for _, p := range placements {
		require.NoError(t, os.MkdirAll(p.dir, 0o755))

		if p.useDirForm {
			clawkerDir := filepath.Join(p.dir, ".clawker")
			require.NoError(t, os.MkdirAll(clawkerDir, 0o755))

			if p.hasMain {
				path := filepath.Join(clawkerDir, "config.yaml")
				writeFile(path, toYAML(p.name+"-main", p.mainGen))
				pathTag[path] = fileTag{p.name, "main", "dir"}
			}
			if p.hasLocal {
				path := filepath.Join(clawkerDir, "config.local.yaml")
				writeFile(path, toYAML(p.name+"-local", p.localGen))
				pathTag[path] = fileTag{p.name, "local", "dir"}
			}
			if p.hasDecoy {
				decoyPath := filepath.Join(p.dir, ".config.yaml")
				writeFile(decoyPath, "name: DECOY\npackages:\n  - DECOY\n")
				wantIgnored = append(wantIgnored, decoyPath)
			}
		} else {
			if p.hasMain {
				path := filepath.Join(p.dir, ".config.yaml")
				writeFile(path, toYAML(p.name+"-main", p.mainGen))
				pathTag[path] = fileTag{p.name, "main", "root"}
			}
			if p.hasLocal {
				path := filepath.Join(p.dir, ".config.local.yaml")
				writeFile(path, toYAML(p.name+"-local", p.localGen))
				pathTag[path] = fileTag{p.name, "local", "root"}
			}
		}
	}

	// User-level config (explicit path, lowest priority).
	userPath := filepath.Join(userConfigDir, "config.yaml")
	writeFile(
		userPath,
		"name: user\nversion: 1\nbuild:\n  image: ubuntu\npackages:\n  - pkg-user\nenv:\n  EDITOR: vim\n",
	)
	pathTag[userPath] = fileTag{"user", "-", "-"}

	// --- Print actual filesystem tree so ai agents can't make a forgery (a8m/tree reads the real FS) ---
	{
		var buf bytes.Buffer
		tr := treepkg.New(root)
		//nolint:exhaustruct // a8m/tree.Options has 30 optional fields; only these apply
		opts := &treepkg.Options{Fs: new(ostree.FS), OutFile: &buf, All: true}
		tr.Visit(opts)
		tr.Print(opts)
		t.Logf("\n=== TREE ===\n%s", buf.String())
	}

	// --- Discover ---
	// Walk-up is generic: storage walks from CWD up to the anchor it's handed
	// and holds no project-registry knowledge. Anchor at the project root.
	t.Chdir(levels[len(levels)-1]) // CWD = deepest level

	store, err := NewStore[testConfig](
		WithFilenames("config.local.yaml", "config.yaml"),
		WithWalkUp(projectDir),
		WithPaths(userConfigDir),
	)
	require.NoError(t, err)

	// --- Print layers table using LayerInfo.Data (no re-reading from disk) ---
	layers := store.Layers()
	cfg := store.Read()
	provMap := store.ProvenanceMap()

	// Table helpers.
	cell := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	listCell := func(ss []string) string {
		if len(ss) == 0 {
			return "-"
		}
		joined := strings.Join(ss, ",")
		if len(joined) > 28 {
			return joined[:25] + "..."
		}
		return joined
	}
	envCell := func(env map[string]string) string {
		if len(env) == 0 {
			return "-"
		}
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pairs := make([]string, len(keys))
		for i, k := range keys {
			pairs[i] = k + "=" + env[k]
		}
		joined := strings.Join(pairs, ",")
		if len(joined) > 30 {
			return joined[:27] + "..."
		}
		return joined
	}
	dataStr := func(data map[string]any, key string) string {
		if v, ok := data[key]; ok {
			return fmt.Sprintf("%v", v)
		}
		return ""
	}
	dataSlice := func(data map[string]any, key string) []string {
		v, ok := data[key]
		if !ok {
			return nil
		}
		sl, ok := v.([]any)
		if !ok {
			return nil
		}
		out := make([]string, len(sl))
		for i, s := range sl {
			out[i] = fmt.Sprintf("%v", s)
		}
		return out
	}
	dataMap := func(data map[string]any, key string) map[string]string {
		v, ok := data[key]
		if !ok {
			return nil
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		out := make(map[string]string, len(m))
		for k, val := range m {
			out[k] = fmt.Sprintf("%v", val)
		}
		return out
	}
	dataImage := func(data map[string]any) string {
		bld, ok := data["build"].(map[string]any)
		if !ok {
			return ""
		}
		if img, ok := bld["image"]; ok {
			return fmt.Sprintf("%v", img)
		}
		return ""
	}
	shortenPath := func(p string) string {
		if rel, err := filepath.Rel(root, p); err == nil {
			return rel
		}
		return p
	}

	// Build table rows from discovered layers using LayerInfo.Data.
	type tableRow struct {
		level, kind, place string
		filePath           string
		ver, image         string
		pkgs, env          string
	}

	// Rows in reverse discovery order (lowest priority first).
	var rows []tableRow
	for i := len(layers) - 1; i >= 0; i-- {
		l := layers[i]
		tag := pathTag[l.Path]
		d := l.Data

		ver := cell(dataStr(d, "version"))
		if ver == "0" {
			ver = "-"
		}

		rows = append(rows, tableRow{
			level:    tag.level,
			kind:     tag.kind,
			place:    tag.place,
			filePath: shortenPath(l.Path),
			ver:      ver,
			image:    cell(dataImage(d)),
			pkgs:     listCell(dataSlice(d, "packages")),
			env:      envCell(dataMap(d, "env")),
		})
	}

	// Merged row.
	rows = append(rows, tableRow{
		level: "MERGED",
		ver:   fmt.Sprintf("%d", cfg.Version),
		image: cell(cfg.Build.Image),
		pkgs:  listCell(cfg.Packages),
		env:   envCell(cfg.Env),
	})

	// Compute column widths.
	const cols = 8
	colW := [cols]int{}
	headers := [cols]string{"LAYER", "TYPE", "PLACE", "FILE", "VER(scalar)", "IMAGE(scalar)", "PKGS(union)", "ENV(map)"}
	for i, h := range headers {
		colW[i] = len(h)
	}
	for _, r := range rows {
		vals := [cols]string{r.level, r.kind, r.place, r.filePath, r.ver, r.image, r.pkgs, r.env}
		for c, v := range vals {
			if len(v) > colW[c] {
				colW[c] = len(v)
			}
		}
	}

	fmtRow := func(vals [cols]string) string {
		return fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s",
			colW[0], vals[0], colW[1], vals[1], colW[2], vals[2],
			colW[3], vals[3], colW[4], vals[4], colW[5], vals[5],
			colW[6], vals[6], vals[7])
	}

	t.Logf("\n=== LAYERS (from LayerInfo.Data — no disk re-read) ===")
	t.Log(fmtRow(headers))
	sepLen := 0
	for _, w := range colW {
		sepLen += w + 2
	}
	for j, r := range rows {
		if j == len(rows)-1 {
			t.Logf("  %s", strings.Repeat("-", sepLen))

			var pkgLines, envLines []string
			for _, p := range cfg.Packages {
				pkgLines = append(pkgLines, p)
			}
			envKeys := make([]string, 0, len(cfg.Env))
			for k := range cfg.Env {
				envKeys = append(envKeys, k)
			}
			sort.Strings(envKeys)
			for _, k := range envKeys {
				envLines = append(envLines, k+"="+cfg.Env[k])
			}

			firstPkg, firstEnv := "-", "-"
			if len(pkgLines) > 0 {
				firstPkg = pkgLines[0]
			}
			if len(envLines) > 0 {
				firstEnv = envLines[0]
			}
			t.Log(fmtRow([cols]string{r.level, r.kind, r.place, r.filePath, r.ver, r.image, firstPkg, firstEnv}))

			pad := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s",
				colW[0], "", colW[1], "", colW[2], "", colW[3], "", colW[4], "", colW[5], "")
			maxLines := len(pkgLines)
			if len(envLines) > maxLines {
				maxLines = len(envLines)
			}
			for k := 1; k < maxLines; k++ {
				pk, ev := "", ""
				if k < len(pkgLines) {
					pk = pkgLines[k]
				}
				if k < len(envLines) {
					ev = envLines[k]
				}
				t.Logf("%s  %-*s  %s", pad, colW[6], pk, ev)
			}
		} else {
			t.Log(fmtRow([cols]string{r.level, r.kind, r.place, r.filePath, r.ver, r.image, r.pkgs, r.env}))
		}
	}

	// --- Print provenance table ---
	t.Logf("\n=== PROVENANCE (field → source file) ===")
	provKeys := make([]string, 0, len(provMap))
	for k := range provMap {
		provKeys = append(provKeys, k)
	}
	sort.Strings(provKeys)
	maxKeyLen := 0
	for _, k := range provKeys {
		if len(k) > maxKeyLen {
			maxKeyLen = len(k)
		}
	}
	for _, k := range provKeys {
		t.Logf("  %-*s  ← %s", maxKeyLen, k, shortenPath(provMap[k]))
	}

	// --- Invariant: LayerInfo.Data matches disk content ---
	for _, l := range layers {
		raw, err := os.ReadFile(l.Path)
		require.NoError(t, err, "reading layer file %s", l.Path)
		var diskData map[string]any
		require.NoError(t, yaml.Unmarshal(raw, &diskData), "parsing %s", l.Path)
		assert.True(t, reflect.DeepEqual(l.Data, diskData),
			"LayerInfo.Data must match disk content for %s", shortenPath(l.Path))
	}

	// --- Invariant: Provenance() returns correct layer for known fields ---
	for field, sourcePath := range provMap {
		li, ok := store.Provenance(field)
		assert.True(t, ok, "Provenance(%q) should return true", field)
		assert.Equal(t, sourcePath, li.Path,
			"Provenance(%q) path mismatch", field)
	}

	// --- Invariant: ProvenanceMap is non-empty for stores with layers ---
	if len(layers) > 0 {
		assert.NotEmpty(t, provMap, "ProvenanceMap should be non-empty when layers exist")
	}

	// --- Invariant: decoy files never appear in layers ---
	layerPaths := make(map[string]bool, len(layers))
	for _, l := range layers {
		layerPaths[l.Path] = true
	}
	for _, ignored := range wantIgnored {
		assert.False(t, layerPaths[ignored],
			"decoy file must not be discovered: %s", ignored)
	}
	assert.NotContains(t, cfg.Name, "DECOY", "decoy name must not win merge")
	for _, pkg := range cfg.Packages {
		assert.NotContains(t, pkg, "DECOY", "decoy package must not appear in union")
	}

	// --- Invariant: explicit user path is last layer ---
	assert.Equal(t, userPath, layers[len(layers)-1].Path,
		"explicit user config is always lowest priority")

	// --- Invariant: walk-up layers are CWD-first ---
	walkUpDir := func(layerPath string) string {
		dir := filepath.Dir(layerPath)
		if filepath.Base(dir) == ".clawker" {
			dir = filepath.Dir(dir)
		}
		return dir
	}
	for i := 0; i < len(layers)-2; i++ {
		thisDir := walkUpDir(layers[i].Path)
		nextDir := walkUpDir(layers[i+1].Path)
		if thisDir != nextDir {
			thisRel, _ := filepath.Rel(root, thisDir)
			nextRel, _ := filepath.Rel(root, nextDir)
			thisDepth := strings.Count(thisRel, string(filepath.Separator))
			nextDepth := strings.Count(nextRel, string(filepath.Separator))
			if thisDepth < nextDepth {
				t.Errorf("walk-up ordering violated: layer[%d] (dir=%s) is shallower than layer[%d] (dir=%s)",
					i, thisRel, i+1, nextRel)
			}
		}
	}

	// --- Oracle: compute expected merge from spec, independent of prod code ---
	//
	// Spec rules encoded:
	//   - Depth:    deeper walk-up level = higher priority
	//   - Filename: first in WithFilenames = higher priority at same depth
	//              (test calls WithFilenames("config.local.yaml", "config.yaml")
	//               → local wins over main at same depth)
	//   - Scalars:  last writer wins (iterate low→high, overwrite)
	//   - Union:    accumulate unique, preserving insertion order
	//   - Map:      overwrite — highest-priority layer replaces entire map
	type oracleResult struct {
		name     string
		version  int
		image    string
		packages []string
		env      map[string]string
	}

	applyOracle := func(o *oracleResult, gen genContent, name string) {
		o.name = name // always set by toYAML
		if gen.version > 0 {
			o.version = gen.version
		}
		if gen.image != "" {
			o.image = gen.image
		}
		for _, pkg := range gen.packages {
			if !slices.Contains(o.packages, pkg) {
				o.packages = append(o.packages, pkg)
			}
		}
		if gen.env != nil {
			o.env = make(map[string]string, len(gen.env))
			maps.Copy(o.env, gen.env)
		}
	}

	// Start with user config (lowest priority — explicit path layer).
	oracle := oracleResult{
		name:     "user",
		version:  1,
		image:    "ubuntu",
		packages: []string{"pkg-user"},
		env:      map[string]string{"EDITOR": "vim"},
	}

	// Apply walk-up layers: shallowest → deepest, main → local at each level.
	// main applied first (lower priority), local applied second (overwrites).
	for _, p := range placements {
		if p.hasMain {
			applyOracle(&oracle, p.mainGen, p.name+"-main")
		}
		if p.hasLocal {
			applyOracle(&oracle, p.localGen, p.name+"-local")
		}
	}

	// Print oracle expectation for debugging.
	t.Logf("\n=== ORACLE (expected) ===")
	t.Logf("  name:     %s", oracle.name)
	t.Logf("  version:  %d", oracle.version)
	t.Logf("  image:    %s", oracle.image)
	t.Logf("  packages: %v", oracle.packages)
	oracleEnvKeys := make([]string, 0, len(oracle.env))
	for k := range oracle.env {
		oracleEnvKeys = append(oracleEnvKeys, k)
	}
	sort.Strings(oracleEnvKeys)
	for _, k := range oracleEnvKeys {
		t.Logf("    %s=%s", k, oracle.env[k])
	}

	// --- Assert: prod merge matches oracle ---
	assert.Equal(t, oracle.name, cfg.Name, "oracle: scalar name")
	assert.Equal(t, oracle.version, cfg.Version, "oracle: scalar version")
	assert.Equal(t, oracle.image, cfg.Build.Image, "oracle: scalar image")
	assert.Equal(t, oracle.packages, cfg.Packages, "oracle: union packages (ordered)")
	assert.Equal(t, oracle.env, cfg.Env, "oracle: map env")
}

// TestStore_WalkUpGolden is a fixed-seed regression guard for the walk-up
// merge. It uses hardcoded golden values captured from a known-correct state.
//
// The golden values are struct literals in this source file — there is NO
// auto-update mechanism. To re-bless after a legitimate behavior change:
//
//	make storage-golden
//
// That command prints the current merge result for manual review. The
// developer must then hand-edit the golden values below and commit.
func TestStore_WalkUpGolden(t *testing.T) {
	const goldenSeed uint64 = 42

	rng := rand.New(rand.NewPCG(0, goldenSeed))

	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	levels := []string{
		projectDir,
		filepath.Join(projectDir, "level1"),
		filepath.Join(projectDir, "level1", "level2"),
		filepath.Join(projectDir, "level1", "level2", "level3"),
	}
	levelNames := []string{"project", "level1", "level2", "level3"}
	userConfigDir := filepath.Join(root, "user", "config")
	require.NoError(t, os.MkdirAll(userConfigDir, 0o755))

	// --- Value pools (must match randomized test exactly) ---
	imagePool := []string{"go:1.22", "node:20", "python:3", "rust:1.80", "ruby:3.3"}
	pkgPool := []string{"git", "curl", "jq", "vim", "tmux", "rg", "fd", "bat", "fzf", "htop"}
	envKeyPool := []string{"A", "B", "C", "D", "E", "F", "G", "H"}

	pickN := func(pool []string, n int) []string {
		if n > len(pool) {
			n = len(pool)
		}
		perm := rng.Perm(len(pool))
		out := make([]string, n)
		for i := range n {
			out[i] = pool[perm[i]]
		}
		sort.Strings(out)
		return out
	}

	type genContent struct {
		version  int
		image    string
		packages []string
		env      map[string]string
	}

	genLayer := func(level string) genContent {
		c := genContent{}
		if rng.IntN(2) == 0 {
			c.version = rng.IntN(999) + 1
		}
		if rng.IntN(2) == 0 {
			c.image = imagePool[rng.IntN(len(imagePool))]
		}
		if rng.IntN(5) > 0 {
			c.packages = append([]string{"pkg-" + level}, pickN(pkgPool, rng.IntN(3))...)
		}
		if rng.IntN(2) == 0 {
			staticKey := strings.ToUpper(level)
			c.env = map[string]string{staticKey: "yes"}
			if rng.IntN(2) == 0 {
				rk := pickN(envKeyPool, 1)[0]
				c.env[rk] = level
			}
		}
		return c
	}

	toYAML := func(name string, c genContent) string {
		var b strings.Builder
		fmt.Fprintf(&b, "name: %s\n", name)
		if c.version > 0 {
			fmt.Fprintf(&b, "version: %d\n", c.version)
		}
		if c.image != "" {
			fmt.Fprintf(&b, "build:\n  image: %s\n", c.image)
		}
		if len(c.packages) > 0 {
			b.WriteString("packages:\n")
			for _, p := range c.packages {
				fmt.Fprintf(&b, "  - %s\n", p)
			}
		}
		if len(c.env) > 0 {
			b.WriteString("env:\n")
			keys := make([]string, 0, len(c.env))
			for k := range c.env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&b, "  %s: %s\n", k, c.env[k])
			}
		}
		return b.String()
	}

	type levelPlacement struct {
		name       string
		dir        string
		useDirForm bool
		hasMain    bool
		hasLocal   bool
		hasDecoy   bool
		mainGen    genContent
		localGen   genContent
	}

	placements := make([]levelPlacement, len(levels))
	for i, dir := range levels {
		isDeepest := i == len(levels)-1
		p := levelPlacement{
			name:       levelNames[i],
			dir:        dir,
			useDirForm: rng.IntN(2) == 0,
			hasMain:    rng.IntN(3) > 0,
			hasLocal:   rng.IntN(3) > 0,
		}
		if isDeepest {
			p.hasMain = true
			p.hasLocal = true
		} else if !p.hasMain && !p.hasLocal {
			p.hasMain = true
		}
		if p.useDirForm {
			p.hasDecoy = rng.IntN(2) == 0
		}
		if p.hasMain {
			p.mainGen = genLayer(p.name)
		}
		if p.hasLocal {
			p.localGen = genLayer(p.name)
		}
		placements[i] = p
	}

	writeFile := func(path, content string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	for _, p := range placements {
		require.NoError(t, os.MkdirAll(p.dir, 0o755))
		if p.useDirForm {
			clawkerDir := filepath.Join(p.dir, ".clawker")
			require.NoError(t, os.MkdirAll(clawkerDir, 0o755))
			if p.hasMain {
				writeFile(filepath.Join(clawkerDir, "config.yaml"), toYAML(p.name+"-main", p.mainGen))
			}
			if p.hasLocal {
				writeFile(filepath.Join(clawkerDir, "config.local.yaml"), toYAML(p.name+"-local", p.localGen))
			}
			if p.hasDecoy {
				writeFile(filepath.Join(p.dir, ".config.yaml"), "name: DECOY\npackages:\n  - DECOY\n")
			}
		} else {
			if p.hasMain {
				writeFile(filepath.Join(p.dir, ".config.yaml"), toYAML(p.name+"-main", p.mainGen))
			}
			if p.hasLocal {
				writeFile(filepath.Join(p.dir, ".config.local.yaml"), toYAML(p.name+"-local", p.localGen))
			}
		}
	}

	userPath := filepath.Join(userConfigDir, "config.yaml")
	writeFile(
		userPath,
		"name: user\nversion: 1\nbuild:\n  image: ubuntu\npackages:\n  - pkg-user\nenv:\n  EDITOR: vim\n",
	)

	t.Chdir(levels[len(levels)-1])

	store, err := NewStore[testConfig](
		WithFilenames("config.local.yaml", "config.yaml"),
		WithWalkUp(projectDir),
		WithPaths(userConfigDir),
	)
	require.NoError(t, err)
	cfg := store.Read()

	// --- Bless mode: print current values for manual review ---
	if os.Getenv("STORAGE_GOLDEN_BLESS") != "" {
		t.Logf("=== GOLDEN BLESS (seed=%d) ===", goldenSeed)
		t.Logf("Name:     %q", cfg.Name)
		t.Logf("Version:  %d", cfg.Version)
		t.Logf("Image:    %q", cfg.Build.Image)
		t.Logf("Packages: %#v", cfg.Packages)
		envKeys := make([]string, 0, len(cfg.Env))
		for k := range cfg.Env {
			envKeys = append(envKeys, k)
		}
		sort.Strings(envKeys)
		t.Logf("Env:")
		for _, k := range envKeys {
			t.Logf("  %q: %q,", k, cfg.Env[k])
		}
		t.Skip("STORAGE_GOLDEN_BLESS: values printed above — hand-edit golden and commit")
	}

	// --- Golden: hardcoded values from seed=42, blessed at known-correct state ---
	// To update: make storage-golden
	goldenName := "level3-local"
	goldenVersion := 710
	goldenImage := "go:1.22"
	goldenPackages := []string{"pkg-user", "pkg-project", "git", "vim", "pkg-level1", "pkg-level2", "jq", "pkg-level3"}
	// Map overwrite: highest-priority layer with env wins entirely.
	// With seed 42, level1 main is the highest-priority layer that has
	// an env section (deeper walk-up = higher priority).
	goldenEnv := map[string]string{
		"F":      "level1",
		"LEVEL1": "yes",
	}

	assert.Equal(t, goldenName, cfg.Name, "golden: name")
	assert.Equal(t, goldenVersion, cfg.Version, "golden: version")
	assert.Equal(t, goldenImage, cfg.Build.Image, "golden: image")
	assert.Equal(t, goldenPackages, cfg.Packages, "golden: packages")
	assert.Equal(t, goldenEnv, cfg.Env, "golden: env")
}

func TestStore_WalkUpAnchorGuard(t *testing.T) {
	// Walk-up is bounded by a caller-supplied anchor that must be CWD or an
	// ancestor of it. A non-ancestor anchor is a caller programming error and
	// fails store construction with ErrAnchorNotAncestor; an empty anchor
	// disables walk-up entirely (the supported "no walk-up" case).
	//
	// Layout (CWD = root/a/b; every level on the CWD→root spine holds a flat
	// dotfile config so the probed range is observable through Layers()):
	//
	//	root/.config.yaml
	//	root/a/.config.yaml
	//	root/a/b/.config.yaml   ← CWD
	//	root/a/b/c/             (descendant, no config)
	//	root/sib/               (sibling branch, no config)
	root := t.TempDir()
	level1 := filepath.Join(root, "a")
	cwd := filepath.Join(level1, "b")
	descendant := filepath.Join(cwd, "c")
	sibling := filepath.Join(root, "sib")
	for _, dir := range []string{descendant, sibling} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	for _, dir := range []string{root, level1, cwd} {
		path := filepath.Join(dir, ".config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("name: "+filepath.Base(dir)+"\n"), 0o644))
	}

	t.Chdir(cwd)

	tests := []struct {
		name      string
		anchor    string
		wantPaths []string // discovered layer paths, highest priority (CWD) first
		wantErr   bool
	}{
		{
			name:      "anchor equals CWD probes exactly CWD",
			anchor:    cwd,
			wantPaths: []string{filepath.Join(cwd, ".config.yaml")},
		},
		{
			name:   "anchor one level up stops at anchor, file above excluded",
			anchor: level1,
			wantPaths: []string{
				filepath.Join(cwd, ".config.yaml"),
				filepath.Join(level1, ".config.yaml"),
			},
		},
		{
			name:   "anchor two levels up includes every level down to CWD",
			anchor: root,
			wantPaths: []string{
				filepath.Join(cwd, ".config.yaml"),
				filepath.Join(level1, ".config.yaml"),
				filepath.Join(root, ".config.yaml"),
			},
		},
		{
			// The guard is pure path math (filepath.Rel never stats), so a
			// nonexistent anchor fails identically to this sibling case.
			name:    "sibling of CWD is not an ancestor",
			anchor:  sibling,
			wantErr: true,
		},
		{
			name:    "descendant of CWD is not an ancestor",
			anchor:  descendant,
			wantErr: true,
		},
		{
			// filepath.Rel cannot relate a relative anchor to the absolute
			// CWD, so a relative anchor is refused like any non-ancestor.
			name:    "relative anchor is refused",
			anchor:  "a",
			wantErr: true,
		},
		{
			name:      "empty anchor disables walk-up without error",
			anchor:    "",
			wantPaths: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewStore[testConfig](
				WithFilenames("config.yaml"),
				WithWalkUp(tc.anchor),
			)
			if tc.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrAnchorNotAncestor)
				return
			}
			require.NoError(t, err)
			var got []string
			for _, l := range store.Layers() {
				got = append(got, l.Path)
			}
			assert.Equal(t, tc.wantPaths, got)
		})
	}
}

func TestStore_Dirs_DedupWithPaths(t *testing.T) {
	// If the same directory is passed to both WithDirs and WithPaths,
	// WithDirs (dual placement) discovers the dotfile form while WithPaths
	// (explicit) probes the plain filename. Dedup ensures no double-loading.
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".config.yaml"),
		[]byte("name: from-dotfile\n"),
		0o644,
	))

	store, err := NewStore[testConfig](
		WithFilenames("config.yaml"),
		WithDirs(dir),
		WithPaths(dir),
	)
	require.NoError(t, err)

	// Only one layer — the dotfile discovered by WithDirs.
	// WithPaths probes dir/config.yaml (plain form) which doesn't exist.
	assert.Len(t, store.Layers(), 1)
	assert.Equal(t, "from-dotfile", store.Read().Name)
}

func TestStore_MutationWithoutSet(t *testing.T) {
	// Two callers Read() the snapshot, mutate it directly (bypassing Set),
	// then call Write(). Because Set() was never called, no new dirty paths
	// are added — Write() does not persist the direct mutations.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(testFullData()), 0o644))

	store, err := NewStore[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
	require.NoError(t, err)

	// Writer A reads snapshot, mutates directly.
	snapA := store.Read()
	snapA.Name = "writer-A"
	snapA.Version = 100
	err = store.Write()
	require.NoError(t, err)

	// Writer B reads snapshot (same pointer), mutates directly.
	snapB := store.Read()
	snapB.Name = "writer-B"
	snapB.Build.Image = "alpine:latest"
	err = store.Write()
	require.NoError(t, err)

	// The canonical tree is completely untouched — direct mutations
	// bypass Set and are never recorded as dirty.
	tree, treeErr := decodeNode[testConfig](store.tree)
	require.NoError(t, treeErr)
	assert.Equal(t, "myproject", tree.Name, "tree retains original name")
	assert.Equal(t, 1, tree.Version, "tree retains original version")
	assert.Equal(t, "node:20", tree.Build.Image, "tree retains original image")

	t.Log("Direct mutation: snapshot dirty in-memory but tree + disk untouched.")
}

func TestStore_MutationWithSet(t *testing.T) {
	t.Run("two Sets on different fields — both survive", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFromString[testConfig](testFullData(), WithFilenames("config.yaml"), WithPaths(dir))
		require.NoError(t, err)

		// Caller A sets name.
		require.NoError(t, store.Set("name", "set-by-A"))

		// Caller B sets version.
		require.NoError(t, store.Set("version", 999))

		cfg := store.Read()
		assert.Equal(t, "set-by-A", cfg.Name, "name from caller A")
		assert.Equal(t, 999, cfg.Version, "version from caller B")
		assert.Equal(t, "node:20", cfg.Build.Image, "untouched field preserved")

		// Write and verify on disk.
		require.NoError(t, store.Write())
		disk := mustReadConfig(t, filepath.Join(dir, "config.yaml"))
		assert.Equal(t, "set-by-A", disk.Name)
		assert.Equal(t, 999, disk.Version)

		t.Logf("Both mutations survived: Name=%q Version=%d", cfg.Name, cfg.Version)
	})

	t.Run("two Sets on same field — second wins", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFromString[testConfig](testFullData(), WithFilenames("config.yaml"), WithPaths(dir))
		require.NoError(t, err)

		require.NoError(t, store.Set("name", "writer-A"))
		require.NoError(t, store.Set("name", "writer-B"))

		cfg := store.Read()
		assert.Equal(t, "writer-B", cfg.Name, "second Set wins")
		assert.Equal(t, 1, cfg.Version)
		assert.Equal(t, "node:20", cfg.Build.Image)

		// Verify disk round-trip.
		require.NoError(t, store.Write())
		disk := mustReadConfig(t, filepath.Join(dir, "config.yaml"))
		assert.Equal(t, "writer-B", disk.Name, "disk matches second Set")

		t.Logf("Same-field result: winner=%q (second Set wins deterministically)", cfg.Name)
	})

	t.Run("snapshot isolation — held Read unaffected by Set", func(t *testing.T) {
		store, err := NewFromString[testConfig](testFullData())
		require.NoError(t, err)

		before := store.Read()
		assert.Equal(t, "myproject", before.Name)

		require.NoError(t, store.Set("name", "mutated"))
		require.NoError(t, store.Set("version", 42))

		// Held snapshot is still the old value.
		assert.Equal(t, "myproject", before.Name, "held snapshot is immutable")
		assert.Equal(t, 1, before.Version, "held snapshot is immutable")

		// Fresh Read() sees the new value.
		after := store.Read()
		assert.Equal(t, "mutated", after.Name)
		assert.Equal(t, 42, after.Version)

		t.Log("Snapshot isolation: old Read() unaffected by Set()")
	})
}

func TestStore_Set_ClearMapPersistsEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Write full config to file first so the store has a real layer.
	require.NoError(t, os.WriteFile(cfgPath, []byte(testFullData()), 0o644))

	store, err := NewStore[testConfig](
		WithFilenames("config.yaml"),
		WithPaths(dir),
	)
	require.NoError(t, err)
	require.NotEmpty(t, store.Read().Env, "precondition: env should have values")

	require.NoError(t, store.Set("env", map[string]string{}))
	require.NoError(t, store.Write())

	onDisk := mustReadConfig(t, cfgPath)
	assert.Empty(t, onDisk.Env, "clearing map via Set should persist an empty map")
}

// TestStore_Set_EmptyStringsNotWritten verifies that Set+Write does not
// pollute the written file with zero-value empty strings. This is the
// root cause of the config layer override bug: when init creates a project
// file, zero-value string fields like agent.editor="" were written to disk,
// overriding values from higher-priority user-config layers during merge.
func TestStore_Set_EmptyStringsNotWritten(t *testing.T) {
	dir := t.TempDir()

	// Start with a store seeded from defaults (only name has a value).
	store, err := NewStore[testConfig](
		WithFilenames("config.yaml"),
		WithDefaults(`name: default-app`),
		WithPaths(dir),
	)
	require.NoError(t, err)

	// Set only the name field — build.image and build.target remain "".
	require.NoError(t, store.Set("name", "my-project"))
	require.NoError(t, store.Write())

	// Read raw YAML from disk — empty string fields must be absent.
	raw, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)

	var onDiskMap map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &onDiskMap))

	// The written file should have name but NOT build.image or build.target.
	assert.Equal(t, "my-project", onDiskMap["name"])

	// build section should either be absent or contain no empty string fields.
	if buildMap, ok := onDiskMap["build"].(map[string]any); ok {
		assert.NotContains(t, buildMap, "image",
			"empty string field build.image should not be written to disk")
		assert.NotContains(t, buildMap, "target",
			"empty string field build.target should not be written to disk")
	}
}

// TestStore_Set_EmptyStringsDontOverrideLowerLayers verifies the multi-layer
// merge scenario: a user-level config sets agent values, a project-level file
// is created via Set+WriteTo with only a few fields, and the user values
// are preserved through the merge (not overridden by empty strings).
func TestStore_Set_EmptyStringsDontOverrideLowerLayers(t *testing.T) {
	projectDir := t.TempDir()
	userDir := t.TempDir()

	// User-level config: provides build.image and build.target.
	userFile := filepath.Join(userDir, "config.yaml")
	require.NoError(t, os.WriteFile(userFile, []byte(`
name: user-app
build:
  image: node:20
  target: production
`), 0o644))

	// Create a project-level store that writes to projectDir.
	// This simulates init: defaults + Set for a few fields + WriteTo.
	projectStore, err := NewStore[testConfig](
		WithFilenames("config.yaml"),
		WithDefaults(`name: default-name`),
		WithPaths(projectDir),
	)
	require.NoError(t, err)

	// Set only name — build.image and build.target are untouched (empty).
	require.NoError(t, projectStore.Set("name", "project-override"))
	require.NoError(t, projectStore.Write(ToPath(filepath.Join(projectDir, "config.yaml"))))

	// Now load a layered store: projectDir (high priority) + userDir (low priority).
	mergedStore, err := NewStore[testConfig](
		WithFilenames("config.yaml"),
		WithDefaults(`name: default-name`),
		WithPaths(projectDir, userDir),
	)
	require.NoError(t, err)

	snap := mergedStore.Read()
	assert.Equal(t, "project-override", snap.Name,
		"project layer should win for explicitly set fields")
	assert.Equal(t, "node:20", snap.Build.Image,
		"user layer value should survive — not overridden by empty string from project layer")
	assert.Equal(t, "production", snap.Build.Target,
		"user layer value should survive — not overridden by empty string from project layer")
}

// TestStore_Set_EmptyStringsPreservedInSlicesAndMaps verifies that the
// empty-string filter only applies to struct fields, not to values inside
// slices or maps where "" is valid data (e.g. env vars, list entries).
func TestStore_Set_EmptyStringsPreservedInSlicesAndMaps(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore[testConfig](
		WithFilenames("config.yaml"),
		WithDefaults(`name: test`),
		WithPaths(dir),
	)
	require.NoError(t, err)

	require.NoError(t, store.Set("name", "test"))
	require.NoError(t, store.Set("tags", []string{"a", "", "b"})) // empty string in slice
	require.NoError(t, store.Set("env", map[string]string{        // empty string in map value
		"SET_VAR":   "value",
		"EMPTY_VAR": "",
	}))
	require.NoError(t, store.Write())

	raw, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)

	var onDiskMap map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &onDiskMap))

	// Slice: empty string must be preserved, not converted to null.
	tags, ok := onDiskMap["tags"].([]any)
	require.True(t, ok, "tags should be a list")
	assert.Equal(t, []any{"a", "", "b"}, tags,
		"empty strings inside slices must be preserved")

	// Map: empty string value must be preserved, not converted to null.
	env, ok := onDiskMap["env"].(map[string]any)
	require.True(t, ok, "env should be a map")
	assert.Equal(t, "", env["EMPTY_VAR"],
		"empty string values inside maps must be preserved")
	assert.Equal(t, "value", env["SET_VAR"])
}

func TestStore_Delete(t *testing.T) {
	t.Run("deletes leaf key and updates snapshot", func(t *testing.T) {
		store, err := NewFromString[testConfig](testFullData())
		require.NoError(t, err)

		assert.Equal(t, "myproject", store.Read().Name)

		deleted, err := store.Remove("name")
		require.NoError(t, err)
		assert.True(t, deleted)
		assert.Empty(t, store.Read().Name, "snapshot should reflect deletion")
	})

	t.Run("deletes nested key", func(t *testing.T) {
		store, err := NewFromString[testConfig](testFullData())
		require.NoError(t, err)

		assert.Equal(t, "node:20", store.Read().Build.Image)

		deleted, err := store.Remove("build.image")
		require.NoError(t, err)
		assert.True(t, deleted)
		assert.Empty(t, store.Read().Build.Image)
		// Sibling key should survive.
		assert.Equal(t, "production", store.Read().Build.Target)
	})

	t.Run("returns false for missing key", func(t *testing.T) {
		store, err := NewFromString[testConfig](testFullData())
		require.NoError(t, err)

		deleted, err := store.Remove("nonexistent.path")
		require.NoError(t, err)
		assert.False(t, deleted)
	})

	t.Run("delete + write + reload shows lower layer", func(t *testing.T) {
		projectDir := t.TempDir()
		userDir := t.TempDir()

		// User-level config provides build.image.
		require.NoError(t, os.WriteFile(
			filepath.Join(userDir, "config.yaml"),
			[]byte("build:\n  image: user-image\n"), 0o644))

		// Project-level config overrides build.image.
		require.NoError(t, os.WriteFile(
			filepath.Join(projectDir, "config.yaml"),
			[]byte("name: my-project\nbuild:\n  image: project-image\n"), 0o644))

		store, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(projectDir, userDir),
		)
		require.NoError(t, err)
		assert.Equal(t, "project-image", store.Read().Build.Image)

		// Delete build.image from the project file via the tree.
		deleted, err := store.Remove("build.image")
		require.NoError(t, err)
		assert.True(t, deleted)

		// Write only the project layer.
		require.NoError(t, store.Write())

		// Reload — user layer's value should now win.
		fresh, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(projectDir, userDir),
		)
		require.NoError(t, err)
		assert.Equal(t, "user-image", fresh.Read().Build.Image,
			"after deleting from project layer, user layer value should show through")
	})
}

func TestStore_Refresh_RemergesLayers(t *testing.T) {
	t.Run("snapshot reflects true merged state after per-layer write", func(t *testing.T) {
		highDir := t.TempDir() // highest priority
		lowDir := t.TempDir()  // lowest priority

		// High-priority layer: agent.editor = nano
		require.NoError(t, os.WriteFile(
			filepath.Join(highDir, "config.yaml"),
			[]byte("build:\n  image: high-image\n"), 0o644))

		// Low-priority layer: agent.editor = vim
		require.NoError(t, os.WriteFile(
			filepath.Join(lowDir, "config.yaml"),
			[]byte("build:\n  image: low-image\nname: from-low\n"), 0o644))

		store, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(highDir, lowDir),
		)
		require.NoError(t, err)
		assert.Equal(t, "high-image", store.Read().Build.Image)

		// Set + Write to the LOW-priority layer — simulates storeui per-layer save.
		require.NoError(t, store.Set("build.image", "user-wrote-this"))
		require.NoError(t, store.Write(ToPath(filepath.Join(lowDir, "config.yaml"))))

		// Write remerges: snapshot immediately reflects the true merge —
		// high-priority layer wins even though we wrote to the low layer.
		assert.Equal(t, "high-image", store.Read().Build.Image,
			"after Write, high-priority layer wins (remerge)")

		// Fields only in the low layer survive.
		assert.Equal(t, "from-low", store.Read().Name)

		// Refresh is idempotent — no change from the already-correct state.
		require.NoError(t, store.Refresh())
		assert.Equal(t, "high-image", store.Read().Build.Image,
			"Refresh is idempotent after remerge")
	})

	t.Run("provenance updated after Refresh", func(t *testing.T) {
		highDir := t.TempDir()
		lowDir := t.TempDir()

		require.NoError(t, os.WriteFile(
			filepath.Join(highDir, "config.yaml"),
			[]byte("name: high\n"), 0o644))
		require.NoError(t, os.WriteFile(
			filepath.Join(lowDir, "config.yaml"),
			[]byte("name: low\n"), 0o644))

		store, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(highDir, lowDir),
		)
		require.NoError(t, err)

		prov, ok := store.Provenance("name")
		require.True(t, ok)
		assert.Equal(t, filepath.Join(highDir, "config.yaml"), prov.Path)

		// Mutate and write to low layer, then refresh.
		require.NoError(t, store.Set("name", "changed"))
		require.NoError(t, store.Write(ToPath(filepath.Join(lowDir, "config.yaml"))))
		require.NoError(t, store.Refresh())

		// Provenance should point back to high layer (it still wins).
		prov, ok = store.Provenance("name")
		require.True(t, ok)
		assert.Equal(t, filepath.Join(highDir, "config.yaml"), prov.Path)
	})

	t.Run("discovers newly created layer file", func(t *testing.T) {
		existingDir := t.TempDir()
		newDir := t.TempDir()

		require.NoError(t, os.WriteFile(
			filepath.Join(existingDir, "config.yaml"),
			[]byte("name: existing\n"), 0o644))

		// newDir has no config.yaml yet — store starts with one layer.
		store, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(newDir, existingDir),
		)
		require.NoError(t, err)
		require.Len(t, store.Layers(), 1, "only the existing file is discovered")

		// Write to the new path (simulates first "Local" save in storeui).
		require.NoError(t, store.Set("build.image", "new-local"))
		newFile := filepath.Join(newDir, "config.yaml")
		require.NoError(t, store.Write(ToPath(newFile)))

		// Write injects the new file into the layer stack immediately.
		layers := store.Layers()
		require.Len(t, layers, 2, "new file should be injected by Write")

		// Find the new file in layers (position depends on injection point).
		var found bool
		for _, l := range layers {
			if l.Path == newFile {
				found = true
				break
			}
		}
		assert.True(t, found, "new file should be in Layers()")

		// Refresh is idempotent — re-discovery finds the same file.
		require.NoError(t, store.Refresh())
		assert.Len(t, store.Layers(), 2, "Refresh is idempotent")
	})
}

func TestStore_MarkForWrite(t *testing.T) {
	t.Run("unchanged value written to lower layer", func(t *testing.T) {
		highDir := t.TempDir()
		lowDir := t.TempDir()

		require.NoError(t, os.WriteFile(
			filepath.Join(highDir, "config.yaml"),
			[]byte("build:\n  image: alpine\n"), 0o644))
		require.NoError(t, os.WriteFile(
			filepath.Join(lowDir, "config.yaml"),
			[]byte("name: low-only\n"), 0o644))

		store, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(highDir, lowDir),
		)
		require.NoError(t, err)
		assert.Equal(t, "alpine", store.Read().Build.Image)

		// No Set call — nothing is dirty, so Write is a no-op even to a lower layer.
		lowFile := filepath.Join(lowDir, "config.yaml")
		require.NoError(t, store.Write(ToPath(lowFile)))

		raw, _ := os.ReadFile(lowFile)
		assert.NotContains(t, string(raw), "alpine",
			"Write with nothing dirty should not write the value")

		// MarkForWrite forces the (unchanged) current value into the write set, so
		// it can be persisted down to a lower layer without a Set.
		store.MarkForWrite("build.image")
		require.NoError(t, store.Write(ToPath(lowFile)))

		raw, _ = os.ReadFile(lowFile)
		assert.Contains(t, string(raw), "alpine",
			"Write after MarkForWrite should persist the value")
	})

	t.Run("no-op when path already dirty from Set", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "config.yaml"),
			[]byte("name: original\n"), 0o644))

		store, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(dir),
		)
		require.NoError(t, err)

		// Set with a different value — path is already dirty.
		require.NoError(t, store.Set("name", "changed"))

		// MarkForWrite is idempotent — doesn't break anything.
		store.MarkForWrite("name")
		require.NoError(t, store.Write(ToPath(filepath.Join(dir, "config.yaml"))))

		raw, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
		assert.Contains(t, string(raw), "changed")
	})
}

func TestStore_Write_RefreshesLayers(t *testing.T) {
	t.Run("layers reflect written values", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "config.yaml"),
			[]byte("name: original\nbuild:\n  image: alpine\n"), 0o644))

		store, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(dir),
		)
		require.NoError(t, err)

		// Verify initial layer data.
		layers := store.Layers()
		require.Len(t, layers, 1)
		buildMap, _ := layers[0].Data["build"].(map[string]any)
		require.NotNil(t, buildMap)
		assert.Equal(t, "alpine", buildMap["image"])

		// Mutate and write.
		require.NoError(t, store.Set("build.image", "ubuntu:22.04"))
		require.NoError(t, store.Write())

		// Layer data should now reflect the written file.
		freshLayers := store.Layers()
		freshBuild, _ := freshLayers[0].Data["build"].(map[string]any)
		require.NotNil(t, freshBuild)
		assert.Equal(t, "ubuntu:22.04", freshBuild["image"],
			"layer data should be refreshed after Write")
	})

	t.Run("ToPath refreshes matching layer", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")

		require.NoError(t, os.WriteFile(cfgPath,
			[]byte("name: original\n"), 0o644))

		store, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(dir),
		)
		require.NoError(t, err)

		require.NoError(t, store.Set("name", "updated-via-topath"))
		require.NoError(t, store.Write(ToPath(cfgPath)))

		layers := store.Layers()
		require.Len(t, layers, 1)
		assert.Equal(t, "updated-via-topath", layers[0].Data["name"],
			"layer data should be refreshed after Write(ToPath)")
	})

	t.Run("provenance is fresh after Write", func(t *testing.T) {
		dir := t.TempDir()
		// First filename = highest priority (like clawker.local.yaml > clawker.yaml).
		localPath := filepath.Join(dir, "local.yaml")
		mainPath := filepath.Join(dir, "main.yaml")

		// local owns "name", main owns "build.image" — split across layers.
		require.NoError(t, os.WriteFile(localPath, []byte("name: from-local\n"), 0o644))
		require.NoError(t, os.WriteFile(mainPath, []byte("build:\n  image: alpine\n"), 0o644))

		store, err := NewStore[testConfig](
			WithFilenames("local.yaml", "main.yaml"),
			WithPaths(dir),
		)
		require.NoError(t, err)

		// Initial provenance: "name" → local (idx 0), "build" → main (idx 1).
		// Provenance tracks at the subtree level, not leaf level.
		pm := store.ProvenanceMap()
		require.Equal(t, localPath, pm["name"], "name should come from local initially")
		require.Equal(t, mainPath, pm["build"], "build should come from main initially")

		// Write build.image to the local layer (promoting it to highest priority).
		require.NoError(t, store.Set("build.image", "ubuntu"))
		require.NoError(t, store.Write(ToPath(localPath)))

		// After Write, provenance should reflect the new state:
		// "build" now exists in both layers; local (idx 0) wins.
		freshPM := store.ProvenanceMap()
		assert.Equal(t, localPath, freshPM["build"],
			"provenance for 'build' should update to local after Write")

		// The snapshot value should also be consistent.
		assert.Equal(t, "ubuntu", store.Read().Build.Image,
			"Read() snapshot should reflect post-Write state")
	})

	t.Run("new file injected into layers after Write", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "main.yaml")

		require.NoError(t, os.WriteFile(existingPath, []byte("name: original\n"), 0o644))

		// local.yaml listed first (highest priority) but doesn't exist on disk yet.
		store, err := NewStore[testConfig](
			WithFilenames("local.yaml", "main.yaml"),
			WithPaths(dir),
		)
		require.NoError(t, err)

		// Only main.yaml discovered (local.yaml doesn't exist yet).
		require.Len(t, store.Layers(), 1)

		// Write to a new file that wasn't in the layer stack.
		newPath := filepath.Join(dir, "local.yaml")
		require.NoError(t, store.Set("build.image", "ubuntu"))
		require.NoError(t, store.Write(ToPath(newPath)))

		// The new file should now appear in Layers().
		layers := store.Layers()
		require.Len(t, layers, 2, "new file should be injected into layer stack")

		var found bool
		for _, l := range layers {
			if l.Path == newPath {
				found = true
				break
			}
		}
		assert.True(t, found, "new file %s should be in Layers()", newPath)

		// Provenance should route build to the new file (highest priority).
		pm := store.ProvenanceMap()
		assert.Equal(t, newPath, pm["build"],
			"provenance should route build to newly written file")
	})
}

func TestStore_Merge_UnionHandlesNonComparableValues(t *testing.T) {
	tags := buildTagRegistry[testUnionMapCfg]()

	base := mustNode(t, map[string]any{
		"items": []any{
			map[string]any{"name": "a"},
		},
	})
	layers := []layer{
		{
			path:     "layer.yaml",
			filename: "layer.yaml",
			node: mustNode(t, map[string]any{
				"items": []any{
					map[string]any{"name": "b"},
				},
			}),
		},
	}

	require.NotPanics(t, func() {
		result, _ := merge(append(layers, layer{path: "", filename: "", node: base}), tags)
		items, ok := nodeToMap(result)["items"].([]any)
		require.True(t, ok)
		assert.Len(t, items, 2)
	})
}

func TestStore_Merge_UnionWithImplicitYAMLFieldName(t *testing.T) {
	tags := buildTagRegistry[testUnionImplicitCfg]()

	base := mustNode(t, map[string]any{
		"items": []any{"a"},
	})
	layers := []layer{
		{
			path:     "layer.yaml",
			filename: "layer.yaml",
			node: mustNode(t, map[string]any{
				"items": []any{"b"},
			}),
		},
	}

	result, _ := merge(append(layers, layer{path: "", filename: "", node: base}), tags)
	cfgResult, err := decodeNode[testUnionImplicitCfg](result)
	require.NoError(t, err)

	assert.Equal(t, []string{"a", "b"}, cfgResult.Items,
		"merge union should still apply when yaml tag uses implicit field name")
}

// testPortRule mirrors a real-world opaque struct-slice element whose Port is a
// Go string but is written on disk as a bare yaml int (e.g. `port: 22`).
type testPortRule struct {
	Dst  string `yaml:"dst"`
	Port string `yaml:"port,omitempty"`
}

type testPortRuleCfg struct {
	Name  string         `yaml:"name"`
	Rules []testPortRule `yaml:"rules" merge:"union"`
}

func (t testPortRuleCfg) Fields() FieldSet { return NormalizeFields(t) }

// TestStore_Set_TypedScalarDriftDoesNotFalselyDirtyOpaqueSlice reproduces a
// store-routing regression: editing one unrelated scalar funneled an untouched
// opaque struct-slice into the targeted layer file.
//
// Root cause: on-disk `port: 22` parses as a yaml !!int into the raw merged
// tree, but coerces into the Go `string` Port field and re-serializes as the
// quoted string `"22"`. Set() diffed the raw parsed tree against the
// struct-serialized form, so the int-vs-string representation mismatch flagged
// the whole rules slice as changed even though the caller never touched it.
// Set() must diff serialized-before vs serialized-after so the coercion cancels.
func TestStore_Set_TypedScalarDriftDoesNotFalselyDirtyOpaqueSlice(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.yaml")
	localPath := filepath.Join(dir, "local.yaml")

	// Unquoted int ports — parsed as yaml !!int, coerced into the string field.
	baseYAML := `
name: base
rules:
  - dst: github.com
    port: 22
  - dst: api.github.com
    port: 443
`
	localYAML := `
name: base
`
	require.NoError(t, os.WriteFile(basePath, []byte(baseYAML), 0o644))
	require.NoError(t, os.WriteFile(localPath, []byte(localYAML), 0o644))

	baseData, err := loadNode(basePath)
	require.NoError(t, err)
	localData, err := loadNode(localPath)
	require.NoError(t, err)

	// local.yaml is the higher-priority layer (index 0).
	layers := []layer{
		{path: localPath, filename: "local.yaml", node: localData},
		{path: basePath, filename: "base.yaml", node: baseData},
	}
	tags := buildTagRegistry[testPortRuleCfg]()
	tree, prov := merge(layers, tags)
	value, err := decodeNode[testPortRuleCfg](tree)
	require.NoError(t, err)

	store := &Store[testPortRuleCfg]{
		tree:   tree,
		layers: layers,
		prov:   prov,
		tags:   tags,
		opts:   options{filenames: []string{"base.yaml", "local.yaml"}},
	}
	store.value.Store(value)

	// Edit only the top-level scalar, routed explicitly to the local layer
	// (mirrors storeui's per-field save: Set + Write(ToPath(target))).
	require.NoError(t, store.Set("name", "local-updated"))
	require.NoError(t, store.Write(ToPath(localPath)))

	var localMap map[string]any
	raw, err := os.ReadFile(localPath)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &localMap))

	assert.Equal(t, "local-updated", localMap["name"])
	assert.NotContains(t, localMap, "rules",
		"untouched opaque rules slice must not be routed into the local layer file")
}

// TestStore_Set_ClearScalarRoutesDeleteToOwningLayer pins the behavior the
// symmetric serialized diff newly enables: clearing a scalar to its zero value
// is recorded as a delete and routed to the layer that owns the field, rather
// than being silently ignored (the old raw-tree-vs-merged-tree diff never saw
// the clear because mergeIntoTree does not remove keys).
func TestStore_Set_ClearScalarRoutesDeleteToOwningLayer(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.yaml")
	localPath := filepath.Join(dir, "local.yaml")

	baseYAML := `
name: base-name
version: 1
`
	// local.yaml is the higher-priority layer and owns the winning name.
	localYAML := `
name: local-name
version: 2
`
	require.NoError(t, os.WriteFile(basePath, []byte(baseYAML), 0o644))
	require.NoError(t, os.WriteFile(localPath, []byte(localYAML), 0o644))

	baseData, err := loadNode(basePath)
	require.NoError(t, err)
	localData, err := loadNode(localPath)
	require.NoError(t, err)

	layers := []layer{
		{path: localPath, filename: "local.yaml", node: localData},
		{path: basePath, filename: "base.yaml", node: baseData},
	}
	tags := buildTagRegistry[testConfig]()
	tree, prov := merge(layers, tags)
	value, err := decodeNode[testConfig](tree)
	require.NoError(t, err)

	store := &Store[testConfig]{
		tree:   tree,
		layers: layers,
		prov:   prov,
		tags:   tags,
		opts:   options{filenames: []string{"base.yaml", "local.yaml"}},
	}
	store.value.Store(value)

	// Clear the scalar via Remove; route via provenance (no explicit ToPath).
	_, err = store.Remove("name")
	require.NoError(t, err)
	require.NoError(t, store.Write())

	// The owning layer must lose the key entirely (not retain a stale value
	// nor write an empty string), while untouched siblings stay put.
	var localMap map[string]any
	raw, err := os.ReadFile(localPath)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &localMap))
	assert.NotContains(t, localMap, "name", "cleared scalar must be deleted from the owning layer file")
	assert.Equal(t, 2, localMap["version"], "untouched sibling must remain")

	// The merged value must now fall through to the lower layer.
	assert.Equal(t, "base-name", store.Read().Name)
}

// TestStore_Set_RejectsSchemaBreakingValue proves the normal Set path validates
// the decode before committing. validateKind only guards leaf schema paths, so a
// scalar grafted over a non-leaf struct path ("build") would otherwise produce a
// tree that no longer decodes — and the old best-effort refreshSnapshot kept the
// stale snapshot while leaving the path dirty, so the next Write persisted the
// bad scalar and the next process start failed the strict load.
func TestStore_Set_RejectsSchemaBreakingValue(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(testFullData()), 0o644))

	store, err := NewStore[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
	require.NoError(t, err)
	require.Equal(t, "node:20", store.Read().Build.Image)

	err = store.Set("build", "oops")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no longer decodes")

	// Snapshot is untouched by the rejected Set.
	assert.Equal(t, "node:20", store.Read().Build.Image)

	// Nothing was marked dirty: Write is a clean no-op and the file is intact.
	require.NoError(t, store.Write())
	reloaded, err := NewStore[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
	require.NoError(t, err)
	assert.Equal(t, "node:20", reloaded.Read().Build.Image)
}

// TestStore_Set_KindValidation covers the validateKind/kindAccepts guard: a value
// whose Go kind cannot satisfy the schema field is rejected, valid values pass,
// nil clears any field, and non-schema paths bypass the check (migrations).
func TestStore_Set_KindValidation(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		value   any
		wantErr bool
	}{
		{"text accepts string", "name", "ok", false},
		{"text rejects int", "name", 5, true},
		{"int accepts int", "version", 7, false},
		{"int rejects string", "version", "seven", true},
		{"slice accepts []string", "packages", []string{"a"}, false},
		{"slice rejects string", "packages", "a", true},
		{"map accepts map", "env", map[string]string{"A": "1"}, false},
		{"map rejects string", "env", "A=1", true},
		{"nil clears any field", "name", nil, false},
		{"non-schema path passes through", "legacy_field", "anything", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewFromString[testConfig](testFullData())
			require.NoError(t, err)
			err = store.Set(tc.path, tc.value)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestStore_WriteOption_ZeroValue proves the zero WriteOption is rejected rather
// than silently targeting layer 0 (which ToLayer(0) must still reach).
func TestStore_WriteOption_ZeroValue(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(testFullData()), 0o644))
	store, err := NewStore[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
	require.NoError(t, err)
	require.NoError(t, store.Set("name", "x"))

	var zero WriteOption
	err = store.Write(zero)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid WriteOption")

	require.NoError(t, store.Write(ToLayer(0)))
}

// TestStore_GetAndHas covers the path-based read API directly (it is otherwise
// only exercised indirectly through migrations).
func TestStore_GetAndHas(t *testing.T) {
	store, err := NewFromString[testConfig](testFullData())
	require.NoError(t, err)

	var image string
	found, err := store.Get("build.image", &image)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "node:20", image)

	// Absent path → found=false, no error, destination untouched.
	var missing string
	found, err = store.Get("build.nonexistent", &missing)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, missing)

	// nil out → presence check without decoding.
	found, err = store.Get("name", nil)
	require.NoError(t, err)
	assert.True(t, found)

	// Decode into a mismatched destination surfaces an error.
	var wrong int
	_, err = store.Get("name", &wrong)
	require.Error(t, err)

	assert.True(t, store.Has("version"))
	assert.False(t, store.Has("does.not.exist"))
}

// TestStore_Migrations_RunOnStore covers the storage-level migration runner:
// migrations run against each file layer's own node (legacy key stripped from
// every owning file, not just the merge winner), and a migration whose store
// type does not match T aborts construction instead of being silently skipped.
func TestStore_Migrations_RunOnStore(t *testing.T) {
	dropLegacy := func(s *Store[testConfig]) (bool, error) {
		if s.Has("legacy_field") {
			return s.Remove("legacy_field")
		}
		return false, nil
	}

	t.Run("runs per layer and rewrites each owning file", func(t *testing.T) {
		hiDir := t.TempDir()
		loDir := t.TempDir()
		hi := filepath.Join(hiDir, "config.yaml")
		lo := filepath.Join(loDir, "config.yaml")
		require.NoError(t, os.WriteFile(hi, []byte("name: hi\nlegacy_field: gone\n"), 0o644))
		require.NoError(t, os.WriteFile(lo, []byte("name: lo\nversion: 3\nlegacy_field: gone\n"), 0o644))

		store, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(hiDir, loDir),
			WithMigrations(dropLegacy),
		)
		require.NoError(t, err)
		assert.Equal(t, "hi", store.Read().Name)
		assert.Equal(t, 3, store.Read().Version)

		hiBytes, err := os.ReadFile(hi)
		require.NoError(t, err)
		assert.NotContains(t, string(hiBytes), "legacy_field")
		loBytes, err := os.ReadFile(lo)
		require.NoError(t, err)
		assert.NotContains(t, string(loBytes), "legacy_field")
	})

	t.Run("aborts construction on wrong migration store type", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("name: x\n"), 0o644))

		// WithMigrations[testUnionMapCfg] does not match Store[testConfig]; the
		// type-erased assertion in migrateLayer must surface an error, not skip.
		bad := func(_ *Store[testUnionMapCfg]) (bool, error) { return false, nil }
		_, err := NewStore[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(dir),
			WithMigrations(bad),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wrong type")
	})
}

// txnAppendTag reads the tags slice, appends one entry, and writes — all inside a
// single store transaction, so concurrent callers cannot lose an update.
func txnAppendTag(store *Store[testConfig], tag string) error {
	return store.Txn(func(tx *Tx[testConfig]) error {
		tags := make([]string, 0, 1)
		if _, e := tx.Get("tags", &tags); e != nil {
			return e
		}
		tags = append(tags, tag)
		if e := tx.Set("tags", tags); e != nil {
			return e
		}
		return tx.Write()
	})
}

// TestStore_Txn_SerializesReadModifyWrite proves Txn makes a compound
// Get→Set→Write atomic against concurrent callers — every append lands, with no
// lost update. Run under -race it also exercises the Layers/Provenance locking.
func TestStore_Txn_SerializesReadModifyWrite(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("tags: []\n"), 0o644))
	store, err := NewStore[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
	require.NoError(t, err)

	const goroutines = 8
	const perG = 5
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perG {
				assert.NoError(t, txnAppendTag(store, fmt.Sprintf("g%d-%d", g, i)))
			}
		}(g)
	}
	wg.Wait()

	require.NoError(t, store.Refresh())
	assert.Len(t, store.Read().Tags, goroutines*perG)
}

func TestRootMapping_RejectsNonMappingRoot(t *testing.T) {
	cases := []struct {
		name      string
		data      string
		wantErr   bool
		wantEmpty bool // expect an empty mapping node (only checked when !wantErr)
	}{
		{name: "empty bytes", data: "", wantErr: false, wantEmpty: true},
		{name: "comments only", data: "# just a comment\n", wantErr: false, wantEmpty: true},
		{name: "mapping root", data: "build:\n  image: x\n", wantErr: false, wantEmpty: false},
		{name: "sequence root", data: "- one\n- two\n", wantErr: true, wantEmpty: false},
		{name: "scalar root", data: "just-a-string\n", wantErr: true, wantEmpty: false},
		{name: "numeric scalar root", data: "42\n", wantErr: true, wantEmpty: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node, err := rootMapping([]byte(tc.data))
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, node)
				require.Contains(t, err.Error(), "expected a mapping at the document root")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, node)
			require.Equal(t, yaml.MappingNode, node.Kind)
			if tc.wantEmpty {
				require.Empty(t, node.Content)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	for _, ok := range []string{"name", "build.image", "a.b.c"} {
		require.NoError(t, validatePath(ok), "path %q must be accepted", ok)
	}
	for _, bad := range []string{"", ".", "build.", ".build", "a..b"} {
		require.Error(t, validatePath(bad), "path %q must be rejected", bad)
	}
}

func TestStore_SetRemove_RejectMalformedPath(t *testing.T) {
	store, err := NewFromString[testConfig](testFullData())
	require.NoError(t, err)

	for _, bad := range []string{"", "build.", "a..b"} {
		require.Error(t, store.Set(bad, "x"), "Set(%q) must be rejected", bad)
		_, rerr := store.Remove(bad)
		require.Error(t, rerr, "Remove(%q) must be rejected", bad)
	}

	// A well-formed path still works — the guard rejects only malformed input.
	require.NoError(t, store.Set("name", "ok"))
}
