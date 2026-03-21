package storage

import (
	"bytes"
	"fmt"
	"maps"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- deterministic seeds for oracle/golden tests
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	tree "github.com/a8m/tree"
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
	Plugins  []string          `yaml:"plugins" merge:"overwrite"`
	Tags     []string          `yaml:"tags"`
	Env      map[string]string `yaml:"env"`
}

func (t testConfig) Fields() FieldSet { return NormalizeFields(t) }

type testBuild struct {
	Image  string `yaml:"image"`
	Target string `yaml:"target"`
}

// Test types for merge union edge cases (promoted from local types to support Schema constraint).
type testUnionMapCfg struct {
	Items []map[string]string `yaml:"items" merge:"union"`
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
	migrationPath := filepath.Join(tempDir, "migration.yaml")

	err := os.WriteFile(fullPath, []byte(testFullData()), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(partialPath, []byte(testPartialData()), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(invalidPath, []byte(testInvalidData()), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(emptyPath, []byte(""), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(migrationPath, []byte(testFullData()), 0o644)
	require.NoError(t, err)

	tests := []struct {
		name         string
		path         string
		migrations   []Migration
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
		{
			name: "migration transforms data",
			path: migrationPath,
			migrations: []Migration{
				func(raw map[string]any) bool {
					raw["name"] = "migrated"
					return true
				},
			},
			wantName:     "migrated",
			wantVersion:  1,
			wantImage:    "node:20",
			wantPackages: []any{"git", "curl"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := loadFile(tt.path, tt.migrations)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

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
	raw, err := loadFile(path, nil)
	require.NoError(t, err)
	return raw, path
}

// mustReadConfig loads a YAML file and unmarshals to testConfig for assertions.
func mustReadConfig(t *testing.T, path string) *testConfig {
	t.Helper()
	raw, err := loadFile(path, nil)
	require.NoError(t, err)
	cfg, err := unmarshal[testConfig](raw)
	require.NoError(t, err)
	return cfg
}

func TestStore_Merge(t *testing.T) {
	tempDir := t.TempDir()
	tags := buildTagRegistry[testConfig]()

	defaults, _ := mustLoadTestMap(t, tempDir, "defaults.yaml", testDefaultsData())
	full, fullPath := mustLoadTestMap(t, tempDir, "full.yaml", testFullData())
	override, overridePath := mustLoadTestMap(t, tempDir, "override.yaml", testOverrideData())
	partial, partialPath := mustLoadTestMap(t, tempDir, "partial.yaml", testPartialData())

	tests := []struct {
		name         string
		base         map[string]any
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
				{path: fullPath, filename: "full.yaml", data: full},
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
				{path: overridePath, filename: "override.yaml", data: override},
				{path: fullPath, filename: "full.yaml", data: full},
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
			// map merge: all keys, override wins conflicts
			wantEnv: map[string]string{"APP_ENV": "development", "LOG_LEVEL": "info", "DEBUG": "true"},
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
				{path: partialPath, filename: "partial.yaml", data: partial},
			},
			wantName:     "myproject",
			wantImage:    "node:20",
			wantPackages: []string{"git"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, prov := merge(tt.base, tt.layers, tags)
			require.NotNil(t, result)

			// Unmarshal the merged map for typed assertions.
			cfg, err := unmarshal[testConfig](result)
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
	tests := []struct {
		name        string
		initial     string
		configPaths bool
		lock        bool
		setFn       func(*testConfig)
		wantErr     bool
		wantNoFile  bool
		wantName    string
		wantVersion int
		wantImage   string
	}{
		{
			name:        "set and write persists to disk",
			initial:     testFullData(),
			configPaths: true,
			setFn: func(c *testConfig) {
				c.Name = "updated"
				c.Version = 99
			},
			wantName:    "updated",
			wantVersion: 99,
			wantImage:   "node:20",
		},
		{
			name:        "write is no-op when clean",
			initial:     testFullData(),
			configPaths: true,
			wantNoFile:  true,
		},
		{
			name:    "write fails without paths",
			initial: testFullData(),
			setFn: func(c *testConfig) {
				c.Name = "nope"
			},
			wantErr: true,
		},
		{
			name:        "write with lock",
			initial:     testFullData(),
			configPaths: true,
			lock:        true,
			setFn: func(c *testConfig) {
				c.Name = "locked-write"
			},
			wantName:    "locked-write",
			wantVersion: 1,
			wantImage:   "node:20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writePath := filepath.Join(dir, "config.yaml")

			store, err := NewFromString[testConfig](tt.initial)
			require.NoError(t, err)

			if tt.configPaths {
				store.opts.paths = []string{dir}
				store.opts.filenames = []string{"config.yaml"}
			}
			if tt.lock {
				store.opts.lock = true
			}

			if tt.setFn != nil {
				store.Set(tt.setFn)
			}

			err = store.Write()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.wantNoFile {
				_, statErr := os.Stat(writePath)
				assert.True(t, os.IsNotExist(statErr))
				return
			}

			result := mustReadConfig(t, writePath)
			assert.Equal(t, tt.wantName, result.Name)
			assert.Equal(t, tt.wantVersion, result.Version)
			assert.Equal(t, tt.wantImage, result.Build.Image)
		})
	}
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

	globalData, err := loadFile(globalPath, nil)
	require.NoError(t, err)
	localData, err := loadFile(localPath, nil)
	require.NoError(t, err)

	layers := []layer{
		{path: localPath, filename: "local.yaml", data: localData},
		{path: globalPath, filename: "global.yaml", data: globalData},
	}

	tags := buildTagRegistry[testConfig]()
	basePath := filepath.Join(dir, "base.yaml")
	require.NoError(t, os.WriteFile(basePath, []byte(testPartialData()), 0o644))
	base, err := loadFile(basePath, nil)
	require.NoError(t, err)

	tree, prov := merge(base, layers, tags)

	// Deserialize for Set.
	value, err := unmarshal[testConfig](tree)
	require.NoError(t, err)

	store := &Store[testConfig]{
		tree:   tree,
		layers: layers,
		prov:   prov,
		tags:   tags,
		opts:   options{filenames: []string{"global.yaml", "local.yaml"}},
	}
	store.value.Store(value)

	require.NoError(t, store.Set(func(c *testConfig) {
		c.Name = "provenance-test"
	}))
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

	globalData, err := loadFile(globalPath, nil)
	require.NoError(t, err)
	localData, err := loadFile(localPath, nil)
	require.NoError(t, err)

	layers := []layer{
		{path: localPath, filename: "local.yaml", data: localData},
		{path: globalPath, filename: "global.yaml", data: globalData},
	}

	tags := buildTagRegistry[testConfig]()
	tree, prov := merge(nil, layers, tags)

	value, err := unmarshal[testConfig](tree)
	require.NoError(t, err)

	store := &Store[testConfig]{
		tree:   tree,
		layers: layers,
		prov:   prov,
		tags:   tags,
		opts:   options{filenames: []string{"global.yaml", "local.yaml"}},
	}
	store.value.Store(value)

	require.NoError(t, store.Set(func(c *testConfig) {
		c.Name = "local-updated"
		c.Tags = []string{"global-updated"}
	}))
	require.NoError(t, store.Write())

	localResult := mustReadConfig(t, localPath)
	globalResult := mustReadConfig(t, globalPath)

	assert.Equal(t, "local-updated", localResult.Name)
	assert.Nil(t, localResult.Tags, "tags should not be routed to local layer")

	assert.Equal(t, "global", globalResult.Name, "name should not be routed to global layer")
	assert.Equal(t, []string{"global-updated"}, globalResult.Tags)
}

func TestStore_WriteFilename(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	localPath := filepath.Join(dir, "config.local.yaml")

	// Create a store with two filenames configured.
	err := os.WriteFile(configPath, []byte(testFullData()), 0o644)
	require.NoError(t, err)

	configData, err := loadFile(configPath, nil)
	require.NoError(t, err)

	tags := buildTagRegistry[testConfig]()
	tree, prov := merge(nil, []layer{
		{path: configPath, filename: "config.yaml", data: configData},
	}, tags)

	value, err := unmarshal[testConfig](tree)
	require.NoError(t, err)

	store := &Store[testConfig]{
		tree: tree,
		layers: []layer{
			{path: configPath, filename: "config.yaml", data: configData},
		},
		prov: prov,
		tags: tags,
		opts: options{
			filenames: []string{"config.yaml", "config.local.yaml"},
			paths:     []string{dir},
		},
	}
	store.value.Store(value)

	require.NoError(t, store.Set(func(c *testConfig) {
		c.Name = "targeted-write"
	}))

	// Write to explicit filename — should create config.local.yaml.
	require.NoError(t, store.Write("config.local.yaml"))

	localResult := mustReadConfig(t, localPath)
	assert.Equal(t, "targeted-write", localResult.Name)
	assert.Equal(t, 1, localResult.Version) // full value written to target
}

func TestStore_ConfigDir(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		onlyWindows bool
		env         map[string]string
		output      string
	}{
		{
			name: "HOME specified",
			env: map[string]string{
				"CLAWKER_CONFIG_DIR": "",
				"XDG_CONFIG_HOME":    "",
				"HOME":               tempDir,
			},
			output: filepath.Join(tempDir, ".config", "clawker"),
		},
		{
			name: "CLAWKER_CONFIG_DIR specified",
			env: map[string]string{
				"CLAWKER_CONFIG_DIR": tempDir,
			},
			output: tempDir,
		},
		{
			name: "XDG_CONFIG_HOME specified",
			env: map[string]string{
				"CLAWKER_CONFIG_DIR": "",
				"XDG_CONFIG_HOME":    tempDir,
			},
			output: filepath.Join(tempDir, "clawker"),
		},
		{
			name: "CLAWKER_CONFIG_DIR wins over XDG_CONFIG_HOME",
			env: map[string]string{
				"CLAWKER_CONFIG_DIR": tempDir,
				"XDG_CONFIG_HOME":    tempDir,
			},
			output: tempDir,
		},
		{
			name:        "AppData specified",
			onlyWindows: true,
			env: map[string]string{
				"CLAWKER_CONFIG_DIR": "",
				"XDG_CONFIG_HOME":    "",
				"AppData":            tempDir,
			},
			output: filepath.Join(tempDir, "clawker"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.onlyWindows && runtime.GOOS != "windows" {
				t.Skip("windows only")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			assert.Equal(t, tt.output, configDir())
		})
	}
}

func TestStore_DataDir(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		onlyWindows bool
		env         map[string]string
		output      string
	}{
		{
			name: "HOME specified",
			env: map[string]string{
				"CLAWKER_DATA_DIR": "",
				"XDG_DATA_HOME":    "",
				"HOME":             tempDir,
			},
			output: filepath.Join(tempDir, ".local", "share", "clawker"),
		},
		{
			name: "CLAWKER_DATA_DIR specified",
			env: map[string]string{
				"CLAWKER_DATA_DIR": tempDir,
			},
			output: tempDir,
		},
		{
			name: "XDG_DATA_HOME specified",
			env: map[string]string{
				"CLAWKER_DATA_DIR": "",
				"XDG_DATA_HOME":    tempDir,
			},
			output: filepath.Join(tempDir, "clawker"),
		},
		{
			name: "CLAWKER_DATA_DIR wins over XDG_DATA_HOME",
			env: map[string]string{
				"CLAWKER_DATA_DIR": tempDir,
				"XDG_DATA_HOME":    tempDir,
			},
			output: tempDir,
		},
		{
			name:        "LOCALAPPDATA specified",
			onlyWindows: true,
			env: map[string]string{
				"CLAWKER_DATA_DIR": "",
				"XDG_DATA_HOME":    "",
				"LOCALAPPDATA":     tempDir,
			},
			output: filepath.Join(tempDir, "clawker"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.onlyWindows && runtime.GOOS != "windows" {
				t.Skip("windows only")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			assert.Equal(t, tt.output, dataDir())
		})
	}
}

func TestStore_StateDir(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		onlyWindows bool
		env         map[string]string
		output      string
	}{
		{
			name: "HOME specified",
			env: map[string]string{
				"CLAWKER_STATE_DIR": "",
				"XDG_STATE_HOME":    "",
				"HOME":              tempDir,
			},
			output: filepath.Join(tempDir, ".local", "state", "clawker"),
		},
		{
			name: "CLAWKER_STATE_DIR specified",
			env: map[string]string{
				"CLAWKER_STATE_DIR": tempDir,
			},
			output: tempDir,
		},
		{
			name: "XDG_STATE_HOME specified",
			env: map[string]string{
				"CLAWKER_STATE_DIR": "",
				"XDG_STATE_HOME":    tempDir,
			},
			output: filepath.Join(tempDir, "clawker"),
		},
		{
			name: "CLAWKER_STATE_DIR wins over XDG_STATE_HOME",
			env: map[string]string{
				"CLAWKER_STATE_DIR": tempDir,
				"XDG_STATE_HOME":    tempDir,
			},
			output: tempDir,
		},
		{
			name:        "AppData specified",
			onlyWindows: true,
			env: map[string]string{
				"CLAWKER_STATE_DIR": "",
				"XDG_STATE_HOME":    "",
				"AppData":           tempDir,
			},
			output: filepath.Join(tempDir, "clawker", "state"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.onlyWindows && runtime.GOOS != "windows" {
				t.Skip("windows only")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			assert.Equal(t, tt.output, stateDir())
		})
	}
}

func TestStore_CacheDir(t *testing.T) {
	expectedCacheDir := "/expected-cache-dir"
	unexpectedCacheDir := "/unexpected-cache-dir"

	tests := []struct {
		name        string
		onlyWindows bool
		env         map[string]string
		output      string
	}{
		{
			name: "CLAWKER_CACHE_DIR is highest precedence",
			env: map[string]string{
				"CLAWKER_CACHE_DIR": expectedCacheDir,
				"XDG_CACHE_HOME":    unexpectedCacheDir,
				"LOCALAPPDATA":      unexpectedCacheDir,
				"HOME":              unexpectedCacheDir,
			},
			output: expectedCacheDir,
		},
		{
			name: "XDG_CACHE_HOME over home dir",
			env: map[string]string{
				"CLAWKER_CACHE_DIR": "",
				"XDG_CACHE_HOME":    expectedCacheDir,
				"LOCALAPPDATA":      unexpectedCacheDir,
				"HOME":              unexpectedCacheDir,
			},
			output: filepath.Join(expectedCacheDir, "clawker"),
		},
		{
			name:        "on windows, LOCALAPPDATA is preferred to home dir",
			onlyWindows: true,
			env: map[string]string{
				"CLAWKER_CACHE_DIR": "",
				"XDG_CACHE_HOME":    "",
				"LOCALAPPDATA":      expectedCacheDir,
				"HOME":              unexpectedCacheDir,
			},
			output: filepath.Join(expectedCacheDir, "clawker", "cache"),
		},
		{
			name: "falls back to home dir cache",
			env: map[string]string{
				"CLAWKER_CACHE_DIR": "",
				"XDG_CACHE_HOME":    "",
				"LOCALAPPDATA":      "",
				"HOME":              expectedCacheDir,
			},
			output: filepath.Join(expectedCacheDir, ".cache", "clawker"),
		},
		{
			name: "falls back to tmpdir when no home",
			env: map[string]string{
				"CLAWKER_CACHE_DIR": "",
				"XDG_CACHE_HOME":    "",
				"LOCALAPPDATA":      "",
				"HOME":              "",
			},
			output: filepath.Join(os.TempDir(), "clawker-cache"),
		},
	}

	for _, tt := range tests {
		if tt.onlyWindows && runtime.GOOS != "windows" {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != nil {
				for k, v := range tt.env {
					t.Setenv(k, v)
				}
			}
			assert.Equal(t, tt.output, cacheDir())
		})
	}
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
				assert.Equal(t, tt.wantName, store.Get().Name)
			}
		})
	}
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
	assert.Equal(t, "override", store.Get().Name)
	assert.Equal(t, 99, store.Get().Version)

	// Low-priority dir provides fields not set in high-priority.
	assert.Equal(t, "node:20", store.Get().Build.Image)
}

func TestWalkType_PointerToStruct(t *testing.T) {
	// walkType must dereference pointer types before the struct check.
	// Without this, passing *T (instead of T) silently returns an empty
	// registry — merge tags are lost and union slices fall back to overwrite.

	type inner struct {
		Items []string `yaml:"items" merge:"union"`
	}
	type outer struct {
		Name  string `yaml:"name"`
		Inner inner  `yaml:"inner"`
	}

	// Value type — baseline.
	valReg := make(tagRegistry)
	walkType(reflect.TypeOf(outer{}), "", valReg)
	require.Contains(t, valReg, "inner.items", "value type: merge tag must be registered")
	assert.Equal(t, "union", valReg["inner.items"])

	// Pointer type — must produce identical registry.
	ptrReg := make(tagRegistry)
	walkType(reflect.TypeOf(&outer{}), "", ptrReg)
	require.Contains(t, ptrReg, "inner.items", "pointer type: merge tag must be registered")
	assert.Equal(t, "union", ptrReg["inner.items"])

	// Both registries must be identical.
	assert.Equal(t, valReg, ptrReg, "value and pointer registries must match")
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
	dataDir := filepath.Join(root, "data")

	require.NoError(t, os.MkdirAll(userConfigDir, 0o755))
	require.NoError(t, os.MkdirAll(dataDir, 0o755))

	// --- Registry ---
	registryYAML := fmt.Sprintf("projects:\n  - root: %s\n", projectDir)
	require.NoError(t, os.WriteFile(
		filepath.Join(dataDir, "registry.yaml"),
		[]byte(registryYAML), 0o644,
	))

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
	writeFile(userPath, "name: user\nversion: 1\nbuild:\n  image: ubuntu\npackages:\n  - pkg-user\nenv:\n  EDITOR: vim\n")
	pathTag[userPath] = fileTag{"user", "-", "-"}

	// --- Print actual filesystem tree so ai agents can't make a forgery (a8m/tree reads the real FS) ---
	{
		var buf bytes.Buffer
		tr := tree.New(root)
		opts := &tree.Options{Fs: new(ostree.FS), OutFile: &buf, All: true}
		tr.Visit(opts)
		tr.Print(opts)
		t.Logf("\n=== TREE ===\n%s", buf.String())
	}

	// --- Discover ---
	t.Setenv("CLAWKER_DATA_DIR", dataDir)
	t.Chdir(levels[len(levels)-1]) // CWD = deepest level

	store, err := NewStore[testConfig](
		WithFilenames("config.local.yaml", "config.yaml"),
		WithWalkUp(),
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
	//   - Map:      accumulate keys, conflicts won by higher priority
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
		maps.Copy(o.env, gen.env)
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
	dataDir := filepath.Join(root, "data")

	require.NoError(t, os.MkdirAll(userConfigDir, 0o755))
	require.NoError(t, os.MkdirAll(dataDir, 0o755))

	registryYAML := fmt.Sprintf("projects:\n  - root: %s\n", projectDir)
	require.NoError(t, os.WriteFile(
		filepath.Join(dataDir, "registry.yaml"),
		[]byte(registryYAML), 0o644,
	))

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
	writeFile(userPath, "name: user\nversion: 1\nbuild:\n  image: ubuntu\npackages:\n  - pkg-user\nenv:\n  EDITOR: vim\n")

	t.Setenv("CLAWKER_DATA_DIR", dataDir)
	t.Chdir(levels[len(levels)-1])

	store, err := NewStore[testConfig](
		WithFilenames("config.local.yaml", "config.yaml"),
		WithWalkUp(),
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
	goldenEnv := map[string]string{
		"B":       "project",
		"EDITOR":  "vim",
		"F":       "level1",
		"LEVEL1":  "yes",
		"PROJECT": "yes",
	}

	assert.Equal(t, goldenName, cfg.Name, "golden: name")
	assert.Equal(t, goldenVersion, cfg.Version, "golden: version")
	assert.Equal(t, goldenImage, cfg.Build.Image, "golden: image")
	assert.Equal(t, goldenPackages, cfg.Packages, "golden: packages")
	assert.Equal(t, goldenEnv, cfg.Env, "golden: env")
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
	assert.Equal(t, "from-dotfile", store.Get().Name)
}

func TestStore_MutationWithoutSet(t *testing.T) {
	// Two callers Read() the snapshot, mutate it directly (bypassing Set),
	// then call Write(). Because Set() was never called, dirty is never
	// set — Write() is a no-op and mutations are silently lost.
	dir := t.TempDir()
	store, err := NewFromString[testConfig](testFullData())
	require.NoError(t, err)
	store.opts.paths = []string{dir}
	store.opts.filenames = []string{"config.yaml"}

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

	// File was never created — Write() was a no-op both times.
	_, statErr := os.Stat(filepath.Join(dir, "config.yaml"))
	assert.True(t, os.IsNotExist(statErr),
		"file should not exist — Write was no-op (dirty never set)")

	// The canonical tree is completely untouched.
	tree, treeErr := unmarshal[testConfig](store.tree)
	require.NoError(t, treeErr)
	assert.Equal(t, "myproject", tree.Name, "tree retains original name")
	assert.Equal(t, 1, tree.Version, "tree retains original version")
	assert.Equal(t, "node:20", tree.Build.Image, "tree retains original image")

	t.Log("Direct mutation: snapshot dirty in-memory but tree + disk untouched.")
}

func TestStore_MutationWithSet(t *testing.T) {
	t.Run("two Sets on different fields — both survive", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFromString[testConfig](testFullData())
		require.NoError(t, err)
		store.opts.paths = []string{dir}
		store.opts.filenames = []string{"config.yaml"}

		// Caller A sets name.
		require.NoError(t, store.Set(func(c *testConfig) {
			c.Name = "set-by-A"
		}))

		// Caller B sets version.
		require.NoError(t, store.Set(func(c *testConfig) {
			c.Version = 999
		}))

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
		store, err := NewFromString[testConfig](testFullData())
		require.NoError(t, err)
		store.opts.paths = []string{dir}
		store.opts.filenames = []string{"config.yaml"}

		require.NoError(t, store.Set(func(c *testConfig) {
			c.Name = "writer-A"
		}))
		require.NoError(t, store.Set(func(c *testConfig) {
			c.Name = "writer-B"
		}))

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

		require.NoError(t, store.Set(func(c *testConfig) {
			c.Name = "mutated"
			c.Version = 42
		}))

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
	store, err := NewFromString[testConfig](testFullData())
	require.NoError(t, err)

	store.opts.paths = []string{dir}
	store.opts.filenames = []string{"config.yaml"}

	require.NoError(t, store.Set(func(c *testConfig) {
		c.Env = map[string]string{}
	}))
	require.NoError(t, store.Write())

	onDisk := mustReadConfig(t, filepath.Join(dir, "config.yaml"))
	assert.Empty(t, onDisk.Env, "clearing map via Set should persist an empty map")
}

func TestStore_Merge_UnionHandlesNonComparableValues(t *testing.T) {
	tags := buildTagRegistry[testUnionMapCfg]()

	base := map[string]any{
		"items": []any{
			map[string]any{"name": "a"},
		},
	}
	layers := []layer{
		{
			path:     "layer.yaml",
			filename: "layer.yaml",
			data: map[string]any{
				"items": []any{
					map[string]any{"name": "b"},
				},
			},
		},
	}

	require.NotPanics(t, func() {
		result, _ := merge(base, layers, tags)
		items, ok := result["items"].([]any)
		require.True(t, ok)
		assert.Len(t, items, 2)
	})
}

func TestStore_Merge_UnionWithImplicitYAMLFieldName(t *testing.T) {
	tags := buildTagRegistry[testUnionImplicitCfg]()

	base := map[string]any{
		"items": []any{"a"},
	}
	layers := []layer{
		{
			path:     "layer.yaml",
			filename: "layer.yaml",
			data: map[string]any{
				"items": []any{"b"},
			},
		},
	}

	result, _ := merge(base, layers, tags)
	cfgResult, err := unmarshal[testUnionImplicitCfg](result)
	require.NoError(t, err)

	assert.Equal(t, []string{"a", "b"}, cfgResult.Items,
		"merge union should still apply when yaml tag uses implicit field name")
}
