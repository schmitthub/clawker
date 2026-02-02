# Factory Refactor Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refactor Factory from a 25-field bag of closures into a 9-field lazy noun registry, rename types for clarity, dissolve `internal/resolver`, and rename `internal/prompter` to `internal/prompter`.

**Architecture:** Factory becomes a pure lazy DI container where every field is a noun pointing to a package-level capability. Commands load the dep then call methods through it. Config is treated as leaf — always passed, never imported for behavior. A new `config.Config` top-level type exposes `Project()` and `Settings()` methods, replacing the two separate Factory fields.

**Tech Stack:** Go, Cobra CLI, zerolog, Docker SDK

---

## Design Principles (reference for all tasks)

1. **Packages are nouns** — they represent things, not actions
2. **Factory fields are nouns** — lazy references to package-level capabilities
3. **Commands load deps, call methods through them** — no verbs on Factory
4. **Config is leaf-like** — always passed around, never imported for behavior
5. **3+ command threshold** — fields earn Factory membership by being needed by 3+ commands
6. **No verbs on Factory** — no `EnsureX`, `ResetX`, `InvalidateX`, `CloseX`

## Target Factory Struct

```go
type Factory struct {
    // Eager (set at construction)
    Version  string
    Commit   string
    IOStreams *iostreams.IOStreams

    // Lazy nouns
    WorkDir   func() string
    Client    func(context.Context) (*docker.Client, error)
    Config    func() *config.Config
    HostProxy func() *hostproxy.Manager
    Prompter  func() *prompter.Prompter
}
```

## Target config.Config Type

```go
// internal/config/config.go (new file)
type Config struct {
    // internals: loaders, registry, resolver, workdir, lazy caching
}

func NewConfig(workDir func() string, opts ...ConfigOption) *Config
func (c *Config) Project() (*Project, error)      // was config.Config (clawker.yaml)
func (c *Config) Settings() (*Settings, error)    // was config.Settings (settings.yaml)
func (c *Config) Resolution() *Resolution          // project resolution from registry
func (c *Config) Registry() (*RegistryLoader, error) // for project init/register
```

## Removed Factory Fields

| Field | Reason | Replacement |
|-------|--------|-------------|
| `BuildOutputDir` | 1 command (generate) | Resolve locally in generate |
| `Debug` | 1 command (generate) + root | Local flag var in root |
| `CloseClient` | Verb | `Client.Close()` in main |
| `ConfigLoader` | Dead field | Internal to `config.Config` |
| `ResetConfig` | Dead verb | `config.Config` manages cache internally |
| `Config func() (*config.Config, error)` | Replaced | `Config func() *config.Config` (new type) |
| `Settings` | Same package as Config | `config.Config.Settings()` |
| `SettingsLoader` | Internal detail | Internal to `config.Config` |
| `InvalidateSettingsCache` | Verb, internal detail | `config.Config` manages cache internally |
| `RegistryLoader` | Internal detail | `config.Config.Registry()` |
| `Registry` | Dead field | Internal to `config.Config` |
| `Resolution` | Internal detail | `config.Config.Resolution()` |
| `EnsureHostProxy` | Verb | `HostProxy().EnsureRunning()` |
| `StopHostProxy` | Dead verb | Removed |
| `HostProxyEnvVar` | Derived value | `HostProxy().ProxyURL()` + build env var in command |
| `RuntimeEnv` | Not a package noun | Commands call `docker.RuntimeEnv(cfg)` directly |
| `BuildKitEnabled` | Not a package noun | Commands call `docker.BuildKitEnabled(ctx, client)` directly |

## Package Changes

| Change | From | To |
|--------|------|----|
| Rename package | `internal/prompter` | `internal/prompter` |
| Rename type | `config.Config` (schema struct) | `config.Project` |
| New type | — | `config.Config` (top-level lazy gateway) |
| Dissolve package | `internal/resolver` | Image resolution moves to `internal/docker` |

---

## Phase 1: Rename `config.Config` to `config.Project` (mechanical, no behavior change)

This is the highest-touch rename (61+ files). Do it first as a standalone commit so diffs are clean.

### Task 1: Rename config.Config struct to config.Project

