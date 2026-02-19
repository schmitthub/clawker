# Config Foundation Package Migration (hostproxy, socketbridge, workspace, containerfs)

> **Status:** In Progress — hostproxy partially edited, 3 packages not started
> **Branch:** `refactor/configapocalypse`
> **Parent memory:** `configapocalypse-prd` (full PRD context)
> **Last updated:** 2026-02-19

## Goal

Migrate 4 foundation packages from deleted `config.*` free functions/constants to the `config.Config` interface methods, unblocking `go build ./...`.

## Config Interface Methods Available (user added these)

The user has already updated the Config interface with these new methods:
- `PidsSubdir() (string, error)` — ensures + returns `<ConfigDir()>/pids`
- `HostProxyPIDFilePath() (string, error)` — ensures pids dir + returns full path
- `HostProxyLogFilePath() (string, error)` — ensures logs dir + returns full path
- `BridgesSubdir() (string, error)` — legacy alias, now returns pids dir (same as PidsSubdir)
- `ShareSubdir() (string, error)` — ensures + returns share dir
- `LogsSubdir() (string, error)` — ensures + returns logs dir
- `LabelManaged()`, `ManagedLabelValue()`, `LabelMonitoringStack()` — label methods
- `ContainerUID() int`, `ContainerGID() int` — container ownership

**Key design decision from user:** "if functions need these values pass in as params" — resolved values should be passed as function parameters, not by storing `cfg config.Config` on structs.

## Migration Steps

### 1. [IN PROGRESS] internal/hostproxy

**Files changed (partially):**
- `daemon.go` — ✅ DONE: removed config import, added label fields to Daemon struct, added label fields to DaemonOptions, changed `DefaultDaemonOptions()` to accept `(pidFile, labelManaged, managedLabelValue, labelMonitoringStack string)`, updated `countClawkerContainers` to use `d.labelManaged` etc., updated `NewDaemon` to copy label values from opts.
- `manager.go` — ⚠️ PARTIAL: removed config import, changed `NewManager` and `NewManagerWithPort` signatures to accept `(pidFile string, logFileFn func() (string, error))`. **STILL NEEDS:** update `openDaemonLogFile()` to use `m.logFileFn` instead of `config.HostProxyLogFile()`/`config.LogsDir()`/`config.EnsureDir()`. Lines 263-273 still reference `config.*`.

**Files NOT yet changed:**
- `manager_test.go` — needs updated constructor calls: `NewManager(pidFile, logFileFn)`, `NewManagerWithPort(pidFile, logFileFn, port)`, `DefaultDaemonOptions(pidFile, labels...)`. Use `config.NewMockConfig()` to get resolved values in tests.
- `daemon_test.go` — needs `DefaultDaemonOptions(...)` call updated with 4 string args.

**Callers NOT yet updated:**
- `internal/cmd/factory/default.go` — `hostProxyFunc()` calls `hostproxy.NewManager()` → needs cfg to resolve pidFile and logFileFn
- `internal/cmd/hostproxy/serve.go` — calls `hostproxy.DefaultDaemonOptions()` → needs cfg to resolve pidFile and label values. Pattern: create `cfg, _ := config.NewConfig()` at top of serve command, then call `cfg.HostProxyPIDFilePath()`, `cfg.LabelManaged()`, etc.

**openDaemonLogFile remaining fix:**
```go
// OLD (lines 263-278):
func (m *Manager) openDaemonLogFile() (*os.File, error) {
    logPath, err := config.HostProxyLogFile()  // BROKEN
    // ...
    logsDir, err := config.LogsDir()           // BROKEN
    // ...
    config.EnsureDir(logsDir)                  // BROKEN
}

// NEW:
func (m *Manager) openDaemonLogFile() (*os.File, error) {
    if m.logFileFn == nil {
        return nil, fmt.Errorf("log file path function not configured")
    }
    logPath, err := m.logFileFn()
    if err != nil {
        return nil, fmt.Errorf("failed to get log file path: %w", err)
    }
    // LogsSubdir already ensures dir exists via subdirPath(), 
    // but ensure parent dir for safety
    if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
        return nil, fmt.Errorf("failed to create logs directory: %w", err)
    }
    file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
    ...
}
```

### 2. [NOT STARTED] internal/socketbridge

**Old references in `manager.go`:**
- `config.BridgePIDFile(containerID)` → derive from `cfg.PidsSubdir()` + `<containerID>.pid`
- `config.BridgesDir()` → `cfg.PidsSubdir()` (BridgesSubdir is legacy alias)
- `config.LogsDir()` → `cfg.LogsSubdir()`
- `config.EnsureDir(path)` → `os.MkdirAll(path, 0o755)` (subdirPath already creates dirs)

