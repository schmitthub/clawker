package storage

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- deterministic seeds for oracle/golden tests
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
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

// mustGet reads the merged value at key, failing the test when it is absent.
//
//nolint:ireturn // Get is generic; the helper mirrors its type parameter.
func mustGet[V any, T Schema](t *testing.T, s *Store[T], key ...string) V {
	t.Helper()
	v, err := Get[V](s, key...)
	require.NoError(t, err)
	return v
}

// getOr reads the merged value at key, yielding V's zero value when the key is
// unset — for assembling a whole-struct view from individual keys.
//
//nolint:ireturn // Get is generic; the helper mirrors its type parameter.
func getOr[V any, T Schema](t *testing.T, s *Store[T], key ...string) V {
	t.Helper()
	v, err := Get[V](s, key...)
	if err != nil {
		require.ErrorIs(t, err, ErrKeyNotFound)
		var zero V
		return zero
	}
	return v
}

// requireAbsent asserts that key resolves to nothing in the merged tree.
func requireAbsent[T Schema](t *testing.T, s *Store[T], key ...string) {
	t.Helper()
	_, err := Get[any](s, key...)
	require.ErrorIs(t, err, ErrKeyNotFound, "key %v must be absent", key)
}

// mergedConfig assembles the fields the merge tests assert on by reading each
// key through Get — there is no whole-tree read.
func mergedConfig(t *testing.T, s *Store[testConfig]) testConfig {
	t.Helper()
	return testConfig{
		Name:    getOr[string](t, s, "name"),
		Version: getOr[int](t, s, "version"),
		Build: testBuild{
			Image:  getOr[string](t, s, "build", "image"),
			Target: getOr[string](t, s, "build", "target"),
		},
		Packages: getOr[[]string](t, s, "packages"),
		Plugins:  getOr[[]string](t, s, "plugins"),
		Tags:     getOr[[]string](t, s, "tags"),
		Env:      getOr[map[string]string](t, s, "env"),
	}
}

// testHeader is a stand-in yaml-language-server directive for WithHeader tests.
const (
	testSchemaURL = "https://example.test/clawker.schema.json"
	testHeader    = "yaml-language-server: $schema=" + testSchemaURL
)

func testRenderedHeader() string { return "# " + testHeader }

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
				{path: fullPath, filename: "full.yaml", node: full, virtual: false, walkUp: false},
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
				{path: overridePath, filename: "override.yaml", node: override, virtual: false, walkUp: false},
				{path: fullPath, filename: "full.yaml", node: full, virtual: false, walkUp: false},
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
				{path: partialPath, filename: "partial.yaml", node: partial, virtual: false, walkUp: false},
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
				mergeLayers = append(
					mergeLayers,
					layer{path: "", filename: "", node: tt.base, virtual: true, walkUp: false},
				)
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
				assert.Equal(t, wantIdx, prov[schemaKey(key)], "provenance for %s", key)
			}
		})
	}
}

func TestStore_Write(t *testing.T) {
	t.Run("set and write persists dirty fields to disk", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte(testFullData()), 0o644))

		store, err := New[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
		require.NoError(t, err)

		require.NoError(t, store.Set([]string{"name"}, "updated"))
		require.NoError(t, store.Set([]string{"version"}, 99))
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

		store, err := New[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
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

		require.NoError(t, store.Set([]string{"name"}, "nope"))
		assert.Error(t, store.Write())
	})

	t.Run("write with lock", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte(testFullData()), 0o644))

		store, err := New[testConfig](WithFilenames("config.yaml"), WithPaths(dir), WithLock())
		require.NoError(t, err)

		require.NoError(t, store.Set([]string{"name"}, "locked-write"))
		require.NoError(t, store.Write())

		result := mustReadConfig(t, cfgPath)
		assert.Equal(t, "locked-write", result.Name)
		assert.Equal(t, 1, result.Version, "unchanged fields should survive")
		assert.Equal(t, "node:20", result.Build.Image, "unchanged fields should survive")
	})
}