**Files:**
- Modify: `internal/config/schema.go` — rename `Config` struct to `Project`
- Modify: `internal/config/schema_test.go` — update type references
- Modify: `internal/config/loader.go` — return type `*Project` instead of `*Config`
- Modify: `internal/config/loader_test.go` — update type references
- Modify: `internal/config/defaults.go` — update type references
- Modify: `internal/config/defaults_test.go` — update type references
- Modify: `internal/config/validator.go` — update type references
- Modify: `internal/config/validator_test.go` — update type references
- Modify: `internal/config/debug_test.go` — update type references

**Step 1: Use Serena's rename_symbol to rename Config to Project in schema.go**

Use `rename_symbol` with `name_path=Config`, `relative_path=internal/config/schema.go`, `new_name=Project`. This performs a codebase-wide rename via the LSP.

**Step 2: Verify the rename propagated correctly**

Run: `go build ./...`
Expected: Clean build. If errors, fix any references the LSP missed.

**Step 3: Run config package tests**

Run: `go test ./internal/config/... -v`
Expected: All pass.

**Step 4: Run full unit test suite**

Run: `make test`
Expected: All pass.

**Step 5: Commit**

```bash
git add -A
git commit -m "refactor(config): rename Config struct to Project

Prepares for new config.Config top-level type that will serve as
the lazy gateway to both Project and Settings config.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 2: Rename `internal/prompter` to `internal/prompter`

### Task 2: Rename prompts package to prompter

**Files:**
- Rename: `internal/prompter/` → `internal/prompter/`
- Modify: All 14 files that import `internal/prompter` (see impact map)
- Modify: `internal/cmdutil/factory.go` — import path + type reference
- Modify: `internal/cmd/factory/default.go` — import path + constructor call
- Modify: `test/harness/factory.go` — import path

**Step 1: Rename the directory**

```bash
git mv internal/prompter internal/prompter
```

**Step 2: Update package declaration in all files under internal/prompter/**

Change `package prompts` → `package prompter` in every `.go` file.

**Step 3: Update all import paths codebase-wide**

Use `search_for_pattern` to find every file importing `internal/prompter`, then update the import path to `internal/prompter` and all `prompter.` references to `prompter.`.

Files to update (14 production + test files):
- `internal/cmdutil/factory.go`
- `internal/cmd/factory/default.go`
- `internal/cmd/container/run/run.go`
- `internal/cmd/container/create/create.go`
- `internal/cmd/project/register/register.go`
- `internal/cmd/project/init/init.go`
- `internal/cmd/init/init.go`
- `internal/cmd/volume/prune/prune.go`
- `internal/cmd/network/prune/prune.go`
- `internal/cmd/image/prune/prune.go`
- `internal/resolver/image.go`
- `test/harness/factory.go`
- `internal/cmd/container/run/run_test.go`
- `internal/cmd/image/prune/prune_test.go`

**Step 4: Verify build**

Run: `go build ./...`
Expected: Clean build.

**Step 5: Run tests**

Run: `make test`
Expected: All pass.

**Step 6: Commit**

```bash
git add -A
git commit -m "refactor: rename internal/prompter to internal/prompter

Packages should be nouns. The prompter package provides a Prompter.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 3: Dissolve `internal/resolver` into `internal/docker`

### Task 3: Move image resolution functions to internal/docker

**Files:**
- Move from: `internal/resolver/image.go` → `internal/docker/image_resolve.go`
- Move from: `internal/resolver/types.go` → `internal/docker/image_resolve.go` (merge)
- Move from: `internal/resolver/image_test.go` → `internal/docker/image_resolve_test.go`
- Delete: `internal/resolver/` (entire directory)
- Modify: `internal/cmd/container/run/run.go` — update import from `resolver` to `docker`
- Modify: `internal/cmd/container/create/create.go` — update import
- Modify: `test/internals/image_resolver_test.go` — update import

**Step 1: Create `internal/docker/image_resolve.go`**

Copy the contents of `internal/resolver/image.go` and `internal/resolver/types.go` into a new file `internal/docker/image_resolve.go`. Change `package resolver` → `package docker`. Update internal imports (the file references `prompts` which is now `prompter`). The `ImageValidationDeps` struct should reference `*prompter.Prompter`.

**Step 2: Create `internal/docker/image_resolve_test.go`**

Copy `internal/resolver/image_test.go`, change package, update imports.

**Step 3: Update consumers**

