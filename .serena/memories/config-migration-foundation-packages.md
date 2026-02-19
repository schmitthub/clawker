# Config Foundation Package Migration — cfg config.Config Pattern

> **Status:** In Progress — hostproxy manager.go partially rewritten, 3 packages not started
> **Branch:** `refactor/configapocalypse`
> **Parent memory:** `configapocalypse-prd`, `configapocalypse-foundation-migration` (OUTDATED — design changed)
> **Last updated:** 2026-02-19

## Design Decision Change

**OLD approach** (from `configapocalypse-foundation-migration` memory): Pass resolved string values as individual params.
**NEW approach** (user-approved): Store `cfg config.Config` on structs, matching `docker.Client` pattern.

This is cleaner because:
1. Consistent with docker.Client pattern (`NewClient(ctx, cfg, opts...)`)
2. Clean constructor signatures
3. Future-proof — new config values don't change constructors
4. Easy to test with `config.NewMockConfig()`

## CRITICAL: config.Config is a Concrete Struct, NOT an Interface

Despite the name `Config` being declared as an `interface` in `config.go`, the **copylocks lint** fires because the concrete `configImpl` wrapping `*viper.Viper` contains `sync.RWMutex`. This means:

1. **Cannot compare to nil with `==`** — use a helper or pointer check instead
2. **Must pass by pointer** — `cfg config.Config` is an interface, but the underlying value has a mutex
3. **The nil checks `m.cfg == nil` cause compile errors** — need `m.hasCfg()` helper or different nil guard pattern

**Fix needed:** The `config.Config` type is an interface, so nil comparison should work IF the variable is typed as the interface. The actual compiler error `mismatched types config.Config and untyped nil` suggests config.Config might be a struct alias, not an interface. **Must investigate `internal/config/config.go` line 32-78 to confirm the actual type.**

Update: Looking at the errors more carefully — `config.Config has no field or method HostProxyPIDFilePath` — this means the method hasn't been added to the interface yet, OR it's named differently. **Must check the actual Config interface methods.** The memory says these methods exist but the compiler disagrees. Possible causes:
- Methods are on `configImpl` but not on the `Config` interface
- Methods have different names than expected
- The config package hasn't been rebuilt/saved

## Current Build State

**NOT building.** `manager.go` has been rewritten but has compile errors:
- `m.cfg == nil` — mismatched types (config.Config vs untyped nil) — need to investigate type
- `m.cfg.HostProxyPIDFilePath` undefined — method not on Config interface
- `m.cfg.HostProxyLogFilePath` undefined — method not on Config interface
- copylocks warning — Config contains sync.RWMutex
- `manager_test.go` — all constructor calls have wrong arg counts (not yet updated)
- `daemon_test.go` — `DefaultDaemonOptions()` wrong args (not yet updated)
- `daemon.go` — not yet modified
- `serve.go` — not yet modified
- `factory/default.go` — not yet modified

## Files Modified So Far

### internal/hostproxy/manager.go — REWRITTEN (has compile errors)
- Struct: `Manager{cfg config.Config, port int, mu sync.Mutex}`
- `NewManager(cfg config.Config) *Manager`
- `NewManagerWithPort(cfg config.Config, port int) *Manager`
- Removed `NewManagerWithOptions` (was test-only)
- `isDaemonRunning()` — uses `m.cfg.HostProxyPIDFilePath()` (BROKEN — method not found)
- `startDaemon()` — uses `m.cfg.HostProxyPIDFilePath()` for --pid-file flag
- `StopDaemon()` — uses `m.cfg.HostProxyPIDFilePath()`
- `openDaemonLogFile()` — uses `m.cfg.HostProxyLogFilePath()`
- Removed `path/filepath` import (no longer needed)

## TODO Sequence

### 1. [BLOCKED] Fix hostproxy/manager.go compile errors
- [ ] Investigate why `config.Config` has no `HostProxyPIDFilePath`/`HostProxyLogFilePath` — check actual interface definition
- [ ] Fix nil comparison pattern for config.Config (interface vs struct issue)
- [ ] Fix copylocks warning

### 2. [NOT STARTED] Update hostproxy/daemon.go
- [ ] Add `Config config.Config` field to DaemonOptions
- [ ] Store `cfg` on Daemon struct, remove individual label fields (`labelManaged`, `managedLabelValue`, `labelMonitoringStack`)
- [ ] `DefaultDaemonOptions(cfg config.Config)` — get PIDFile from `cfg.HostProxyPIDFilePath()`, store cfg
- [ ] `NewDaemon(opts)` — copy `opts.Config` to `d.cfg`
- [ ] `countClawkerContainers` — use `d.cfg.LabelManaged()`, `d.cfg.ManagedLabelValue()`, `d.cfg.LabelMonitoringStack()`

### 3. [NOT STARTED] Update hostproxy tests
- [ ] `manager_test.go` — update all constructor calls to `NewManager(config.NewMockConfig())` / `NewManagerWithPort(config.NewMockConfig(), port)`
- [ ] `daemon_test.go` — update `DefaultDaemonOptions()` → `DefaultDaemonOptions(config.NewMockConfig())`, update Daemon struct literals

### 4. [NOT STARTED] Update hostproxy callers
- [ ] `internal/cmd/factory/default.go` — `hostProxyFunc()` → `hostProxyFunc(f *cmdutil.Factory)`, get cfg from `f.Config()` inside lazy closure
- [ ] `internal/cmd/hostproxy/serve.go` — `DefaultDaemonOptions()` → create cfg via `config.NewConfig()`, pass to `DefaultDaemonOptions(cfg)`
- [ ] `serve.go` status/stop commands also call `DefaultDaemonOptions()` — same fix