// TestStore_Header covers the WithHeader comment block: stamped as the first
// line(s), never duplicated on re-write, stale directive values from another
// writer replaced, absent when no header is set, and emitted without
// clobbering pre-existing user comments.
func TestStore_Header(t *testing.T) {
	newStore := func(dir, header string) *Store[testConfig] {
		opts := []Option{WithFilenames("config.yaml"), WithPaths(dir)}
		if header != "" {
			opts = append(opts, WithHeader(header))
		}
		s, err := New[testConfig](opts...)
		require.NoError(t, err)
		return s
	}

	t.Run("stamped as first line", func(t *testing.T) {
		dir := t.TempDir()
		s := newStore(dir, testHeader)
		require.NoError(t, s.Set([]string{"name"}, "demo"))
		require.NoError(t, s.Write())

		got := mustReadFile(t, filepath.Join(dir, "config.yaml"))
		assert.Equal(t, testRenderedHeader(), strings.SplitN(got, "\n", 2)[0],
			"header must be the first line\nfile:\n%s", got)
		assert.Contains(t, got, "name: demo")
	})

	t.Run("not duplicated on re-write", func(t *testing.T) {
		dir := t.TempDir()
		s := newStore(dir, testHeader)
		require.NoError(t, s.Set([]string{"name"}, "demo"))
		require.NoError(t, s.Write())

		// Fresh store discovers + re-reads the already-stamped file.
		s2 := newStore(dir, testHeader)
		require.NoError(t, s2.Set([]string{"version"}, 2))
		require.NoError(t, s2.Write())

		got := mustReadFile(t, filepath.Join(dir, "config.yaml"))
		assert.Equal(t, 1, strings.Count(got, testRenderedHeader()),
			"header must appear exactly once after re-write\nfile:\n%s", got)
		assert.Equal(t, testRenderedHeader(), strings.SplitN(got, "\n", 2)[0])

		reloaded := newStore(dir, testHeader)
		assert.Equal(t, "demo", mustGet[string](t, reloaded, "name"))
		assert.Equal(t, 2, mustGet[int](t, reloaded, "version"))
	})

	t.Run("preserves user comments", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("name: original # keep me\n"), 0o644))

		s := newStore(dir, testHeader)
		require.NoError(t, s.Set([]string{"version"}, 7))
		require.NoError(t, s.Write())

		got := mustReadFile(t, path)
		assert.Contains(t, got, "keep me",
			"user comment on an untouched key must survive a field-merge write\nfile:\n%s", got)
		assert.Contains(t, got, "version: 7")
		assert.Equal(t, testRenderedHeader(), strings.SplitN(got, "\n", 2)[0])
	})

	t.Run("absent when no header", func(t *testing.T) {
		dir := t.TempDir()
		s := newStore(dir, "")
		require.NoError(t, s.Set([]string{"name"}, "demo"))
		require.NoError(t, s.Write())

		got := mustReadFile(t, filepath.Join(dir, "config.yaml"))
		assert.NotContains(t, got, "yaml-language-server",
			"no header should be written when none is configured")
	})

	t.Run("multi-line header renders and never stacks", func(t *testing.T) {
		dir := t.TempDir()
		noteLine := "Managed by clawker; comments survive writes"
		header := testHeader + "\n" + noteLine

		s := newStore(dir, header)
		require.NoError(t, s.Set([]string{"name"}, "demo"))
		require.NoError(t, s.Write())

		// Fresh store re-reads the stamped file and writes again.
		s2 := newStore(dir, header)
		require.NoError(t, s2.Set([]string{"version"}, 2))
		require.NoError(t, s2.Write())

		got := mustReadFile(t, filepath.Join(dir, "config.yaml"))
		lines := strings.SplitN(got, "\n", 3)
		require.Len(t, lines, 3, "file:\n%s", got)
		assert.Equal(t, testRenderedHeader(), lines[0])
		assert.Equal(t, "# "+noteLine, lines[1])
		assert.Equal(t, 1, strings.Count(got, noteLine),
			"colon-less header line must appear exactly once after re-write\nfile:\n%s", got)
		assert.Equal(t, 1, strings.Count(got, "yaml-language-server"))
	})

	t.Run("replaces stale directive value from another writer", func(t *testing.T) {
		dir := t.TempDir()
		stale := newStore(dir, "yaml-language-server: $schema=https://example.test/old.json")
		require.NoError(t, stale.Set([]string{"name"}, "demo"))
		require.NoError(t, stale.Write())

		// A store configured with a different URL (e.g. a newer release
		// pinning its own tag) must replace the directive, not stack a
		// second copy.
		s := newStore(dir, testHeader)
		require.NoError(t, s.Set([]string{"version"}, 2))
		require.NoError(t, s.Write())

		got := mustReadFile(t, filepath.Join(dir, "config.yaml"))
		assert.NotContains(t, got, "old.json",
			"stale directive value must be replaced\nfile:\n%s", got)
		assert.Equal(t, 1, strings.Count(got, "yaml-language-server"))
		assert.Equal(t, testRenderedHeader(), strings.SplitN(got, "\n", 2)[0])
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
	store, err := New[testConfig](WithFilenames("config.yaml"), WithPaths(hiDir, loDir))
	require.NoError(t, err)

	// Sanity: version is owned by the base (low) file.
	prov, ok := store.Provenance("version")
	require.True(t, ok)
	require.Equal(t, basePath, prov.Path, "version must be provenance-owned by the base file")

	// Change a value owned by B, then write. Provenance routes it to B.
	require.NoError(t, store.Set([]string{"version"}, 2))
	require.NoError(t, store.Write())

	got := mustReadFile(t, basePath)

	// Byte-exact: B is its original text with only the dirtied node's value
	// updated — comments (head + both fields, including the changed one)
	// intact, nothing from layer A or the virtual layer grafted in.
	const wantBase = `# file: base (low priority)
name: base-name # B name comment
version: 2 # B version comment
`
	assert.Equal(t, wantBase, got, "routed write must be the original file with only the dirtied node updated")

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

	store, err := New[testConfig](WithFilenames("config.yaml"), WithPaths(hiDir, loDir))
	require.NoError(t, err)

	// build.target is unset in both files; build.image is owned by the local
	// (high) file, so a new build.target routes to the local file (walk-up to
	// the owning layer of build.*).
	require.NoError(t, store.Set([]string{"build", "target"}, "prod"))
	require.NoError(t, store.Write())

	got := mustReadFile(t, localPath)
	// Byte-exact: local file is its original text plus only the new dirtied
	// key — its own comment intact, nothing from the base file or defaults.
	const wantLocal = `build:
  image: local-img # local only
  target: prod
`
	assert.Equal(t, wantLocal, got, "routed write must add only the dirtied key to the owning file")

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

	// local.yaml is listed first, so it is the higher-priority layer.
	store, err := NewFromString[testConfig](testPartialData(),
		WithFilenames("local.yaml", "global.yaml"),
		WithPaths(dir),
	)
	require.NoError(t, err)

	require.NoError(t, store.Set([]string{"name"}, "provenance-test"))
	require.NoError(t, store.Write())

	// name came from local layer (highest priority) — verify it was written there.
	localResult := mustReadConfig(t, localPath)
	assert.Equal(t, "provenance-test", localResult.Name)

	// The layer that owns no dirty field keeps its own content.
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

	store, err := New[testConfig](WithFilenames("local.yaml", "global.yaml"), WithPaths(dir))
	require.NoError(t, err)

	require.NoError(t, store.Set([]string{"name"}, "local-updated"))
	require.NoError(t, store.Set([]string{"tags"}, []string{"global-updated"}))
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

	store, err := New[testConfig](WithFilenames("local.yaml", "global.yaml"), WithPaths(dir))
	require.NoError(t, err)

	// Add a NEW map entry — FOO has no provenance because it's not in any layer file.
	require.NoError(t, store.Set([]string{"env", "FOO"}, "2"))
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

	store, err := New[testConfig](
		WithFilenames("config.yaml", "config.local.yaml"),
		WithPaths(dir),
	)
	require.NoError(t, err)

	require.NoError(t, store.Set([]string{"name"}, "targeted-write"))

	// Write to explicit path — should create config.local.yaml.
	require.NoError(t, store.WriteTo(localPath))

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

			store, err := New[testConfig](
				WithFilenames("config.yaml"),
				WithDirs(projectDir),
			)
			require.NoError(t, err)

			layers := store.Layers()
			assert.Len(t, layers, tt.wantLayers)

			if tt.wantName != "" {
				assert.Equal(t, tt.wantName, mustGet[string](t, store, "name"))
			}
		})
	}
}

// TestStore_MixedPlacementDiscovery pins the dual-placement precedence
// contract at a single level: each filename resolves independently through
// .clawker/{f} → .clawker/.{f} → .{f}, so the dir form beats a flat duplicate
// of the SAME filename while flat files of other filenames stay in play —
// a .clawker/ directory must never black out sibling flat dotfiles.
func TestStore_MixedPlacementDiscovery(t *testing.T) {
	const mainYAML = "name: main-file\n"
	const localYAML = "name: local-file\n"

	tests := []struct {
		name      string
		files     map[string]string // relative path → content
		wantPaths []string          // relative, highest priority first
		wantName  string
	}{
		{
			name: "flat main beside dir-form local",
			files: map[string]string{
				".config.yaml":               mainYAML,
				".clawker/config.local.yaml": localYAML,
			},
			wantPaths: []string{".clawker/config.local.yaml", ".config.yaml"},
			wantName:  "local-file",
		},
		{
			name: "flat main beside dotted dir-form local",
			files: map[string]string{
				".config.yaml":                mainYAML,
				".clawker/.config.local.yaml": localYAML,
			},
			wantPaths: []string{".clawker/.config.local.yaml", ".config.yaml"},
			wantName:  "local-file",
		},
		{
			name: "flat duplicate shadowed by dir form",
			files: map[string]string{
				".clawker/config.yaml": mainYAML,
				".config.yaml":         "name: SHADOWED\n",
			},
			wantPaths: []string{".clawker/config.yaml"},
			wantName:  "main-file",
		},
		{
			name: "dotted dir form shadowed by canonical dir form",
			files: map[string]string{
				".clawker/config.yaml":  mainYAML,
				".clawker/.config.yaml": "name: SHADOWED\n",
			},
			wantPaths: []string{".clawker/config.yaml"},
			wantName:  "main-file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tt.files {
				path := filepath.Join(dir, rel)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
			}

			store, err := New[testConfig](
				WithFilenames("config.local.yaml", "config.yaml"),
				WithDefaultFilename("config.yaml"),
				WithDirs(dir),
			)
			require.NoError(t, err)

			got := make([]string, 0, len(store.Layers()))
			for _, l := range store.Layers() {
				rel, relErr := filepath.Rel(dir, l.Path)
				require.NoError(t, relErr)
				got = append(got, filepath.ToSlash(rel))
			}
			assert.Equal(t, tt.wantPaths, got)
			assert.Equal(t, tt.wantName, mustGet[string](t, store, "name"))
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
			meta, ok := reg[schemaKey(tt.path)]
			require.True(t, ok, "path %q should be in registry", tt.path)
			assert.Equal(t, tt.kind, meta.kind, "path %q kind mismatch", tt.path)
		})
	}

	// Verify merge tags are still recorded alongside kinds.
	assert.Equal(t, "union", reg[schemaKey("packages")].mergeTag)
	assert.Equal(t, "overwrite", reg[schemaKey("plugins")].mergeTag)
	assert.Empty(t, reg[schemaKey("name")].mergeTag, "untagged field should have empty merge tag")
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

	store, err := New[testConfig](
		WithFilenames("config.yaml"),
		WithDirs(highDir, lowDir),
	)
	require.NoError(t, err)

	assert.Len(t, store.Layers(), 2)

	// High-priority dir wins for scalar fields.
	assert.Equal(t, "override", mustGet[string](t, store, "name"))
	assert.Equal(t, 99, mustGet[int](t, store, "version"))

	// Low-priority dir provides fields not set in high-priority.
	assert.Equal(t, "node:20", mustGet[string](t, store, "build", "image"))
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