In `internal/cmd/container/run/run.go` and `create/create.go`:
- Remove `resolver` import
- Change `resolver.ResolveAndValidateImage` → `docker.ResolveAndValidateImage`
- Change `resolver.ImageValidationDeps` → `docker.ImageValidationDeps`
- Change `resolver.ResolvedImage` → `docker.ResolvedImage`
- Change `resolver.ImageSourceProject` etc. → `docker.ImageSourceProject`

In `test/internals/image_resolver_test.go`: same import changes.

**Step 4: Delete old package**

```bash
rm -rf internal/resolver
```

**Step 5: Verify build**

Run: `go build ./...`
Expected: Clean build.

**Step 6: Run tests**

Run: `go test ./internal/docker/... -v && make test`
Expected: All pass.

**Step 7: Commit**

```bash
git add -A
git commit -m "refactor: dissolve internal/resolver into internal/docker

Image resolution is Docker domain logic. The resolver package was a
single-concern package that didn't justify its own existence.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 4: Create `config.Config` top-level type

This is the core architectural change. We create the new `config.Config` gateway type that encapsulates project config, settings, registry, and resolution behind a single noun.

### Task 4: Create config.Config gateway type with tests

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Step 1: Write failing tests for config.Config**

Create `internal/config/config_test.go`:

```go
package config

import (
    "os"
    "path/filepath"
    "testing"
)

func TestConfig_Project(t *testing.T) {
    // Setup: create a temp dir with a clawker.yaml
    tmpDir := t.TempDir()
    yamlContent := `version: "1"
project: "test-project"
build:
  image: "node:20"
`
    if err := os.WriteFile(filepath.Join(tmpDir, "clawker.yaml"), []byte(yamlContent), 0644); err != nil {
        t.Fatal(err)
    }

    cfg := NewConfig(func() string { return tmpDir })
    project, err := cfg.Project()
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if project.Build.Image != "node:20" {
        t.Errorf("expected image node:20, got %s", project.Build.Image)
    }
}

func TestConfig_Project_Caching(t *testing.T) {
    tmpDir := t.TempDir()
    yamlContent := `version: "1"
project: "test"
`
    if err := os.WriteFile(filepath.Join(tmpDir, "clawker.yaml"), []byte(yamlContent), 0644); err != nil {
        t.Fatal(err)
    }

    cfg := NewConfig(func() string { return tmpDir })

    p1, err1 := cfg.Project()
    p2, err2 := cfg.Project()
    if err1 != nil || err2 != nil {
        t.Fatalf("unexpected errors: %v, %v", err1, err2)
    }
    if p1 != p2 {
        t.Error("expected same pointer from cached Project() calls")
    }
}

func TestConfig_Settings(t *testing.T) {
    tmpDir := t.TempDir()

    cfg := NewConfig(func() string { return tmpDir })
    settings, err := cfg.Settings()
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if settings == nil {
        t.Fatal("expected non-nil settings")
    }
}