### 5. [NOT STARTED] Migrate internal/socketbridge
- [ ] Add `cfg config.Config` field to Manager
- [ ] `NewManager(cfg config.Config)` — store cfg
- [ ] Replace `config.BridgePIDFile(containerID)` → `m.cfg.BridgePIDFilePath(containerID)`
- [ ] Replace `config.BridgesDir()` → `m.cfg.PidsSubdir()`
- [ ] Replace `config.LogsDir()` → `m.cfg.LogsSubdir()`
- [ ] Replace `config.EnsureDir(path)` → removed (Subdir methods ensure dirs)
- [ ] Update `manager_test.go` — `NewManager()` → `NewManager(config.NewMockConfig())`
- [ ] Update `factory/default.go` `socketBridgeFunc()` → `socketBridgeFunc(f *cmdutil.Factory)`

### 6. [NOT STARTED] Migrate internal/workspace
- [ ] `EnsureShareDir()` → `EnsureShareDir(cfg config.Config)` — use `cfg.ShareSubdir()` (already ensures dir)
- [ ] `EnsureConfigVolumes` — change `docker.VolumeLabels(...)` → `cli.VolumeLabels(...)` (already a Client method)
- [ ] Update `strategy_test.go`:
  - `config.clawkerHomeEnv` (unexported) → use `cfg.ConfigDirEnvVar()` or hardcode `"CLAWKER_HOME"`
  - `config.ShareSubdir` (unexported) → compute expected path from mock config
- [ ] Update `setup.go` caller of `EnsureShareDir()`

### 7. [NOT STARTED] Migrate internal/containerfs
- [ ] `PrepareOnboardingTar(containerHomeDir)` → `PrepareOnboardingTar(cfg config.Config, containerHomeDir string)` — use `cfg.ContainerUID()`, `cfg.ContainerGID()`
- [ ] `PreparePostInitTar(script)` → `PreparePostInitTar(cfg config.Config, script string)` — same
- [ ] Update `containerfs_test.go` — pass `config.NewMockConfig()` as first arg
- [ ] Update caller `internal/cmd/container/shared/containerfs.go`:
  - `InjectOnboardingOpts` / `InjectPostInitOpts` — need cfg param or get it from CreateContainerConfig
  - Lines 131, 164 — add cfg arg to `PrepareOnboardingTar`, `PreparePostInitTar`

### 8. [NOT STARTED] Verify build + tests
- [ ] `go build ./...`
- [ ] `make test`

### 9. [NOT STARTED] Update documentation and memories
- [ ] Update package CLAUDE.md files (hostproxy, socketbridge, workspace, containerfs)
- [ ] Update `configapocalypse-foundation-migration` memory or delete it
- [ ] Update `configapocalypse-prd` memory migration status

## Key Config Interface Methods (from earlier exploration)

From the Config interface (config.go lines 32-78):
- `HostProxyPIDFilePath() (string, error)` — line 54
- `HostProxyLogFilePath() (string, error)` — line 53
- `BridgePIDFilePath(containerID string) (string, error)` — line 52
- `PidsSubdir() (string, error)` — line 51
- `BridgesSubdir() (string, error)` — line 50 (legacy alias for PidsSubdir)
- `LogsSubdir() (string, error)` — line 49
- `ShareSubdir() (string, error)` — line 55
- `LabelManaged() string` — line 57
- `ManagedLabelValue() string` — line 71
- `LabelMonitoringStack() string` — line 58
- `ContainerUID() int` — line 74 (was `config.ContainerUID` constant)
- `ContainerGID() int` — line 75 (was `config.ContainerGID` constant)
- `ConfigDirEnvVar() string` — line 44

## Files That Need Changes (Complete List)

| File | Status | Changes |
|------|--------|---------|
| `internal/hostproxy/manager.go` | REWRITTEN (broken) | cfg on struct, new constructors |
| `internal/hostproxy/daemon.go` | Not started | cfg on Daemon, labels from cfg |
| `internal/hostproxy/manager_test.go` | Not started | Update constructor args |
| `internal/hostproxy/daemon_test.go` | Not started | Update constructor args |
| `internal/cmd/hostproxy/serve.go` | Not started | Create cfg, pass to DefaultDaemonOptions |
| `internal/cmd/factory/default.go` | Not started | hostProxyFunc/socketBridgeFunc take f |
| `internal/socketbridge/manager.go` | Not started | cfg on struct, replace config.* calls |
| `internal/socketbridge/manager_test.go` | Not started | Update constructor args |
| `internal/workspace/strategy.go` | Not started | EnsureShareDir(cfg), cli.VolumeLabels |
| `internal/workspace/strategy_test.go` | Not started | Fix unexported config refs |
| `internal/workspace/setup.go` | Not started | Pass cfg to EnsureShareDir |
| `internal/containerfs/containerfs.go` | Not started | cfg param on Prepare*Tar funcs |
| `internal/containerfs/containerfs_test.go` | Not started | Pass mock config |
| `internal/cmd/container/shared/containerfs.go` | Not started | Thread cfg to Prepare*Tar calls |

---

**IMPERATIVE:** Always check with the user before proceeding with the next todo item. If all work is done, ask the user if they want to delete this memory.