// Placement forms for the walk-up fixture generators. Each file at a level
// is placed independently — levels may mix forms (flat main beside a
// .clawker/ local override).
const (
	placeFlat      = iota // .{filename} in the level dir
	placeDir              // .clawker/{filename} (canonical dir form)
	placeDirDotted        // .clawker/.{filename} (dotted dir form)
)

// placedPath returns the fixture path for a filename at the given placement.
func placedPath(dir, fname string, form int) string {
	switch form {
	case placeDir:
		return filepath.Join(dir, ".clawker", fname)
	case placeDirDotted:
		return filepath.Join(dir, ".clawker", "."+fname)
	default:
		return filepath.Join(dir, "."+fname)
	}
}

func TestStore_WalkUpLayerMerge(t *testing.T) {
	// Property-based walk-up test. Each run randomizes the placement matrix:
	//   - Which filenames (config.yaml, config.local.yaml, both, or neither) exist
	//   - Per-file placement: flat dotfile, .clawker/ dir form, or dotted dir form
	//   - Whether a decoy exists (flat dotfile DUPLICATE of a dir-placed main —
	//     shadowed by the dir form, never a layer)
	//
	// The seed is logged so failures are reproducible via: go test -run ... -seed=<N>
	//
	// Invariants asserted regardless of placement:
	//   1. Walk-up layers are CWD-first; explicit path is last
	//   2. Dir form (.clawker/) takes precedence per filename — a flat dotfile
	//      duplicate of a dir-form file is shadowed; other filenames at the
	//      same level resolve independently (mixed placement is honored)
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
		hasMain    bool
		hasLocal   bool
		mainPlace  int
		localPlace int
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
			hasMain:    rng.IntN(3) > 0, // 2/3 chance
			hasLocal:   rng.IntN(3) > 0, // 2/3 chance
			mainPlace:  rng.IntN(3),
			localPlace: rng.IntN(3),
		}
		if isDeepest {
			// Deepest level must have both files to exercise filename priority.
			p.hasMain = true
			p.hasLocal = true
		} else if !p.hasMain && !p.hasLocal {
			p.hasMain = true
		}
		// A decoy is a flat dotfile DUPLICATE of a dir-placed main file —
		// the dir form shadows it, so it must never become a layer.
		if p.hasMain && p.mainPlace != placeFlat {
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

	placeLabel := func(form int) string {
		switch form {
		case placeDir:
			return "dir"
		case placeDirDotted:
			return "dir-dotted"
		default:
			return "root"
		}
	}

	for _, p := range placements {
		require.NoError(t, os.MkdirAll(p.dir, 0o755))

		if p.hasMain {
			path := placedPath(p.dir, "config.yaml", p.mainPlace)
			writeFile(path, toYAML(p.name+"-main", p.mainGen))
			pathTag[path] = fileTag{p.name, "main", placeLabel(p.mainPlace)}
		}
		if p.hasLocal {
			path := placedPath(p.dir, "config.local.yaml", p.localPlace)
			writeFile(path, toYAML(p.name+"-local", p.localGen))
			pathTag[path] = fileTag{p.name, "local", placeLabel(p.localPlace)}
		}
		if p.hasDecoy {
			decoyPath := filepath.Join(p.dir, ".config.yaml")
			writeFile(decoyPath, "name: DECOY\npackages:\n  - DECOY\n")
			wantIgnored = append(wantIgnored, decoyPath)
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

	store, err := New[testConfig](
		WithFilenames("config.local.yaml", "config.yaml"),
		WithWalkUp(projectDir),
		WithPaths(userConfigDir),
	)
	require.NoError(t, err)

	// --- Print layers table using LayerInfo.Data (no re-reading from disk) ---
	layers := store.Layers()
	cfg := mergedConfig(t, store)
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
	// ProvenanceMap keys are display-form and never reparsed, so the segments
	// come from the store's own key registry.
	for joined, idx := range store.prov {
		key := splitKey(joined)
		li, ok := store.Provenance(key...)
		assert.True(t, ok, "Provenance(%q) should return true", key)
		assert.Equal(t, store.Layers()[idx].Path, li.Path,
			"Provenance(%q) path mismatch", key)
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
		hasMain    bool
		hasLocal   bool
		mainPlace  int
		localPlace int
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
			hasMain:    rng.IntN(3) > 0,
			hasLocal:   rng.IntN(3) > 0,
			mainPlace:  rng.IntN(3),
			localPlace: rng.IntN(3),
		}
		if isDeepest {
			p.hasMain = true
			p.hasLocal = true
		} else if !p.hasMain && !p.hasLocal {
			p.hasMain = true
		}
		if p.hasMain && p.mainPlace != placeFlat {
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
		if p.hasMain {
			writeFile(placedPath(p.dir, "config.yaml", p.mainPlace), toYAML(p.name+"-main", p.mainGen))
		}
		if p.hasLocal {
			writeFile(placedPath(p.dir, "config.local.yaml", p.localPlace), toYAML(p.name+"-local", p.localGen))
		}
		if p.hasDecoy {
			writeFile(filepath.Join(p.dir, ".config.yaml"), "name: DECOY\npackages:\n  - DECOY\n")
		}
	}

	userPath := filepath.Join(userConfigDir, "config.yaml")
	writeFile(
		userPath,
		"name: user\nversion: 1\nbuild:\n  image: ubuntu\npackages:\n  - pkg-user\nenv:\n  EDITOR: vim\n",
	)

	t.Chdir(levels[len(levels)-1])

	store, err := New[testConfig](
		WithFilenames("config.local.yaml", "config.yaml"),
		WithWalkUp(projectDir),
		WithPaths(userConfigDir),
	)
	require.NoError(t, err)
	cfg := mergedConfig(t, store)

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
	goldenVersion := 876
	goldenImage := "go:1.22"
	goldenPackages := []string{"pkg-user", "pkg-project", "git", "vim", "pkg-level2", "pkg-level3", "curl"}
	// Map overwrite: highest-priority layer with env wins entirely.
	// With seed 42, level1 main is the highest-priority layer that has
	// an env section (deeper walk-up = higher priority).
	goldenEnv := map[string]string{
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
			store, err := New[testConfig](
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

	store, err := New[testConfig](
		WithFilenames("config.yaml"),
		WithDirs(dir),
		WithPaths(dir),
	)
	require.NoError(t, err)

	// Only one layer — the dotfile discovered by WithDirs.
	// WithPaths probes dir/config.yaml (plain form) which doesn't exist.
	assert.Len(t, store.Layers(), 1)
	assert.Equal(t, "from-dotfile", mustGet[string](t, store, "name"))
}

func TestStore_MutationWithSet(t *testing.T) {
	t.Run("two Sets on different fields — both survive", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFromString[testConfig](testFullData(), WithFilenames("config.yaml"), WithPaths(dir))
		require.NoError(t, err)

		// Caller A sets name.
		require.NoError(t, store.Set([]string{"name"}, "set-by-A"))

		// Caller B sets version.
		require.NoError(t, store.Set([]string{"version"}, 999))

		assert.Equal(t, "set-by-A", mustGet[string](t, store, "name"), "name from caller A")
		assert.Equal(t, 999, mustGet[int](t, store, "version"), "version from caller B")
		assert.Equal(t, "node:20", mustGet[string](t, store, "build", "image"), "untouched field preserved")

		// Write and verify on disk.
		require.NoError(t, store.Write())
		disk := mustReadConfig(t, filepath.Join(dir, "config.yaml"))
		assert.Equal(t, "set-by-A", disk.Name)
		assert.Equal(t, 999, disk.Version)
	})

	t.Run("two Sets on same field — second wins", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFromString[testConfig](testFullData(), WithFilenames("config.yaml"), WithPaths(dir))
		require.NoError(t, err)

		require.NoError(t, store.Set([]string{"name"}, "writer-A"))
		require.NoError(t, store.Set([]string{"name"}, "writer-B"))

		assert.Equal(t, "writer-B", mustGet[string](t, store, "name"), "second Set wins")
		assert.Equal(t, 1, mustGet[int](t, store, "version"))
		assert.Equal(t, "node:20", mustGet[string](t, store, "build", "image"))

		// Verify disk round-trip.
		require.NoError(t, store.Write())
		disk := mustReadConfig(t, filepath.Join(dir, "config.yaml"))
		assert.Equal(t, "writer-B", disk.Name, "disk matches second Set")
	})
}

func TestStore_Set_ClearMapPersistsEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Write full config to file first so the store has a real layer.
	require.NoError(t, os.WriteFile(cfgPath, []byte(testFullData()), 0o644))

	store, err := New[testConfig](
		WithFilenames("config.yaml"),
		WithPaths(dir),
	)
	require.NoError(t, err)
	require.NotEmpty(t, mustGet[map[string]string](t, store, "env"), "precondition: env should have values")

	require.NoError(t, store.Set([]string{"env"}, map[string]string{}))
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
	store, err := New[testConfig](
		WithFilenames("config.yaml"),
		WithDefaults(`name: default-app`),
		WithPaths(dir),
	)
	require.NoError(t, err)

	// Set only the name field — build.image and build.target remain "".
	require.NoError(t, store.Set([]string{"name"}, "my-project"))
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
	projectStore, err := New[testConfig](
		WithFilenames("config.yaml"),
		WithDefaults(`name: default-name`),
		WithPaths(projectDir),
	)
	require.NoError(t, err)

	// Set only name — build.image and build.target are untouched (empty).
	require.NoError(t, projectStore.Set([]string{"name"}, "project-override"))
	require.NoError(t, projectStore.WriteTo(filepath.Join(projectDir, "config.yaml")))

	// Now load a layered store: projectDir (high priority) + userDir (low priority).
	mergedStore, err := New[testConfig](
		WithFilenames("config.yaml"),
		WithDefaults(`name: default-name`),
		WithPaths(projectDir, userDir),
	)
	require.NoError(t, err)

	assert.Equal(t, "project-override", mustGet[string](t, mergedStore, "name"),
		"project layer should win for explicitly set fields")
	assert.Equal(t, "node:20", mustGet[string](t, mergedStore, "build", "image"),
		"user layer value should survive — not overridden by empty string from project layer")
	assert.Equal(t, "production", mustGet[string](t, mergedStore, "build", "target"),
		"user layer value should survive — not overridden by empty string from project layer")
}

// TestStore_Set_EmptyStringsPreservedInSlicesAndMaps verifies that the
// empty-string filter only applies to struct fields, not to values inside
// slices or maps where "" is valid data (e.g. env vars, list entries).
func TestStore_Set_EmptyStringsPreservedInSlicesAndMaps(t *testing.T) {
	dir := t.TempDir()
	store, err := New[testConfig](
		WithFilenames("config.yaml"),
		WithDefaults(`name: test`),
		WithPaths(dir),
	)
	require.NoError(t, err)

	require.NoError(t, store.Set([]string{"name"}, "test"))
	require.NoError(t, store.Set([]string{"tags"}, []string{"a", "", "b"})) // empty string in slice
	require.NoError(t, store.Set([]string{"env"}, map[string]string{        // empty string in map value
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
	t.Run("deletes leaf key", func(t *testing.T) {
		store, err := NewFromString[testConfig](testFullData())
		require.NoError(t, err)

		assert.Equal(t, "myproject", mustGet[string](t, store, "name"))

		require.NoError(t, store.Remove("name"))
		requireAbsent(t, store, "name")
	})

	t.Run("deletes nested key", func(t *testing.T) {
		store, err := NewFromString[testConfig](testFullData())
		require.NoError(t, err)

		assert.Equal(t, "node:20", mustGet[string](t, store, "build", "image"))

		require.NoError(t, store.Remove("build", "image"))
		requireAbsent(t, store, "build", "image")
		// Sibling key should survive.
		assert.Equal(t, "production", mustGet[string](t, store, "build", "target"))
	})

	t.Run("missing key returns ErrKeyNotFound", func(t *testing.T) {
		store, err := NewFromString[testConfig](testFullData())
		require.NoError(t, err)

		assert.ErrorIs(t, store.Remove("nonexistent", "path"), ErrKeyNotFound)
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

		store, err := New[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(projectDir, userDir),
		)
		require.NoError(t, err)
		assert.Equal(t, "project-image", mustGet[string](t, store, "build", "image"))

		// Delete build.image from the project file via the tree.
		require.NoError(t, store.Remove("build", "image"))

		// Write only the project layer.
		require.NoError(t, store.Write())

		// Reload — user layer's value should now win.
		fresh, err := New[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(projectDir, userDir),
		)
		require.NoError(t, err)
		assert.Equal(t, "user-image", mustGet[string](t, fresh, "build", "image"),
			"after deleting from project layer, user layer value should show through")
	})
}

func TestStore_Write_RemergesLayers(t *testing.T) {
	t.Run("merged view reflects true merged state after per-layer write", func(t *testing.T) {
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

		store, err := New[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(highDir, lowDir),
		)
		require.NoError(t, err)
		assert.Equal(t, "high-image", mustGet[string](t, store, "build", "image"))

		// Set + Write to the LOW-priority layer — simulates storeui per-layer save.
		require.NoError(t, store.Set([]string{"build", "image"}, "user-wrote-this"))
		require.NoError(t, store.WriteTo(filepath.Join(lowDir, "config.yaml")))

		// Write remerges: the merged view immediately reflects the true merge —
		// high-priority layer wins even though we wrote to the low layer.
		assert.Equal(t, "high-image", mustGet[string](t, store, "build", "image"),
			"after Write, high-priority layer wins (remerge)")

		// Fields only in the low layer survive.
		assert.Equal(t, "from-low", mustGet[string](t, store, "name"))
	})

	t.Run("provenance updated after write to lower layer", func(t *testing.T) {
		highDir := t.TempDir()
		lowDir := t.TempDir()

		require.NoError(t, os.WriteFile(
			filepath.Join(highDir, "config.yaml"),
			[]byte("name: high\n"), 0o644))
		require.NoError(t, os.WriteFile(
			filepath.Join(lowDir, "config.yaml"),
			[]byte("name: low\n"), 0o644))

		store, err := New[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(highDir, lowDir),
		)
		require.NoError(t, err)

		prov, ok := store.Provenance("name")
		require.True(t, ok)
		assert.Equal(t, filepath.Join(highDir, "config.yaml"), prov.Path)

		// Mutate and write to the low layer.
		require.NoError(t, store.Set([]string{"name"}, "changed"))
		require.NoError(t, store.WriteTo(filepath.Join(lowDir, "config.yaml")))

		// Provenance should point back to high layer (it still wins).
		prov, ok = store.Provenance("name")
		require.True(t, ok)
		assert.Equal(t, filepath.Join(highDir, "config.yaml"), prov.Path)
	})
}

func TestStore_Write_RefreshesLayers(t *testing.T) {
	t.Run("layers reflect written values", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "config.yaml"),
			[]byte("name: original\nbuild:\n  image: alpine\n"), 0o644))

		store, err := New[testConfig](
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
		require.NoError(t, store.Set([]string{"build", "image"}, "ubuntu:22.04"))
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

		store, err := New[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(dir),
		)
		require.NoError(t, err)

		require.NoError(t, store.Set([]string{"name"}, "updated-via-topath"))
		require.NoError(t, store.WriteTo(cfgPath))

		layers := store.Layers()
		require.Len(t, layers, 1)
		assert.Equal(t, "updated-via-topath", layers[0].Data["name"],
			"layer data should be refreshed after WriteTo")
	})

	t.Run("provenance is fresh after Write", func(t *testing.T) {
		dir := t.TempDir()
		// First filename = highest priority (like clawker.local.yaml > clawker.yaml).
		localPath := filepath.Join(dir, "local.yaml")
		mainPath := filepath.Join(dir, "main.yaml")

		// local owns "name", main owns "build.image" — split across layers.
		require.NoError(t, os.WriteFile(localPath, []byte("name: from-local\n"), 0o644))
		require.NoError(t, os.WriteFile(mainPath, []byte("build:\n  image: alpine\n"), 0o644))

		store, err := New[testConfig](
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
		require.NoError(t, store.Set([]string{"build", "image"}, "ubuntu"))
		require.NoError(t, store.WriteTo(localPath))

		// After Write, provenance should reflect the new state:
		// "build" now exists in both layers; local (idx 0) wins.
		freshPM := store.ProvenanceMap()
		assert.Equal(t, localPath, freshPM["build"],
			"provenance for 'build' should update to local after Write")

		// The merged value should also be consistent.
		assert.Equal(t, "ubuntu", mustGet[string](t, store, "build", "image"),
			"the merged view should reflect post-Write state")
	})

	t.Run("new file injected into layers after Write", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "main.yaml")

		require.NoError(t, os.WriteFile(existingPath, []byte("name: original\n"), 0o644))

		// local.yaml listed first (highest priority) but doesn't exist on disk yet.
		store, err := New[testConfig](
			WithFilenames("local.yaml", "main.yaml"),
			WithPaths(dir),
		)
		require.NoError(t, err)

		// Only main.yaml discovered (local.yaml doesn't exist yet).
		require.Len(t, store.Layers(), 1)

		// Write to a new file that wasn't in the layer stack.
		newPath := filepath.Join(dir, "local.yaml")
		require.NoError(t, store.Set([]string{"build", "image"}, "ubuntu"))
		require.NoError(t, store.WriteTo(newPath))

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
		result, _ := merge(
			append(layers, layer{path: "", filename: "", node: base, virtual: true, walkUp: false}),
			tags,
		)
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

	result, _ := merge(append(layers, layer{path: "", filename: "", node: base, virtual: true, walkUp: false}), tags)
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

// TestStore_Set_TypedScalarDriftDoesNotFalselyDirtyOpaqueSlice pins the
// store-routing invariant that editing one unrelated scalar never funnels an
// untouched opaque struct-slice into the targeted layer file — even when the
// slice's on-disk representation drifts from its Go type (`port: 22` parses as
// a yaml !!int but the schema field is a Go string).
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

	// local.yaml is listed first, so it is the higher-priority layer.
	store, err := New[testPortRuleCfg](WithFilenames("local.yaml", "base.yaml"), WithPaths(dir))
	require.NoError(t, err)

	// Edit only the top-level scalar, routed explicitly to the local layer
	// (mirrors storeui's per-field save: Set + WriteTo(target)).
	require.NoError(t, store.Set([]string{"name"}, "local-updated"))
	require.NoError(t, store.WriteTo(localPath))

	var localMap map[string]any
	raw, err := os.ReadFile(localPath)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &localMap))

	assert.Equal(t, "local-updated", localMap["name"])
	assert.NotContains(t, localMap, "rules",
		"untouched opaque rules slice must not be routed into the local layer file")
}