**Migration plan:**
- Add `pidsDir string` and `logsDir string` fields to Manager (or pass resolved dir paths through constructor)
- `NewManager(pidsDir, logsDir string)` — caller resolves from cfg
- `BridgePIDFile` becomes: `filepath.Join(m.pidsDir, containerID+".pid")`
- Update `EnsureBridge`, `StopBridge`, `StopAll`, `IsRunning`, `startBridge`, `openBridgeLogFile`
- Update `manager_test.go` and `socketbridgetest/mock.go` if needed
- Update callers: `internal/cmd/factory/default.go` socketBridgeFunc()

### 3. [NOT STARTED] internal/workspace

**Old references:**
- `strategy.go:146` — `config.ShareDir()` → `cfg.ShareSubdir()` (need to thread through)
- `strategy.go:150` — `config.EnsureDir(sharePath)` → not needed (ShareSubdir ensures via subdirPath)
- `strategy.go:105,116` — `docker.VolumeLabels(...)` → `cli.VolumeLabels(...)` (method on *Client, already migrated in docker pkg)
- `setup.go:54` — `config.IgnoreFileName` → need to check if this exists or add to Config interface
- `setup.go:81` — `config.ParseMode(modeStr)` → still exists as free function ✅
- `setup.go:20` — `config.Project` → still exists as schema type ✅
- `strategy_test.go:45,68` — `config.clawkerHomeEnv` → private; use `cfg.ConfigDirEnvVar()` 
- `strategy_test.go:52` — `config.ShareSubdir` → private constant; compute expected from cfg

**Migration plan:**
- `EnsureShareDir()` → needs cfg param for `cfg.ShareSubdir()`. Since subdirPath already creates dir, can simplify to just call `cfg.ShareSubdir()`.
- `EnsureConfigVolumes` uses `docker.VolumeLabels()` which is now a Client method → change to `cli.VolumeLabels()`
- `config.IgnoreFileName` — check if it's on the interface or needs to be added
- Update tests to use `config.NewMockConfig()` for cfg values

### 4. [NOT STARTED] internal/containerfs

**Old references in `containerfs.go`:**
- `config.ContainerUID` (lines 172, 208, 221) → pass as param `uid int`
- `config.ContainerGID` (lines 173, 209, 222) → pass as param `gid int`

**Migration plan:**
- `PrepareOnboardingTar(containerHomeDir string, uid, gid int)` — add uid/gid params
- `PreparePostInitTar(script string, uid, gid int)` — add uid/gid params
- Update callers (in container/shared/ CreateContainer flow)
- Update `containerfs_test.go` to pass uid/gid (use `config.NewMockConfig().ContainerUID()`)

### 5. [NOT STARTED] Update all callers & verify build

- `internal/cmd/factory/default.go` — hostProxyFunc, socketBridgeFunc need cfg
- `internal/cmd/hostproxy/serve.go` — needs `config.NewConfig()` for DefaultDaemonOptions
- Any other callers of workspace.EnsureShareDir, containerfs.PrepareOnboardingTar, etc.

### 6. [NOT STARTED] Update package CLAUDE.md files

- `internal/hostproxy/CLAUDE.md` — update constructor signatures
- `internal/socketbridge/CLAUDE.md` — update constructor signatures
- `internal/workspace/CLAUDE.md` — update EnsureShareDir, EnsureConfigVolumes
- `internal/containerfs/CLAUDE.md` — update Prepare* signatures

### 7. [NOT STARTED] Update configapocalypse-prd memory

Mark items 2-6 as done, update migration status.

## Current Build State

**NOT building.** hostproxy has partial edits with broken references:
- `manager.go:263-273` still references `config.HostProxyLogFile`, `config.LogsDir`, `config.EnsureDir`
- `manager.go:9` has unused `path/filepath` import (will be used once openDaemonLogFile is fixed)
- `manager_test.go` has wrong arg counts for updated constructors
- `daemon_test.go` has wrong arg count for `DefaultDaemonOptions`

## Lessons Learned

1. The user prefers passing resolved values as params rather than storing `cfg config.Config` on structs
2. The Config interface `*Subdir()` methods already ensure dirs exist via `subdirPath()` — no need for separate `EnsureDir` calls
3. `BridgesSubdir()` is a legacy alias for `PidsSubdir()` — both return the pids directory now
4. `docker.VolumeLabels` is now a method on `*docker.Client`, not a free function

---

**IMPERATIVE:** Always check with the user before proceeding with the next todo item. If all work is done, ask the user if they want to delete this memory.
