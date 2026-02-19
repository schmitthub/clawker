# Config Foundation Migration: hostproxy + socketbridge → cfg config.Config

> **Status:** COMPLETE — hostproxy and socketbridge both fully migrated
> **Branch:** `refactor/configapocalypse`
> **Parent memory:** `config-migration-foundation-packages` (has full TODO list for all packages)
> **Last updated:** 2026-02-19

## Goal

Migrate `internal/hostproxy` and `internal/socketbridge` from old removed `config.*` free functions to the new `cfg config.Config` interface pattern (storing cfg on structs, matching `docker.Client` pattern).

## Design Decisions Made

1. **cfg on struct pattern** — Store `cfg config.Config` on Manager/Daemon structs, pass via constructor
2. **DefaultDaemonOptions returns error** — `DefaultDaemonOptions(cfg config.Config) (DaemonOptions, error)` — does NOT swallow the `cfg.HostProxyPIDFilePath()` error
3. **Config on DaemonOptions** — `DaemonOptions.Config config.Config` field, copied to `Daemon.cfg`
4. **Label fields removed from Daemon** — `labelManaged`, `managedLabelValue`, `labelMonitoringStack` fields replaced by `d.cfg.LabelManaged()` etc. in `countClawkerContainers`
5. **serve.go restructured** — Flag vars are separate from opts; cfg loaded inside RunE; `DefaultDaemonOptions(cfg)` called inside RunE; flag overrides applied via `cmd.Flags().Changed()`
6. **factory hostProxyFunc takes f** — `hostProxyFunc(f *cmdutil.Factory)` gets cfg from `f.Config()` inside the lazy closure
7. **EnsureDir calls removed** — Subdir methods (`BridgesSubdir()`, `LogsSubdir()`) already call `os.MkdirAll` internally
8. **Test env var via cfg** — Use `cfg.ConfigDirEnvVar()` (returns `"CLAWKER_CONFIG_DIR"`) instead of hardcoded env var names. Old tests used wrong env var `"CLAWKER_HOME"`.
9. **Subdir name: pids not bridges** — `BridgesSubdir()` returns the `pids` subdir (legacy alias). Test fixtures should use `pids/` not `bridges/` for PID file paths.
10. **copylocks warnings are false positives** — `config.Config` is an interface; `configImpl` is always `*configImpl` (pointer receiver). The linter traces through the interface to the concrete type's mutex. Safe to ignore.

## Completed Steps

### 1. ✅ hostproxy/manager.go — DONE (was already rewritten before this session)
- `Manager{cfg config.Config, port int, mu sync.Mutex}`
- `NewManager(cfg config.Config) *Manager`
- `NewManagerWithPort(cfg config.Config, port int) *Manager`
- Removed `NewManagerWithOptions` (was test-only)
- All methods use `m.cfg.HostProxyPIDFilePath()`, `m.cfg.HostProxyLogFilePath()`
- Nil checks on `m.cfg` for graceful degradation

### 2. ✅ hostproxy/manager_test.go — DONE
- All `NewManager()` → `NewManager(config.NewMockConfig())`
- All `NewManagerWithPort(port)` → `NewManagerWithPort(config.NewMockConfig(), port)`
- `TestManagerWithOptions` removed (constructor deleted)
- Added `config` import

### 3. ✅ hostproxy/daemon.go — DONE
- `DaemonOptions{Config config.Config, ...}` — removed individual label fields
- `Daemon{cfg config.Config, ...}` — removed `labelManaged`, `managedLabelValue`, `labelMonitoringStack`
- `DefaultDaemonOptions(cfg config.Config) (DaemonOptions, error)` — returns error from `cfg.HostProxyPIDFilePath()`
- `NewDaemon(opts)` copies `opts.Config` to `d.cfg`
- `countClawkerContainers` uses `d.cfg.LabelManaged()`, `d.cfg.ManagedLabelValue()`, `d.cfg.LabelMonitoringStack()`