// TestStore_Remove_RoutesDeleteToOwningLayer pins the unset path: Remove is
// recorded as a delete and routed to the layer that owns the field, so the key
// leaves that file entirely (never a stale value nor an empty string) and the
// merged value falls through to the lower layer.
func TestStore_Remove_RoutesDeleteToOwningLayer(t *testing.T) {
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

	store, err := New[testConfig](WithFilenames("local.yaml", "base.yaml"), WithPaths(dir))
	require.NoError(t, err)

	// Clear the scalar via Remove; route via provenance (no explicit target).
	require.NoError(t, store.Remove("name"))
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
	assert.Equal(t, "base-name", mustGet[string](t, store, "name"))
}

// TestStore_Set_RejectsSchemaBreakingValue proves the normal Set path validates
// the whole candidate tree before committing. validateKind only guards declared
// leaf keys, so a value grafted at a dynamic map-entry key would otherwise
// produce a tree that no longer decodes, and the next Write would persist it —
// failing the strict load on the next process start.
func TestStore_Set_RejectsSchemaBreakingValue(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(testFullData()), 0o644))

	store, err := New[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
	require.NoError(t, err)
	require.Equal(t, "node:20", mustGet[string](t, store, "build", "image"))

	// env is map[string]string; a struct value at a dynamic entry breaks it.
	err = store.Set([]string{"env", "FOO"}, map[string]string{"nested": "map"})
	require.ErrorIs(t, err, ErrSchemaDecode)

	// A non-leaf key is not a settable field at all.
	require.ErrorIs(t, store.Set([]string{"build"}, "oops"), ErrUnknownKey)

	// The merged tree is untouched by the rejected Sets.
	assert.Equal(t, "node:20", mustGet[string](t, store, "build", "image"))
	requireAbsent(t, store, "env", "FOO")

	// Nothing was marked dirty: Write is a clean no-op and the file is intact.
	require.NoError(t, store.Write())
	reloaded, err := New[testConfig](WithFilenames("config.yaml"), WithPaths(dir))
	require.NoError(t, err)
	assert.Equal(t, "node:20", mustGet[string](t, reloaded, "build", "image"))
}