func TestConfig_Resolution_NoRegistry(t *testing.T) {
    tmpDir := t.TempDir()

    cfg := NewConfig(func() string { return tmpDir })
    res := cfg.Resolution()
    if res.Found() {
        t.Error("expected no project found without registry")
    }
    if res.WorkDir != tmpDir {
        t.Errorf("expected workdir %s, got %s", tmpDir, res.WorkDir)
    }
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/... -run TestConfig_ -v`
Expected: FAIL — `NewConfig` not defined.

**Step 3: Implement config.Config**

Create `internal/config/config.go`:

```go
package config

import (
    "sync"

    "github.com/clawker/clawker/internal/logger"
)

// Config is the top-level configuration gateway. It lazily loads and caches
// project config, user settings, and project resolution.
type Config struct {
    workDir func() string

    // Project config (clawker.yaml)
    projectOnce sync.Once
    projectLoader *Loader
    project       *Project
    projectErr    error

    // Settings (settings.yaml)
    settingsOnce   sync.Once
    settingsLoader *SettingsLoader
    settingsErr    error
    settings       *Settings

    // Registry + Resolution
    registryOnce   sync.Once
    registryLoader *RegistryLoader
    registryErr    error
    resolution     *Resolution
    resolutionOnce sync.Once
}

// NewConfig creates a new Config gateway. The workDir function is called
// lazily when needed for project resolution and config loading.
func NewConfig(workDir func() string) *Config {
    return &Config{workDir: workDir}
}

// Project returns the project configuration from clawker.yaml.
// Results are cached after first load.
func (c *Config) Project() (*Project, error) {
    c.projectOnce.Do(func() {
        var opts []LoaderOption

        res := c.Resolution()
        if res.Found() {
            opts = append(opts,
                WithProjectRoot(res.ProjectRoot()),
                WithProjectKey(res.ProjectKey),
            )
        }

        opts = append(opts, WithUserDefaults(""))
        c.projectLoader = NewLoader(c.workDir(), opts...)
        c.project, c.projectErr = c.projectLoader.Load()
    })
    return c.project, c.projectErr
}

// Settings returns the user settings from settings.yaml.
// Results are cached after first load.
func (c *Config) Settings() (*Settings, error) {
    c.settingsOnce.Do(func() {
        var opts []SettingsLoaderOption

        res := c.Resolution()
        if res.Found() {
            opts = append(opts, WithProjectSettingsRoot(res.ProjectRoot()))
        }

        c.settingsLoader, c.settingsErr = NewSettingsLoader(opts...)
        if c.settingsErr != nil {
            return
        }
        c.settings, c.settingsErr = c.settingsLoader.Load()
    })
    return c.settings, c.settingsErr
}

// SettingsLoader returns the underlying settings loader for write operations
// (e.g., saving updated default image). Lazily initialized.
func (c *Config) SettingsLoader() (*SettingsLoader, error) {
    // Ensure settings are loaded (which initializes the loader)
    _, _ = c.Settings()
    return c.settingsLoader, c.settingsErr
}

// Resolution returns the project resolution (project key, entry, workdir).
// Results are cached after first resolution.
func (c *Config) Resolution() *Resolution {
    c.resolutionOnce.Do(func() {
        wd := c.workDir()
        registry, err := c.registry()
        if err != nil {
            logger.Warn().Err(err).Msg("failed to load project registry; operating without project context")
            c.resolution = &Resolution{WorkDir: wd}
            return
        }
        if registry == nil {
            c.resolution = &Resolution{WorkDir: wd}
            return
        }
        resolver := NewResolver(registry)
        c.resolution = resolver.Resolve(wd)
    })
    return c.resolution
}

// Registry returns the registry loader for write operations
// (e.g., project register, project init).
func (c *Config) Registry() (*RegistryLoader, error) {
    c.initRegistry()
    return c.registryLoader, c.registryErr
}

func (c *Config) initRegistry() {
    c.registryOnce.Do(func() {
        c.registryLoader, c.registryErr = NewRegistryLoader()
    })
}

func (c *Config) registry() (*ProjectRegistry, error) {
    c.initRegistry()
    if c.registryErr != nil {
        return nil, c.registryErr
    }
    return c.registryLoader.Load()
}
```

**Step 4: Run tests**

Run: `go test ./internal/config/... -run TestConfig_ -v`
Expected: All pass.

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add Config gateway type

New top-level config.Config encapsulates lazy loading and caching
of project config, user settings, registry, and resolution behind
a single noun.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 5: Refactor Factory struct and constructor

### Task 5: Update Factory struct to 9 fields

**Files:**
- Modify: `internal/cmdutil/factory.go`
- Test: `internal/cmd/factory/default_test.go`

**Step 1: Rewrite the Factory struct**

Replace the Factory struct in `internal/cmdutil/factory.go` with:

```go
type Factory struct {
    // Eager (set at construction)
    Version  string
    Commit   string
    IOStreams *iostreams.IOStreams

    // Lazy nouns
    WorkDir   func() string
    Client    func(context.Context) (*docker.Client, error)
    Config    func() *config.Config
    HostProxy func() *hostproxy.Manager
    Prompter  func() *prompter.Prompter
}
```

Remove unused imports (`prompts` is gone, replaced by `prompter`). Remove import of `config` types that are no longer directly on Factory (`Settings`, `SettingsLoader`, `Resolution`, `ProjectRegistry`, `RegistryLoader`). Note: `config.Config` is now the gateway type, not the old schema struct.

**Step 2: Verify build fails as expected**

Run: `go build ./...`
Expected: FAIL — many files reference removed fields. This confirms the struct change propagates.

**Step 3: Commit the struct change (broken build is OK — we'll fix consumers next)**

Do NOT commit yet. Proceed to Task 6.

### Task 6: Rewrite Factory constructor with extracted helpers

**Files:**
- Modify: `internal/cmd/factory/default.go`
- Modify: `internal/cmd/factory/default_test.go`

**Step 1: Rewrite `New()` as a wiring manifest with extracted helpers**

Replace the entire contents of `internal/cmd/factory/default.go`:

```go
package factory

import (
    "context"
    "os"
    "sync"

    "github.com/clawker/clawker/internal/cmd/factory/internal/config"
    "github.com/clawker/clawker/internal/cmdutil"
    "github.com/clawker/clawker/internal/docker"
    "github.com/clawker/clawker/internal/hostproxy"
    "github.com/clawker/clawker/internal/iostreams"
    "github.com/clawker/clawker/internal/prompter"
)

func New(version, commit string) *cmdutil.Factory {
    f := &cmdutil.Factory{
        Version: version,
        Commit:  commit,
    }

    // Eager
    f.IOStreams = ioStreams()

    // Lazy nouns — no factory dependencies
    f.WorkDir = workDirFunc()
    f.Client = clientFunc()
    f.HostProxy = hostProxyFunc()

    // Lazy nouns — depend on factory
    f.Config = configFunc(f)
    f.Prompter = prompterFunc(f)

    return f
}

func ioStreams() *iostreams.IOStreams {
    ios := iostreams.NewIOStreams()

    if ios.IsOutputTTY() {
        ios.DetectTerminalTheme()
        if os.Getenv("NO_COLOR") != "" {
            ios.SetColorEnabled(false)
        }
    } else {
        ios.SetColorEnabled(false)
    }

    if os.Getenv("CI") != "" {
        ios.SetNeverPrompt(true)
    }

    return ios
}

func workDirFunc() func() string {
    var (
        once sync.Once
        wd   string
    )
    return func() string {
        once.Do(func() {
            wd, _ = os.Getwd()
        })
        return wd
    }
}

func clientFunc() func(context.Context) (*docker.Client, error) {
    var (
        once   sync.Once
        client *docker.Client
        err    error
    )
    return func(ctx context.Context) (*docker.Client, error) {
        once.Do(func() {
            client, err = docker.NewClient(ctx)
            if err == nil {
                docker.WireBuildKit(client)
            }
        })
        return client, err
    }
}

func hostProxyFunc() func() *hostproxy.Manager {
    var (
        once    sync.Once
        manager *hostproxy.Manager
    )
    return func() *hostproxy.Manager {
        once.Do(func() {
            manager = hostproxy.NewManager()
        })
        return manager
    }
}

func configFunc(f *cmdutil.Factory) func() *config.Config {
    var (
        once sync.Once
        cfg  *config.Config
    )
    return func() *config.Config {
        once.Do(func() {
            cfg = config.NewConfig(f.WorkDir)
        })
        return cfg
    }
}

func prompterFunc(f *cmdutil.Factory) func() *prompter.Prompter {
    return func() *prompter.Prompter {
        return prompter.NewPrompter(f.IOStreams)
    }
}
```

NOTE: Fix the import path above — it should be the actual module path for `internal/config`, not `internal/cmd/factory/internal/config`. Use the correct module path from `go.mod`.

**Step 2: Update factory tests**

Rewrite `internal/cmd/factory/default_test.go` to test the new Factory shape:

```go
package factory

import (
    "context"
    "testing"
)

func TestNew(t *testing.T) {
    f := New("1.0.0", "abc123")

    if f.Version != "1.0.0" {
        t.Errorf("expected version 1.0.0, got %s", f.Version)
    }
    if f.IOStreams == nil {
        t.Error("expected IOStreams to be set")
    }
    if f.WorkDir == nil {
        t.Error("expected WorkDir to be set")
    }
    if f.Client == nil {
        t.Error("expected Client to be set")
    }
    if f.Config == nil {
        t.Error("expected Config to be set")
    }
    if f.HostProxy == nil {
        t.Error("expected HostProxy to be set")
    }
    if f.Prompter == nil {
        t.Error("expected Prompter to be set")
    }
}

func TestNew_WorkDir(t *testing.T) {
    f := New("1.0.0", "abc")
    wd := f.WorkDir()
    if wd == "" {
        t.Error("expected non-empty WorkDir")
    }
    // Should be cached
    wd2 := f.WorkDir()
    if wd != wd2 {
        t.Error("expected WorkDir to return same value on second call")
    }
}

func TestNew_Config(t *testing.T) {
    f := New("1.0.0", "abc")
    cfg := f.Config()
    if cfg == nil {
        t.Error("expected non-nil Config")
    }
    // Should be cached
    cfg2 := f.Config()
    if cfg != cfg2 {
        t.Error("expected Config to return same pointer on second call")
    }
}
```

**Step 3: Do NOT run tests yet** — consumers are still broken. Proceed to Task 7.

---

## Phase 6: Update root.go and all command consumers

### Task 7: Refactor root.go

**Files:**
- Modify: `internal/cmd/root/root.go`

**Step 1: Rewrite root.go**

Key changes:
- `Debug` → local `var debug bool` bound to flag
- `WorkDir` flag → overrides `f.WorkDir` with a flag-aware closure
- Remove `BuildOutputDir` from PersistentPreRunE
- PersistentPreRunE becomes just: logger init + debug log

```go
func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
    var debug bool

    cmd := &cobra.Command{
        // ... Use, Short, Long unchanged ...
        SilenceUsage: true,
        PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
            initializeLogger(debug)

            logger.Debug().
                Str("version", f.Version).
                Str("workdir", f.WorkDir()).
                Bool("debug", debug).
                Msg("clawker starting")

            return nil
        },
        Version: f.Version,
    }

    // Global flags
    cmd.PersistentFlags().BoolVarP(&debug, "debug", "D", false, "Enable debug logging")

    var workDirFlag string
    cmd.PersistentFlags().StringVarP(&workDirFlag, "workdir", "w", "", "Working directory (default: current directory)")

    // Override factory default with flag-aware closure
    origWorkDir := f.WorkDir
    f.WorkDir = func() string {
        if workDirFlag != "" {
            return workDirFlag
        }
        return origWorkDir()
    }

    // Version template
    cmd.SetVersionTemplate(fmt.Sprintf("clawker %s (commit: %s)\n", f.Version, f.Commit))

    // ... registerAliases, AddCommand calls unchanged ...
}
```

### Task 8: Update generate command (remove BuildOutputDir and Debug)

**Files:**
- Modify: `internal/cmd/generate/generate.go`

**Step 1: Update GenerateOptions and NewCmdGenerate**

Remove `Debug` and `BuildOutputDir` from Options. Resolve `BuildOutputDir` locally in the run function via `config.BuildDir()`. For debug, add a local `--debug` flag on the generate command if needed, or read from the cobra persistent flag.

### Task 9: Update cmd/clawker-generate/main.go

**Files:**
- Modify: `cmd/clawker-generate/main.go`

**Step 1: Remove BuildOutputDir and Debug from Factory literal**

This standalone binary constructs a partial Factory. Remove the deleted fields. If it needs build output dir, resolve it locally.

### Task 10: Update container/run and container/create (heaviest consumers)

**Files:**
- Modify: `internal/cmd/container/run/run.go`
- Modify: `internal/cmd/container/run/run_test.go`
- Modify: `internal/cmd/container/create/create.go`

These commands have the most Factory field references. Key changes:

**Options struct changes:**
- `WorkDir string` → `WorkDir func() string` (call sites change from `opts.WorkDir` to `opts.WorkDir()`)
- `Config func() (*config.Config, error)` → `Config func() *config.Config` then call `opts.Config().Project()`
- `Settings func() (*config.Settings, error)` → removed, access via `opts.Config().Settings()`
- `SettingsLoader func() (*config.SettingsLoader, error)` → removed, access via `opts.Config().SettingsLoader()`
- `InvalidateSettingsCache func()` → removed (settings loader manages cache internally)
- `EnsureHostProxy func() error` → `HostProxy func() *hostproxy.Manager` then call `opts.HostProxy().EnsureRunning()`
- `HostProxyEnvVar func() string` → removed, build env var from `opts.HostProxy().ProxyURL()`
- `RuntimeEnv func() ([]string, error)` → removed, call `docker.RuntimeEnv(cfg)` directly
- `Resolution func() *config.Resolution` → removed, call `opts.Config().Resolution()`
- `Prompter func() *prompter.Prompter` → `Prompter func() *prompter.Prompter`

**Wiring in NewCmdRun:**
```go
opts := &RunOptions{
    IOStreams:  f.IOStreams,
    Client:    f.Client,
    Config:    f.Config,
    HostProxy: f.HostProxy,
    Prompter:  f.Prompter,
    WorkDir:   f.WorkDir,
}
```

**Run function changes:**
```go
// Before
cfg, err := opts.Config()
// After
cfg, err := opts.Config().Project()

