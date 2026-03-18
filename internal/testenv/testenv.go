// Package testenv provides unified, progressively-configured test environments
// for isolated filesystem tests. It creates temp directories for all four XDG
// categories (config, data, state, cache), sets the corresponding CLAWKER_*_DIR
// env vars, and optionally wires up a real config.Config and/or ProjectManager.
//
// Usage:
//
//	// Just isolated dirs (storage tests):
//	env := testenv.New(t)
//	env.Dirs.Data // absolute path
//
//	// With real config (config, socketbridge tests):
//	env := testenv.New(t, testenv.WithConfig())
//	env.Config() // config.Config backed by temp dirs
//
//	// With real project manager (project tests):
//	env := testenv.New(t, testenv.WithProjectManager(nil))
//	env.ProjectManager() // project.ProjectManager
//	env.Config()         // also available — PM implies Config
package testenv

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
)

// IsolatedDirs holds the four XDG-style directory paths created for the test.
type IsolatedDirs struct {
	Base   string // temp root (parent of all dirs)
	Config string // CLAWKER_CONFIG_DIR
	Data   string // CLAWKER_DATA_DIR
	State  string // CLAWKER_STATE_DIR
	Cache  string // CLAWKER_CACHE_DIR
}

// Env is a unified test environment with isolated directories and optional
// higher-level capabilities (config, project manager).
type Env struct {
	Dirs IsolatedDirs

	config         config.Config
	projectManager project.ProjectManager
}

// Option configures an Env during construction.
type Option func(t *testing.T, e *Env)

// WithConfig creates a real config.Config backed by the isolated directories.
// The config is available via env.Config().
func WithConfig() Option {
	return func(t *testing.T, e *Env) {
		t.Helper()
		if e.config != nil {
			return // already created (e.g. by WithProjectManager)
		}
		cfg, err := config.NewConfig()
		if err != nil {
			t.Fatalf("testenv: creating config: %v", err)
		}
		e.config = cfg
	}
}

// WithProjectManager creates a real project.ProjectManager backed by the
// isolated directories. Implies WithConfig. Pass nil for gitFactory if
// worktree operations are not needed.
func WithProjectManager(gitFactory project.GitManagerFactory) Option {
	return func(t *testing.T, e *Env) {
		t.Helper()
		// Ensure config is created first.
		WithConfig()(t, e)

		mgr, err := project.NewProjectManager(e.config, logger.Nop(), gitFactory)
		if err != nil {
			t.Fatalf("testenv: creating project manager: %v", err)
		}
		e.projectManager = mgr
	}
}

// New creates an isolated test environment. It:
//  1. Creates temp directories for config, data, state, and cache
//  2. Sets CLAWKER_CONFIG_DIR, CLAWKER_DATA_DIR, CLAWKER_STATE_DIR,
//     CLAWKER_CACHE_DIR env vars (restored on test cleanup)
//  3. Applies any options (WithConfig, WithProjectManager)
func New(t *testing.T, opts ...Option) *Env {
	t.Helper()

	// Resolve symlinks on the base temp dir so paths match os.Getwd()
	// after chdir (macOS: /var → /private/var).
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("testenv: resolving temp dir symlinks: %v", err)
	}

	dirs := IsolatedDirs{
		Base:   base,
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		State:  filepath.Join(base, "state"),
		Cache:  filepath.Join(base, "cache"),
	}

	for _, dir := range []string{dirs.Config, dirs.Data, dirs.State, dirs.Cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("testenv: creating dir %s: %v", dir, err)
		}
	}

	t.Setenv("HOME", base)

	// Create minimal ~/.claude/ so container init can find host config dir.
	if err := os.MkdirAll(filepath.Join(base, ".claude"), 0o755); err != nil {
		t.Fatalf("testenv: creating .claude dir: %v", err)
	}

	t.Setenv("CLAWKER_CONFIG_DIR", dirs.Config)
	t.Setenv("CLAWKER_DATA_DIR", dirs.Data)
	t.Setenv("CLAWKER_STATE_DIR", dirs.State)
	t.Setenv("CLAWKER_CACHE_DIR", dirs.Cache)

	env := &Env{Dirs: dirs}

	for _, opt := range opts {
		opt(t, env)
	}

	return env
}

// ConfigFile identifies a configuration file type and its canonical location.
type ConfigFile int

const (
	// ProjectConfig writes .clawker.yaml to the given directory (dotfile placement).
	ProjectConfig ConfigFile = iota
	// ProjectConfigLocal writes .clawker.local.yaml to the given directory (highest priority).
	ProjectConfigLocal
	// Settings writes settings.yaml to the config directory.
	Settings
	// EgressRules writes egress-rules.yaml to the state directory.
	EgressRules
	// ProjectRegistry writes projects.yaml to the data directory.
	ProjectRegistry
)

// configFileInfo maps a ConfigFile to its filename and directory resolution.
type configFileInfo struct {
	filename func() string       // canonical filename
	dir      func(e *Env) string // target directory (nil = use caller-provided dir)
	dotfile  bool                // prepend "." to filename (dual placement dotfile form)
}

var configFiles = map[ConfigFile]configFileInfo{
	ProjectConfig:      {filename: func() string { return "clawker.yaml" }, dotfile: true},
	ProjectConfigLocal: {filename: func() string { return "clawker.local.yaml" }, dotfile: true},
	Settings:           {filename: func() string { return "settings.yaml" }, dir: func(e *Env) string { return e.Dirs.Config }},
	EgressRules:        {filename: func() string { return "egress-rules.yaml" }, dir: func(e *Env) string { return e.Dirs.State }},
	ProjectRegistry:    {filename: func() string { return "projects.yaml" }, dir: func(e *Env) string { return e.Dirs.Data }},
}

// WriteYAML writes YAML content to the canonical location for the given file type.
//
// For project config files (ProjectConfig, ProjectConfigLocal), dir specifies
// the project directory where the dotfile will be created. For all other file
// types, dir is ignored and the appropriate XDG directory is used.
//
//	env.WriteYAML(t, testenv.ProjectConfig, projectDir, `agent: { claude_code: { use_host_auth: false } }`)
//	env.WriteYAML(t, testenv.Settings, "", `firewall: { enable: false }`)
func (e *Env) WriteYAML(t *testing.T, file ConfigFile, dir string, content string) {
	t.Helper()

	info, ok := configFiles[file]
	if !ok {
		t.Fatalf("testenv: unknown ConfigFile %d", file)
	}

	targetDir := dir
	if info.dir != nil {
		targetDir = info.dir(e)
	}
	if targetDir == "" {
		t.Fatalf("testenv: WriteYAML(%d) requires a dir argument for project config files", file)
	}

	filename := info.filename()
	if info.dotfile {
		filename = "." + filename
	}

	path := filepath.Join(targetDir, filename)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("testenv: creating parent dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("testenv: writing %s: %v", path, err)
	}
}

// Config returns the config.Config. Panics if WithConfig (or
// WithProjectManager) was not passed to New.
func (e *Env) Config() config.Config {
	if e.config == nil {
		panic("testenv: Config() called but WithConfig() was not applied — pass testenv.WithConfig() to testenv.New()")
	}
	return e.config
}

// ProjectManager returns the project.ProjectManager. Panics if
// WithProjectManager was not passed to New.
func (e *Env) ProjectManager() project.ProjectManager {
	if e.projectManager == nil {
		panic("testenv: ProjectManager() called but WithProjectManager() was not applied — pass testenv.WithProjectManager() to testenv.New()")
	}
	return e.projectManager
}