// TestStore_Set_KindValidation covers the validateKind/kindAccepts guard: a value
// whose Go kind cannot satisfy the schema field is rejected, valid values pass,
// nil clears any field, and non-schema paths bypass the check (migrations).
func TestStore_Set_KindValidation(t *testing.T) {
	cases := []struct {
		name    string
		key     []string
		value   any
		wantErr error
	}{
		{"text accepts string", []string{"name"}, "ok", nil},
		{"text rejects int", []string{"name"}, 5, errKindMismatch},
		{"int accepts int", []string{"version"}, 7, nil},
		{"int rejects string", []string{"version"}, "seven", errKindMismatch},
		{"slice accepts []string", []string{"packages"}, []string{"a"}, nil},
		{"slice rejects string", []string{"packages"}, "a", errKindMismatch},
		{"map accepts map", []string{"env"}, map[string]string{"A": "1"}, nil},
		{"map rejects string", []string{"env"}, "A=1", errKindMismatch},
		{"nil is a caller infraction", []string{"name"}, nil, ErrNilValue},
		{"typed nil is a caller infraction", []string{"packages"}, ([]string)(nil), ErrNilValue},
		{"key outside the schema is rejected", []string{"legacy_field"}, "anything", ErrUnknownKey},
		{"dynamic entry under a map field is allowed", []string{"env", "FOO"}, "1", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewFromString[testConfig](testFullData())
			require.NoError(t, err)
			err = store.Set(tc.key, tc.value)
			switch {
			case tc.wantErr == nil:
				require.NoError(t, err)
			case errors.Is(tc.wantErr, errKindMismatch):
				// The kind check has no sentinel — it is a plain caller error.
				require.Error(t, err)
			default:
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

// errKindMismatch marks a table row expecting the (sentinel-less) kind-check
// rejection rather than a specific storage sentinel.
var errKindMismatch = errors.New("kind mismatch")

// TestStore_Get covers the key-addressed read API directly (it is otherwise
// only exercised indirectly through migrations).
func TestStore_Get(t *testing.T) {
	store, err := NewFromString[testConfig](testFullData())
	require.NoError(t, err)

	assert.Equal(t, "node:20", mustGet[string](t, store, "build", "image"))

	// Absent key → ErrKeyNotFound.
	_, err = Get[string](store, "build", "nonexistent")
	require.ErrorIs(t, err, ErrKeyNotFound)

	// A value that cannot decode into V surfaces the decode error.
	_, err = Get[int](store, "name")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrKeyNotFound)

	// At least one segment is required — there is no whole-tree read.
	_, err = Get[testConfig](store)
	require.Error(t, err)

	// Keys is the non-error existence check.
	assert.Contains(t, store.Keys(), "version")
	assert.NotContains(t, store.Keys(), "does_not_exist")
	assert.ElementsMatch(t, []string{"image", "target"}, store.Keys("build"))
	assert.Empty(t, store.Keys("does", "not", "exist"))
	assert.Empty(t, store.Keys("name"), "a scalar has no child keys")
}

// TestStore_Migrations_RunOnStore covers the storage-level migration runner:
// migrations run against each file layer's own node (legacy key stripped from
// every owning file, not just the merge winner), and a migration whose store
// type does not match T aborts construction instead of being silently skipped.
func TestStore_Migrations_RunOnStore(t *testing.T) {
	dropLegacy := func(s *Store[testConfig]) (bool, error) {
		if !slices.Contains(s.Keys(), "legacy_field") {
			return false, nil
		}
		if err := s.Remove("legacy_field"); err != nil {
			return false, err
		}
		return true, nil
	}

	t.Run("runs per layer and rewrites each owning file", func(t *testing.T) {
		hiDir := t.TempDir()
		loDir := t.TempDir()
		hi := filepath.Join(hiDir, "config.yaml")
		lo := filepath.Join(loDir, "config.yaml")
		require.NoError(t, os.WriteFile(hi, []byte("name: hi\nlegacy_field: gone\n"), 0o644))
		require.NoError(t, os.WriteFile(lo, []byte("name: lo\nversion: 3\nlegacy_field: gone\n"), 0o644))

		store, err := New[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(hiDir, loDir),
			WithMigrations(dropLegacy),
		)
		require.NoError(t, err)
		assert.Equal(t, "hi", mustGet[string](t, store, "name"))
		assert.Equal(t, 3, mustGet[int](t, store, "version"))

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
		_, err := New[testConfig](
			WithFilenames("config.yaml"),
			WithPaths(dir),
			WithMigrations(bad),
		)
		require.ErrorIs(t, err, ErrMigrationType)
	})
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
				require.ErrorIs(t, err, ErrNonMappingRoot)
				require.Nil(t, node)
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

func TestValidateKey(t *testing.T) {
	for _, ok := range [][]string{{"name"}, {"build", "image"}, {"a", "b", "c"}, {"aliases", "a.b"}} {
		require.NoError(t, validateKey(ok), "key %q must be accepted", ok)
	}
	for _, bad := range [][]string{nil, {}, {""}, {"build", ""}, {"", "build"}} {
		require.Error(t, validateKey(bad), "key %q must be rejected", bad)
	}
}

func TestStore_SetRemove_RejectMalformedKey(t *testing.T) {
	store, err := NewFromString[testConfig](testFullData())
	require.NoError(t, err)

	for _, bad := range [][]string{{}, {""}, {"build", ""}} {
		require.Error(t, store.Set(bad, "x"), "Set(%q) must be rejected", bad)
		require.Error(t, store.Remove(bad...), "Remove(%q) must be rejected", bad)
	}

	// A well-formed key still works — the guard rejects only malformed input.
	require.NoError(t, store.Set([]string{"name"}, "ok"))
}

// --- Hardening regression tests ---
//
// Each test below pins one hardening fix: defaults never leak into user files,
// writes merge into current on-disk state (under the flock), construction is
// loud on corruption, migrations are persisted from the engine's own dirty
// tracking, multi-document YAML is rejected, unset keys round-trip, and
// cloneNode yields self-contained trees.

// hardSchema is the schema for hardening regression tests: one plain field and
// two defaulted fields so virtual-layer (defaults) behavior is exercised.
type hardSchema struct {
	Name  string `yaml:"name"  label:"Name"  desc:"n"`
	Mode  string `yaml:"mode"  label:"Mode"  desc:"m" default:"bind"`
	Count int    `yaml:"count" label:"Count" desc:"c" default:"7"`
}

//nolint:ireturn // storage.Schema mandates returning the FieldSet interface.
func (s hardSchema) Fields() FieldSet { return NormalizeFields(s) }

// altSchema exists only to build a Migration with the wrong store type.
type altSchema struct {
	Other string `yaml:"other" label:"Other" desc:"o"`
}

//nolint:ireturn // storage.Schema mandates returning the FieldSet interface.
func (s altSchema) Fields() FieldSet { return NormalizeFields(s) }

func writeHardFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func newHardStore(t *testing.T, dir string, opts ...Option) *Store[hardSchema] {
	t.Helper()
	base := []Option{WithFilenames("cfg.yaml"), WithPaths(dir)}
	s, err := New[hardSchema](append(base, opts...)...)
	require.NoError(t, err)
	return s
}

// A provenance-routed Write must persist only explicit mutations — never
// materialize schema defaults (virtual layer) into the user's file. Defaults
// pinned into the file would shadow future binary default changes forever.
func TestWrite_DoesNotFlushDefaultsToFile(t *testing.T) {
	dir := t.TempDir()
	file := writeHardFile(t, dir, "cfg.yaml", "name: alice\n")

	s := newHardStore(t, dir, WithDefaultsFromStruct[hardSchema]())
	require.NoError(t, s.Set([]string{"name"}, "bob"))
	require.NoError(t, s.Write())

	data, err := os.ReadFile(file)
	require.NoError(t, err)
	// Byte-exact: the routed file is the original with only the dirtied key
	// updated — the entire virtual layer (mode/count defaults) stays out.
	assert.Equal(t, "name: bob\n", string(data), "routed write must contain only dirtied keys")

	// Defaults still apply through the merged view on a fresh load.
	s2 := newHardStore(t, dir, WithDefaultsFromStruct[hardSchema]())
	assert.Equal(t, "bind", mustGet[string](t, s2, "mode"), "defaults lost on reload")
	assert.Equal(t, 7, mustGet[int](t, s2, "count"), "defaults lost on reload")
}

// A targeted WriteTo (storeui saving one field to a chosen layer file) must not
// dump defaults either — seed flushing is opt-in via MarkSeedForWrite.
func TestWriteTargeted_DoesNotFlushDefaults(t *testing.T) {
	dir := t.TempDir()
	file := writeHardFile(t, dir, "cfg.yaml", "name: alice\n")

	s := newHardStore(t, dir, WithDefaultsFromStruct[hardSchema]())
	require.NoError(t, s.Set([]string{"name"}, "bob"))
	require.NoError(t, s.WriteTo(file))

	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, "name: bob\n", string(data), "targeted write must contain only dirtied keys")
}

// MarkSeedForWrite is the explicit opt-in for the preset flow: flush every
// never-persisted virtual-layer field (seed + defaults) on the next Write.
func TestMarkSeedForWrite_FlushesSeedAndDefaults(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "new.yaml")

	s, err := NewFromString[hardSchema]("name: alice", WithDefaultsFromStruct[hardSchema]())
	require.NoError(t, err)
	s.MarkSeedForWrite()
	require.NoError(t, s.WriteTo(dest))

	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	got := string(data)
	for _, want := range []string{"name: alice", "mode: bind", "count: 7"} {
		assert.Contains(t, got, want, "missing preset value")
	}
}

// Without MarkSeedForWrite, a seed-only store has nothing dirty: Write is a
// no-op and creates no file.
func TestWrite_SeedOnlyStoreIsClean(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "new.yaml")

	s, err := NewFromString[hardSchema]("name: alice", WithDefaultsFromStruct[hardSchema]())
	require.NoError(t, err)
	require.NoError(t, s.WriteTo(dest))

	_, statErr := os.Stat(dest)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "clean store created a file")
}