// Before
settings, err := opts.Settings()
// After
settings, err := opts.Config().Settings()

// Before
opts.EnsureHostProxy()
// After
opts.HostProxy().EnsureRunning()

// Before
envVar := opts.HostProxyEnvVar()
// After
hp := opts.HostProxy()
envVar := ""
if hp.IsRunning() {
    envVar = "CLAWKER_HOST_PROXY=" + hp.ProxyURL()
}

// Before
runtimeEnv, err := opts.RuntimeEnv()
// After
runtimeEnv, err := docker.RuntimeEnv(cfg)

// Before
res := opts.Resolution()
// After
res := opts.Config().Resolution()
```

**Update ImageValidationDeps construction:**
```go
// Before
deps := resolver.ImageValidationDeps{
    IOStreams:                opts.IOStreams,
    Prompter:                opts.Prompter,
    SettingsLoader:          opts.SettingsLoader,
    InvalidateSettingsCache: opts.InvalidateSettingsCache,
}
// After
deps := docker.ImageValidationDeps{
    IOStreams:       opts.IOStreams,
    Prompter:       opts.Prompter,
    SettingsLoader: func() (*config.SettingsLoader, error) {
        return opts.Config().SettingsLoader()
    },
}
```

NOTE: `ImageValidationDeps` also needs updating (in Phase 3 when we moved it to docker package) to remove the `InvalidateSettingsCache` field since the settings loader now manages its own cache.

### Task 11: Update container commands that use Resolution (17 commands)

**Files (all in `internal/cmd/container/`):**
- `stop/stop.go`, `kill/kill.go`, `restart/restart.go`, `remove/remove.go`
- `rename/rename.go`, `pause/pause.go`, `unpause/unpause.go`, `update/update.go`
- `stats/stats.go`, `top/top.go`, `wait/wait.go`, `logs/logs.go`
- `inspect/inspect.go`, `exec/exec.go`, `cp/cp.go`, `attach/attach.go`
- `start/start.go`

For each command:

**Options struct change:**
```go
// Before
Resolution func() *config.Resolution
// After
Config func() *config.Config
```

**Wiring change in NewCmd*:**
```go
// Before
Resolution: f.Resolution,
// After
Config: f.Config,
```

**Usage change in run function:**
```go
// Before
res := opts.Resolution()
// After
res := opts.Config().Resolution()
```

For `start.go` and `attach.go` which also use `EnsureHostProxy`:
```go
// Before
opts.EnsureHostProxy()
// After
opts.HostProxy().EnsureRunning()
```

### Task 12: Update container command tests (17 test files)

**Files (all in `internal/cmd/container/`):**
All `*_test.go` files for commands updated in Task 11.

For each test file:
- Update Options struct construction to use `Config` instead of `Resolution`
- Create a test config helper that returns `*config.Config` with a test resolution

### Task 13: Update remaining commands

**Files:**
- `internal/cmd/image/build/build.go` — remove `BuildKitEnabled` from Options, call `docker.BuildKitEnabled` directly; `Config` becomes `func() *config.Config`; `WorkDir` becomes `func() string`
- `internal/cmd/config/check/check.go` — `WorkDir` becomes `func() string`
- `internal/cmd/project/init/init.go` — `Settings` → `Config func() *config.Config`, `RegistryLoader` → access via `Config().Registry()`, `WorkDir` becomes `func() string`
- `internal/cmd/project/register/register.go` — `RegistryLoader` → access via `Config().Registry()`, `WorkDir` becomes `func() string`
- `internal/cmd/ralph/run/run.go` — `Config` becomes `func() *config.Config`
- `internal/cmd/ralph/status/status.go` — same
- `internal/cmd/ralph/tui/tui.go` — same
- `internal/cmd/ralph/reset/reset.go` — same
- `internal/cmd/init/init.go` — verify Prompter type updated

### Task 14: Update test harness

**Files:**
- Modify: `test/harness/factory.go`

Update `NewTestFactory` to construct the new 9-field Factory:

```go
func NewTestFactory(h *Harness) (*cmdutil.Factory, *iostreams.TestIOStreams) {
    tio := iostreams.NewTestIOStreams()

    f := &cmdutil.Factory{
        WorkDir:   func() string { return h.ProjectDir },
        IOStreams:  tio.IOStreams,
        Client:    func(ctx context.Context) (*docker.Client, error) { return h.DockerClient, nil },
        Config:    func() *config.Config { return config.NewConfig(func() string { return h.ProjectDir }) },
        HostProxy: func() *hostproxy.Manager { return hostproxy.NewManager() },
        Prompter:  func() *prompter.Prompter { return prompter.NewPrompter(tio.IOStreams) },
    }

    return f, tio
}
```

### Task 15: Update main entry point (CloseClient removal)

**Files:**
- Modify: `internal/clawker/cmd.go`

Replace `f.CloseClient()` with direct client close. Since the factory no longer exposes `CloseClient`, main needs to handle this differently. Options:
- Call `f.Client(ctx)` to get the client, then defer `client.Close()`
- Or accept that the client is lazily initialized and only close it if it was opened

Check the current code and determine the cleanest approach.

### Task 16: Build and test everything

**Step 1: Build**

Run: `go build ./...`
Expected: Clean build.

**Step 2: Run full unit test suite**

Run: `make test`
Expected: All pass.

**Step 3: Run race detector**

Run: `go test ./internal/cmd/factory/... -v -race`
Expected: No races detected.

**Step 4: Commit**

```bash
git add -A
git commit -m "refactor: slim Factory to 9-field lazy noun registry