### 4. ✅ hostproxy/daemon_test.go — DONE
- `DefaultDaemonOptions(config.NewMockConfig())` with error check
- All `DaemonOptions{}` literals have `Config: config.NewMockConfig()`
- All `&Daemon{}` literals have `cfg: config.NewMockConfig()`
- All tests pass: `go test ./internal/hostproxy/ -timeout 30s` ✅

### 5. ✅ cmd/hostproxy/serve.go — DONE
- `loadDaemonConfig()` helper removed; each RunE creates `config.NewConfig()` directly
- `NewCmdServe` — flag vars separate from opts; cfg+opts created inside RunE; flag overrides via `cmd.Flags().Changed()`
- `NewCmdStatus` — loads cfg inside RunE
- `NewCmdStop` — loads cfg inside RunE

### 6. ✅ cmd/factory/default.go — DONE
- `hostProxyFunc(f *cmdutil.Factory)` — gets cfg from `f.Config()` inside lazy closure
- `f.HostProxy = hostProxyFunc(f)` moved after Factory creation (needs f.Config)
- `socketBridgeFunc()` still needs update (task #6 below)

### 7. ✅ socketbridge/manager.go — DONE
- `Manager{cfg config.Config, ...}`
- `NewManager(cfg config.Config) *Manager`
- `config.BridgePIDFile(id)` → `m.cfg.BridgePIDFilePath(id)`
- `config.BridgesDir()` → `m.cfg.BridgesSubdir()`
- `config.LogsDir()` → `m.cfg.LogsSubdir()`
- `config.EnsureDir(dir)` — removed (subdir methods ensure dirs)
- Builds: `go build ./internal/socketbridge/` ✅

### 8. ✅ socketbridge/manager_test.go — DONE
- Added `config` import
- Created `newTestManager(t, dir) *Manager` helper using `cfg.ConfigDirEnvVar()`
- `TestNewManager` updated
- `TestManagerIsRunning` — **3 subtests updated** to use `newTestManager`
- All test functions updated to use `newTestManager(t, dir)` with config-backed paths

## Remaining TODO Sequence

All items 9-12 COMPLETE. Committed and pushed in refactor/configapocalypse branch.

### 13. [ ] (Future) Remaining foundation packages from parent memory
- `internal/workspace` — `EnsureShareDir(cfg)`, `cli.VolumeLabels()`, tests
- `internal/containerfs` — `PrepareOnboardingTar(cfg, ...)`, `PreparePostInitTar(cfg, ...)`
- See `config-migration-foundation-packages` memory for full details

## Files Modified (uncommitted)

| File | Status |
|------|--------|
| `internal/hostproxy/manager.go` | Modified (was already touched before session) |
| `internal/hostproxy/manager_test.go` | Modified |
| `internal/hostproxy/daemon.go` | Modified |
| `internal/hostproxy/daemon_test.go` | Modified |
| `internal/cmd/hostproxy/serve.go` | Modified |
| `internal/cmd/factory/default.go` | Modified |
| `internal/socketbridge/manager.go` | Modified |
| `internal/socketbridge/manager_test.go` | Modified |

## Key Config Interface Methods Used

```
HostProxyPIDFilePath() (string, error)
HostProxyLogFilePath() (string, error)
BridgePIDFilePath(containerID string) (string, error)
BridgesSubdir() (string, error)    // legacy alias — returns pids dir
LogsSubdir() (string, error)
LabelManaged() string
ManagedLabelValue() string
LabelMonitoringStack() string
ConfigDirEnvVar() string           // "CLAWKER_CONFIG_DIR"
```

## Test Stubs Used

- `config.NewMockConfig()` — default in-memory config for all tests
- `cfg.ConfigDirEnvVar()` — get env var name for test isolation (set to t.TempDir())

---

**IMPERATIVE:** Always check with the user before proceeding with the next todo item. If all work is done, ask the user if they want to delete this memory.