// WriteTo against an existing file the store never discovered must merge into
// it — preserving its other keys and comments — not clobber it wholesale.
func TestWriteTo_MergesIntoExistingExternalFile(t *testing.T) {
	dir := t.TempDir()
	ext := writeHardFile(t, dir, "ext.yaml", "# precious comment\nmode: snapshot\n")

	s, err := NewFromString[hardSchema]("name: alice")
	require.NoError(t, err)
	s.MarkSeedForWrite()
	require.NoError(t, s.WriteTo(ext))

	data, err := os.ReadFile(ext)
	require.NoError(t, err)
	got := string(data)
	assert.Contains(t, got, "name: alice", "written field missing")
	assert.Contains(t, got, "mode: snapshot", "existing key clobbered")
	assert.Contains(t, got, "# precious comment", "existing comment lost")
}

// Two stores on the same file (two processes): each sets a disjoint field and
// writes. The second write must not revert the first's update — the write path
// re-reads the file under the lock instead of trusting its stale in-memory node.
func TestWrite_CrossStoreNoLostUpdate(t *testing.T) {
	dir := t.TempDir()
	file := writeHardFile(t, dir, "cfg.yaml", "name: one\nmode: two\n")

	s1 := newHardStore(t, dir, WithLock())
	s2 := newHardStore(t, dir, WithLock())

	require.NoError(t, s1.Set([]string{"name"}, "ONE-UPDATED"))
	require.NoError(t, s1.Write())
	require.NoError(t, s2.Set([]string{"mode"}, "TWO-UPDATED"))
	require.NoError(t, s2.Write())

	data, err := os.ReadFile(file)
	require.NoError(t, err)
	got := string(data)
	assert.Contains(t, got, "name: ONE-UPDATED", "s2's write reverted s1's update")
	assert.Contains(t, got, "mode: TWO-UPDATED", "s2's own write missing")
}