- Factory fields are nouns (lazy deps), not verbs (actions)
- config.Config gateway replaces 8 separate config/settings/registry fields
- Commands load deps and call methods through them
- WorkDir is now a lazy closure with flag override in root
- Remove BuildOutputDir, Debug from Factory (resolve locally)
- Remove RuntimeEnv, BuildKitEnabled (call docker pkg directly)
- Remove all verb fields (CloseClient, ResetConfig, etc.)
- 25 fields → 9 fields

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 7: Documentation and cleanup

### Task 17: Update CLAUDE.md files

**Files:**
- Modify: `CLAUDE.md` — update Factory section, package list, key concepts table
- Modify: `internal/cmdutil/CLAUDE.md` — update Factory field docs
- Modify: `internal/cmd/factory/CLAUDE.md` — update constructor docs
- Modify: `internal/config/CLAUDE.md` — add config.Config gateway docs, document config.Project rename
- Delete or update: `internal/resolver/CLAUDE.md` (if exists)
- Create: `internal/prompter/CLAUDE.md` (if `internal/prompter/CLAUDE.md` existed, rename)

### Task 18: Update Serena memory

**Step 1: Update the factory-refactor-plan memory with completion status**

Use `edit_memory` to update `.serena/memories/factory-refactor-plan.md` noting the refactor is complete and documenting the final architecture.

