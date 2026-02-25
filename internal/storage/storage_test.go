package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

type testBuild struct {
	Image  string `yaml:"image"`
	Target string `yaml:"target"`
}

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
				"XDG_CONFIG_HOME": tempDir,
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
				"XDG_DATA_HOME": tempDir,
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
				"XDG_STATE_HOME": tempDir,
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