// Construction must surface a layer that no longer parses — silently dropping
// it would revert every field it owned to defaults.
func TestNew_CorruptLayerErrors(t *testing.T) {
	dir := t.TempDir()
	// Sequence root is not a mapping.
	writeHardFile(t, dir, "cfg.yaml", "- not\n- a\n- mapping\n")

	_, err := New[hardSchema](WithFilenames("cfg.yaml"), WithPaths(dir))
	require.Error(t, err, "construction silently accepted a corrupt layer")
	assert.ErrorIs(t, err, ErrNonMappingRoot)
}

// A migration that mutates the layer but self-reports changed=false must still
// be persisted — the engine trusts its own dirty tracking, not the return value.
func TestMigrations_SelfReportFalseStillPersists(t *testing.T) {
	dir := t.TempDir()
	file := writeHardFile(t, dir, "cfg.yaml", "name: alice\nlegacy: gone\n")

	lyingMigration := func(s *Store[hardSchema]) (bool, error) {
		if err := s.Remove("legacy"); err != nil {
			return false, err
		}
		return false, nil // lies: it changed the layer
	}
	s, err := New[hardSchema](
		WithFilenames("cfg.yaml"),
		WithPaths(dir),
		WithMigrations(lyingMigration),
	)
	require.NoError(t, err)
	assert.Equal(t, "alice", mustGet[string](t, s, "name"), "merged tree corrupted by migration")

	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "legacy",
		"mutation by self-report-false migration not persisted")
}

// A migration typed for the wrong schema aborts construction even when no file
// layers exist — the programming error must not hide until a file appears.
func TestNew_MigrationTypeMismatchWithoutFileLayers(t *testing.T) {
	wrongType := func(s *Store[altSchema]) (bool, error) { return false, nil }
	_, err := New[hardSchema](WithMigrations(wrongType))
	require.Error(t, err, "mismatched migration accepted on in-memory store")
	assert.ErrorIs(t, err, ErrMigrationType)
}

// Multi-document YAML is rejected: config files are single-document, and
// silently using only the first document would drop the rest.
func TestNew_MultiDocumentRejected(t *testing.T) {
	dir := t.TempDir()
	writeHardFile(t, dir, "cfg.yaml", "name: alice\n---\nname: bob\n")

	_, err := New[hardSchema](WithFilenames("cfg.yaml"), WithPaths(dir))
	require.Error(t, err, "multi-document yaml accepted")
	assert.ErrorIs(t, err, ErrMultiDocument)
}

// cloneNode must produce a self-contained tree: an alias in the clone resolves
// through the CLONED anchor, not back into the original tree. Without alias
// remapping, mutating the original anchor after cloning changes what the
// clone's alias decodes to.
func TestCloneNode_RemapsAliasPointers(t *testing.T) {
	root, err := rootMapping([]byte("shared: &d\n  x: 1\nother: *d\n"))
	require.NoError(t, err)
	clone := cloneNode(root)

	// Mutate the ORIGINAL anchor's content in place.
	shared, ok := mappingValue(root, "shared")
	require.True(t, ok, "anchor node missing")
	xVal, ok := mappingValue(shared, "x")
	require.True(t, ok, "anchored key missing")
	xVal.Value = "999"

	var decoded struct {
		Other map[string]int `yaml:"other"`
	}
	require.NoError(t, clone.Decode(&decoded))
	assert.Equal(t, 1, decoded.Other["x"],
		"clone's alias resolved through the mutated original")
}

// WriteTo requires an absolute path — a relative path would resolve against
// whatever CWD the process happens to have.
func TestWriteTo_RelativePathRejected(t *testing.T) {
	t.Chdir(t.TempDir()) // a regressed guard writes here, not into the package dir
	s, err := NewFromString[hardSchema]("name: alice")
	require.NoError(t, err)
	assert.Error(t, s.WriteTo("relative/cfg.yaml"), "WriteTo accepted a relative path")
}

// WriteFieldTo flushes ONLY the named dirty field — every other dirty field
// stays staged and routes to its own destination on a later Write/WriteTo.
// This is the per-field save primitive: a seed-marked preset store (project
// init) must never dump its full seed into a user-chosen destination file.
func TestWriteFieldTo_FlushesOnlyThatField(t *testing.T) {
	dir := t.TempDir()
	userFile := filepath.Join(dir, "user.yaml")
	projFile := filepath.Join(dir, "proj.yaml")

	s, err := NewFromString[hardSchema]("name: alice\nmode: snapshot")
	require.NoError(t, err)
	s.MarkSeedForWrite()

	require.NoError(t, s.WriteFieldTo(userFile, "mode"))

	userData, err := os.ReadFile(userFile)
	require.NoError(t, err)
	assert.Contains(t, string(userData), "mode: snapshot", "flushed field missing from target")
	assert.NotContains(t, string(userData), "name:", "unrelated dirty field dumped into target")

	// The rest of the seed is still dirty and lands in the project file.
	require.NoError(t, s.WriteTo(projFile))
	projData, err := os.ReadFile(projFile)
	require.NoError(t, err)
	assert.Contains(t, string(projData), "name: alice", "remaining dirty field lost")
	assert.NotContains(t, string(projData), "mode:", "already-flushed field re-routed to project file")
}

// A staged Set on another field must survive WriteFieldTo's post-write
// remerge (the tree is rebuilt from layer data; a bare staged value is in no
// layer) and still persist on the next Write.
func TestWriteFieldTo_StagedSetSurvives(t *testing.T) {
	dir := t.TempDir()
	file := writeHardFile(t, dir, "cfg.yaml", "name: alice\nmode: snapshot\n")
	other := filepath.Join(dir, "other.yaml")

	s := newHardStore(t, dir)
	require.NoError(t, s.Set([]string{"name"}, "staged-value"))
	require.NoError(t, s.Set([]string{"mode"}, "bind"))

	require.NoError(t, s.WriteFieldTo(other, "mode"))
	assert.Equal(t, "staged-value", mustGet[string](t, s, "name"), "staged Set reverted by remerge")

	require.NoError(t, s.Write())
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: staged-value", "staged Set not persisted after partial flush")
}

