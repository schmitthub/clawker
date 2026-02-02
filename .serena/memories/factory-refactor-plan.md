# Plan: Refactor Factory Constructor + Clean Up Design Violations

## Summary

Refactor `internal/cmd/factory/default.go` to match the GitHub CLI factory pattern. Three categories of changes:

1. **Extract closures** into named helper functions, making `New()` a readable wiring manifest
2. **Move caching** into config/settings packages where it belongs (not factory's concern)
3. **Remove design violations** — WorkDir becomes a lazy closure (not a mutated string); BuildOutputDir and Debug removed from Factory (< 3 commands)

## Reference: GitHub CLI Factory Pattern

gh CLI's `factory.New()` is a clean wiring manifest. Every closure is a named helper function. No-dep helpers can be called before the struct literal; factory-dep helpers are assigned after. Root overrides factory defaults (e.g. `SmartBaseRepoFunc` overrides `BaseRepoFunc`).

## Files to Modify

| File | Change |
|---|---|
| `internal/cmdutil/factory.go` | `WorkDir string` → `func() string`; remove `BuildOutputDir`, `Debug` |
| `internal/cmd/factory/default.go` | Full refactor: extract helpers, wire manifest |
| `internal/cmd/root/root.go` | Override `f.WorkDir` with flag-aware closure; remove PersistentPreRunE mutations; local debug var |
| `internal/cmd/container/create/create.go` | `f.WorkDir` → `f.WorkDir()` |
| `internal/cmd/container/run/run.go` | `f.WorkDir` → `f.WorkDir()` |
| `internal/cmd/config/check/check.go` | `f.WorkDir` → `f.WorkDir()` |
| `internal/cmd/project/init/init.go` | `f.WorkDir` → `f.WorkDir()` |
| `internal/cmd/project/register/register.go` | `f.WorkDir` → `f.WorkDir()` |
| `internal/cmd/image/build/build.go` | `f.WorkDir` → `f.WorkDir()`; resolve BuildOutputDir locally |
| `internal/cmd/generate/generate.go` | `f.WorkDir` → `f.WorkDir()`; resolve BuildOutputDir and Debug locally |
| `cmd/clawker-generate/main.go` | Remove `BuildOutputDir` from Factory literal |
| `internal/cmd/factory/default_test.go` | Update WorkDir usage |
| `internal/config/loader.go` | Add caching with mutex + `Reset()` method to `Loader` |
| `internal/config/settings_loader.go` | Add caching with mutex + `Invalidate()` method to `SettingsLoader` |
| `internal/cmd/factory/CLAUDE.md` | Update to reflect new structure |
| `internal/cmdutil/CLAUDE.md` | Update Factory field docs |

## Changes

### 1. Factory struct changes (`internal/cmdutil/factory.go`)

Remove `BuildOutputDir string` and `Debug bool` (each used by only 1 command — doesn't meet the 3+ threshold per dependency-placement decision tree). Change `WorkDir string` → `WorkDir func() string`.

```go
type Factory struct {
    // Removed: WorkDir string (now lazy closure below)
    // Removed: BuildOutputDir string (only used by generate command)
    // Removed: Debug bool (only used by generate command; root reads flag directly)

    Version string
    Commit  string

    IOStreams *iostreams.IOStreams

    // Lazy closure (was: WorkDir string)
    WorkDir func() string

    // Existing closures unchanged...
    Client      func(context.Context) (*docker.Client, error)
    // ...
}
```

### 2. Extract every closure into a named helper function

Every dependency gets a helper function.

| Helper function | Returns | Receives `f`? |
|---|---|---|
| `ioStreams(f)` | `*iostreams.IOStreams` | yes — future Config dependency |
| `workDirFunc()` | `func() string` | no — default: os.Getwd() |
| `clientFunc()` | `Client`, `CloseClient` | no |
| `registryFunc()` | `RegistryLoader`, `Registry` | no |
| `hostProxyFunc()` | `HostProxy`, `EnsureHostProxy`, `StopHostProxy`, `HostProxyEnvVar` | no |
| `prompterFunc(f)` | `Prompter` | yes — reads `f.IOStreams` |
| `resolutionFunc(f)` | `Resolution` | yes — reads `f.Registry`, `f.WorkDir` |
| `configLoaderFunc(f)` | `ConfigLoader` | yes — reads `f.Resolution`, `f.WorkDir` |
| `configFunc(f)` | `Config`, `ResetConfig` | yes — reads `f.ConfigLoader` |
| `settingsLoaderFunc(f)` | `SettingsLoader` | yes — reads `f.Resolution` |
| `settingsFunc(f)` | `Settings`, `InvalidateSettingsCache` | yes — reads `f.SettingsLoader` |
| `runtimeEnvFunc(f)` | `RuntimeEnv` | yes — reads `f.Config` |
| `buildKitEnabledFunc(f)` | `BuildKitEnabled` | yes — reads `f.Client` |

### 3. Transform `New()` into a wiring manifest

```go
func New(version, commit string) *cmdutil.Factory {
    f := &cmdutil.Factory{
        Version: version,
        Commit:  commit,
    }

    // --- Eager ---

    f.IOStreams = ioStreams(f)                                          // Future: Config

    // --- No factory dependencies ---

    f.WorkDir = workDirFunc()                                          // Default: os.Getwd()
    f.Client, f.CloseClient = clientFunc()                             // Docker connection (lazy)
    f.RegistryLoader, f.Registry = registryFunc()                      // Project registry (lazy)
    f.HostProxy, f.EnsureHostProxy,
        f.StopHostProxy, f.HostProxyEnvVar = hostProxyFunc()           // Host proxy (lazy)

    // --- Depends on Factory ---

    f.Prompter = prompterFunc(f)                                       // Depends on IOStreams
    f.Resolution = resolutionFunc(f)                                   // Depends on Registry, WorkDir
    f.ConfigLoader = configLoaderFunc(f)                               // Depends on Resolution, WorkDir
    f.Config, f.ResetConfig = configFunc(f)                            // Depends on ConfigLoader
    f.SettingsLoader = settingsLoaderFunc(f)                           // Depends on Resolution
    f.Settings, f.InvalidateSettingsCache = settingsFunc(f)            // Depends on SettingsLoader
    f.RuntimeEnv = runtimeEnvFunc(f)                                   // Depends on Config
    f.BuildKitEnabled = buildKitEnabledFunc(f)                         // Depends on Client

    return f
}
```

### 4. WorkDir: string → lazy closure

**Default** (factory/default.go):
```go
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
```

**Override by root** (root.go) — follows gh CLI's `BaseRepoFunc`/`SmartBaseRepoFunc` pattern where root overrides factory defaults:
```go
var workDirFlag string
cmd.PersistentFlags().StringVarP(&workDirFlag, "workdir", "w", "", "Working directory (default: current directory)")

// Override factory default with flag-aware closure
f.WorkDir = func() string {
    if workDirFlag != "" {
        return workDirFlag
    }
    wd, _ := os.Getwd()
    return wd
}
```

**Remove from PersistentPreRunE**: the `if f.WorkDir == ""` block is deleted entirely.

### 5. Remove BuildOutputDir and Debug from Factory

Both are used by only 1 command (`generate.go`). Per the dependency placement decision tree: <3 commands → doesn't belong on Factory.

**BuildOutputDir:**
- Remove `BuildOutputDir string` from `cmdutil.Factory`
- `generate.go`: resolve `config.BuildDir()` in the run function
- `cmd/clawker-generate/main.go`: pass build dir directly to the options struct
- Remove PersistentPreRunE `BuildOutputDir` block from `root.go`

**Debug:**
- Remove `Debug bool` from `cmdutil.Factory`
- `root.go`: use a local `var debug bool` for the flag binding; pass to `initializeLogger(debug)` directly
- `generate.go`: accept debug from its own flag or resolve locally

### 6. Move Config/Settings caching into their packages

Caching and thread-safety are the config package's concern, not the factory's.

**`config.Loader`** — add internal caching with mutex + `Reset()` method:
- `Load()` caches result on first call, returns cached on subsequent calls (mutex-protected)
- `Reset()` clears cache, next `Load()` re-reads from disk

**`config.SettingsLoader`** — same pattern with `Invalidate()` method:
- `Load()` caches result (mutex-protected)
- `Invalidate()` clears cache

**Factory closures become simple pass-throughs:**
```go
func configFunc(f *cmdutil.Factory) (func() (*config.Config, error), func()) {
    return func() (*config.Config, error) {
            return f.ConfigLoader().Load()
        }, func() {
            f.ConfigLoader().Reset()
        }
}
```

Same simplification for `settingsFunc`.

### 7. Clean up root.go PersistentPreRunE

After removing WorkDir, BuildOutputDir, and Debug mutations, PersistentPreRunE becomes just logger init + debug logging:

```go
// debug is a local var bound to --debug flag
PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
    initializeLogger(debug)
    logger.Debug().
        Str("version", f.Version).
        Str("workdir", f.WorkDir()).
        Bool("debug", debug).
        Msg("clawker starting")
    return nil
},
```

## Factory Field Coverage

All fields accounted for:

| Field | Status |
|---|---|
| `WorkDir` | `string` → `func() string` closure; default in factory, override in root |
| `BuildOutputDir` | **Removed** — resolved locally in generate command |
| `Debug` | **Removed** — local flag var in root; generate resolves locally |
| `Version` | struct literal |
| `Commit` | struct literal |
| `IOStreams` | `ioStreams(f)` |
| `Client`, `CloseClient` | `clientFunc()` |
| `RegistryLoader`, `Registry` | `registryFunc()` |
| `Resolution` | `resolutionFunc(f)` |
| `ConfigLoader` | `configLoaderFunc(f)` |
| `Config`, `ResetConfig` | `configFunc(f)` — pass-through to loader's cached Load/Reset |
| `SettingsLoader` | `settingsLoaderFunc(f)` |
| `Settings`, `InvalidateSettingsCache` | `settingsFunc(f)` — pass-through to loader's cached Load/Invalidate |
| `HostProxy`, `EnsureHostProxy`, `StopHostProxy`, `HostProxyEnvVar` | `hostProxyFunc()` |
| `Prompter` | `prompterFunc(f)` |
| `RuntimeEnv` | `runtimeEnvFunc(f)` |
| `BuildKitEnabled` | `buildKitEnabledFunc(f)` |

## Verification

```bash
go build ./cmd/clawker                         # Compile check
go test ./internal/cmd/factory/... -v -race    # Factory tests + race detector
go test ./internal/cmd/... -v                  # All command unit tests
make test                                       # Full unit suite
```