### Task 19: Final verification

**Step 1: Run all unit tests**

Run: `make test`
Expected: All pass.

**Step 2: Run integration tests (if Docker available)**

Run: `go test ./test/commands/... -v -timeout 10m`
Expected: All pass.

**Step 3: Build CLI**

Run: `go build -o bin/clawker ./cmd/clawker`
Expected: Clean build.

**Step 4: Commit docs**

```bash
git add -A
git commit -m "docs: update CLAUDE.md files for factory refactor

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Summary

| Phase | Tasks | Commits | Risk |
|-------|-------|---------|------|
| 1. Rename config.Config → config.Project | 1 | 1 | Low (mechanical rename) |
| 2. Rename prompts → prompter | 2 | 1 | Low (mechanical rename) |
| 3. Dissolve resolver → docker | 3 | 1 | Low (move + import update) |
| 4. Create config.Config gateway | 4 | 1 | Medium (new type + caching) |
| 5. Refactor Factory struct + constructor | 5-6 | 0 (part of Phase 6 commit) | Medium |
| 6. Update all consumers | 7-16 | 1 | High (100+ files) |
| 7. Documentation | 17-19 | 1 | Low |
| **Total** | **19 tasks** | **6 commits** | |

The highest-risk phase is 6 (updating all consumers). If the LSP rename in Phase 1 works cleanly, that gives confidence. The consumer updates in Phase 6 are mechanical but voluminous — the subagent should handle them file-by-file with build checks between batches.