// A staged Remove on another field must also survive the remerge: the merged
// view keeps showing the field as gone and the next Write still deletes it.
func TestWriteFieldTo_StagedRemoveSurvives(t *testing.T) {
	dir := t.TempDir()
	file := writeHardFile(t, dir, "cfg.yaml", "name: alice\nmode: snapshot\n")
	other := filepath.Join(dir, "other.yaml")

	s := newHardStore(t, dir)
	require.NoError(t, s.Remove("name"))
	require.NoError(t, s.Set([]string{"mode"}, "bind"))

	require.NoError(t, s.WriteFieldTo(other, "mode"))
	requireAbsent(t, s, "name")

	require.NoError(t, s.Write())
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "name:", "staged Remove not persisted after partial flush")
}

// WriteFieldTo with a staged delete removes the field from the target file
// and leaves the file's other keys intact.
func TestWriteFieldTo_Delete(t *testing.T) {
	dir := t.TempDir()
	file := writeHardFile(t, dir, "cfg.yaml", "name: alice\nmode: snapshot\n")

	s := newHardStore(t, dir)
	require.NoError(t, s.Remove("mode"))

	require.NoError(t, s.WriteFieldTo(file, "mode"))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "mode:", "deleted field still in file")
	assert.Contains(t, string(data), "name: alice", "unrelated key clobbered")
}

// A clean field is a no-op: no file is created, no error returned.
func TestWriteFieldTo_CleanFieldNoOp(t *testing.T) {
	dir := t.TempDir()
	writeHardFile(t, dir, "cfg.yaml", "name: alice\n")
	target := filepath.Join(dir, "target.yaml")

	s := newHardStore(t, dir)
	require.NoError(t, s.WriteFieldTo(target, "name"))
	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "no-op WriteFieldTo created the target file")
}

func TestWriteFieldTo_RelativePathRejected(t *testing.T) {
	t.Chdir(t.TempDir()) // a regressed guard writes here, not into the package dir
	s, err := NewFromString[hardSchema]("name: alice")
	require.NoError(t, err)
	s.MarkSeedForWrite()
	assert.Error(t, s.WriteFieldTo("relative/cfg.yaml", "name"), "WriteFieldTo accepted a relative path")
}

// A file using YAML anchors/aliases survives an unrelated field write: the
// anchor structure is preserved in the rewritten bytes and the aliased values
// still decode correctly. (End-to-end YAML-structure fidelity; the cloneNode
// alias-remapping contract itself is pinned by TestCloneNode_RemapsAliasPointers.)
func TestWrite_PreservesAnchorsAndAliases(t *testing.T) {
	dir := t.TempDir()
	file := writeHardFile(t, dir, "cfg.yaml",
		"shared: &d\n  x: 1\nother: *d\nname: alice\n")

	s := newHardStore(t, dir)
	require.NoError(t, s.Set([]string{"name"}, "bob"))
	require.NoError(t, s.Write())

	data, err := os.ReadFile(file)
	require.NoError(t, err)
	got := string(data)
	assert.Contains(t, got, "&d", "anchor lost")
	assert.Contains(t, got, "*d", "alias lost")

	// The rewritten file must still parse and resolve the alias.
	var decoded struct {
		Other map[string]int `yaml:"other"`
		Name  string         `yaml:"name"`
	}
	require.NoError(t, yaml.Unmarshal(data, &decoded))
	assert.Equal(t, 1, decoded.Other["x"], "aliased value corrupted")
	assert.Equal(t, "bob", decoded.Name)
}

// --- Unset vs set-empty ---

// A bare `key:` (YAML null) is UNSET: the merge skips it in every case, so the
// lower layer — and failing that the schema default — shows through. An
// explicit empty (`key: ""`, `key: []`) is a real value that WINS the merge.
func TestStore_UnsetVsSetEmpty(t *testing.T) {
	cases := []struct {
		name     string
		highYAML string
		wantName string
		wantTags []string
	}{
		{
			name:     "bare key is ignored by the merge",
			highYAML: "name:\ntags:\n",
			wantName: "from-low",
			wantTags: []string{"low"},
		},
		{
			name:     "explicit null is ignored by the merge",
			highYAML: "name: null\ntags: ~\n",
			wantName: "from-low",
			wantTags: []string{"low"},
		},
		{
			name:     "explicit empty wins the merge",
			highYAML: `name: ""` + "\ntags: []\n",
			wantName: "",
			wantTags: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			highDir := t.TempDir()
			lowDir := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(highDir, "config.yaml"), []byte(tc.highYAML), 0o644))
			require.NoError(t, os.WriteFile(
				filepath.Join(lowDir, "config.yaml"), []byte("name: from-low\ntags:\n  - low\n"), 0o644))

			store, err := New[testConfig](WithFilenames("config.yaml"), WithPaths(highDir, lowDir))
			require.NoError(t, err)

			assert.Equal(t, tc.wantName, mustGet[string](t, store, "name"))
			assert.Equal(t, tc.wantTags, mustGet[[]string](t, store, "tags"))
		})
	}
}

// A bare key in the only layer leaves the field unset entirely: the schema
// default shows through, Get reports ErrKeyNotFound when there is none, and
// Keys never lists it.
func TestStore_UnsetFallsThroughToDefaults(t *testing.T) {
	dir := t.TempDir()
	writeHardFile(t, dir, "cfg.yaml", "name: alice\nmode:\n")

	s := newHardStore(t, dir, WithDefaultsFromStruct[hardSchema]())
	assert.Equal(t, "bind", mustGet[string](t, s, "mode"), "bare key must not mask the default")

	// Without defaults the key is simply absent — never an empty string.
	bare := newHardStore(t, dir)
	requireAbsent(t, bare, "mode")
	assert.NotContains(t, bare.Keys(), "mode", "Keys must not list an unset key")
}

// A bare key survives on disk: only the merge ignores it, so a write that
// touches a different field leaves the user's `key:` line intact.
func TestWrite_PreservesBareKeys(t *testing.T) {
	dir := t.TempDir()
	file := writeHardFile(t, dir, "cfg.yaml", "name: alice\nmode:\n")

	s := newHardStore(t, dir)
	require.NoError(t, s.Set([]string{"name"}, "bob"))
	require.NoError(t, s.Write())

	assert.Equal(t, "name: bob\nmode:\n", mustReadFile(t, file),
		"the bare key must round-trip untouched")
}

// --- Dotted key names ---

// aliasSchema mirrors an aliases-style config: a dynamic string map whose entry
// names routinely contain literal dots.
type aliasSchema struct {
	Aliases map[string]string `yaml:"aliases" label:"Aliases" desc:"a"`
}

//nolint:ireturn // storage.Schema mandates returning the FieldSet interface.
func (s aliasSchema) Fields() FieldSet { return NormalizeFields(s) }

// A map entry whose name contains a literal dot is addressed exactly: it
// round-trips through Set/Get/Keys without being reparsed as nesting, and it
// persists as one key line rather than a nested mapping.
func TestStore_DottedKeyName(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cfg.yaml")

	s, err := New[aliasSchema](WithFilenames("cfg.yaml"), WithPaths(dir))
	require.NoError(t, err)

	require.NoError(t, s.Set([]string{"aliases", "a.b"}, "x"))
	assert.Equal(t, "x", mustGet[string](t, s, "aliases", "a.b"))
	assert.Equal(t, []string{"a.b"}, s.Keys("aliases"), "the dot must not have become nesting")
	requireAbsent(t, s, "aliases", "a")

	require.NoError(t, s.Write())
	assert.Equal(t, "aliases:\n  a.b: x\n", mustReadFile(t, file),
		"dotted key must persist as one key, never as nesting")

	// The written file reloads as the same single entry.
	reloaded, err := New[aliasSchema](WithFilenames("cfg.yaml"), WithPaths(dir))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a.b": "x"}, mustGet[map[string]string](t, reloaded, "aliases"))
}
