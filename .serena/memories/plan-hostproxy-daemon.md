# Plan: Daemonize Host Proxy Server

**Status:** Implemented (pending network-dependent full build verification)
**Branch:** TBD (create from `main`)
**Issue:** Host browser forwarder stops working because the proxy runs in-process with the CLI and dies when the CLI exits.

## Problem

The `hostproxy.Manager` runs an HTTP server in goroutines within the CLI process. When the CLI command exits (detach, `start` without `--attach`, command completion), the proxy dies. Containers that are still running cannot reach `/open/url` and get `connection refused`.

**Reproduction test:** `TestManagerProxyUnavailableAfterStop` in `internal/hostproxy/manager_test.go` (already committed on `a/build-refactor` branch).

### Affected Commands

| Command | Behavior | Problem |
|---------|----------|---------|
| `clawker run @` | Proxy lives during attach, dies on detach | Gap between detach and next attach |
| `clawker start @` (no attach) | Proxy lives only during `startRun()` | Proxy dies immediately after start completes |
| `clawker attach @` | Proxy lives during attach | Gap between detach and next attach |
| `clawker create @` | Proxy lives during `createRun()` | Proxy dies immediately after create completes |

### Call Sites

- `internal/cmd/container/run/run.go:221`
- `internal/cmd/container/create/create.go:199`
- `internal/cmd/container/start/start.go:110`
- `internal/cmd/container/attach/attach.go:116`

None of these call `hp.Stop()`. The proxy just dies with the process.

## Design

### Approach: Subprocess Daemon with PID File

Fork the host proxy as a separate background process that persists beyond CLI lifetime. Use a PID file for lifecycle management.

**Why not systemd/launchd?** Too platform-specific and heavyweight. A simple self-daemonizing subprocess covers macOS and Linux with minimal complexity.

### PID File Location

`~/.local/clawker/hostproxy.pid` — alongside other clawker state files. Use `config.ClawkerHome()` to resolve.

### Lifecycle

```
Manager.EnsureRunning()
  |
  +- Check PID file exists AND process alive AND health check passes
  |   +- Yes -> touch heartbeat, return nil (reuse existing daemon)
  |
  +- Check port in use by clawker proxy (existing isPortInUse check)
  |   +- Yes -> adopt (write PID file if missing), touch heartbeat, return nil
  |
  +- No running proxy found
      +- Start subprocess: `clawker host-proxy serve --port PORT`
      +- Write PID to pidfile
      +- Health check with retry (up to 2s)
      +- Touch heartbeat
      +- Return nil on success
```

### Daemon Entry Point: Hidden Subcommand

**Option B (recommended):** `clawker host-proxy serve` — hidden subcommand on the main binary.
- `Manager.startDaemon()` runs `os.Executable()` with `host-proxy serve --port PORT`
- Single binary distribution, always available
- Not shown in help output

### Daemon Process Behavior

1. Creates `hostproxy.NewServer(port)`
2. Calls `server.Start()`
3. Writes own PID to pidfile
4. Handles SIGTERM/SIGINT gracefully
5. Logs to `~/.local/clawker/logs/hostproxy.log`
6. Auto-exits after idle timeout (see below)

### Auto-Shutdown (Idle Detection)

Prevents orphaned daemons via heartbeat file approach:

- Heartbeat file: `~/.local/clawker/hostproxy.heartbeat`
- CLI commands touch this file in `EnsureRunning()` (already called at command start)
- Daemon checks mtime every 60 seconds; exits if stale > 30 minutes
- Configurable via flag: `--idle-timeout 30m`
- No Docker SDK dependency in daemon binary

### Manager Changes

```go
func (m *Manager) EnsureRunning() error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 1. Check if daemon is running via PID file + health check
    if m.isDaemonRunning() {
        m.touchHeartbeat()
        return nil
    }

    // 2. Check if something is on the port (isPortInUse -- existing)
    if m.isPortInUse() {
        m.touchHeartbeat()
        return nil
    }

    // 3. Start daemon subprocess
    return m.startDaemon()
}
```

`Manager.Stop()` should NOT kill the daemon -- other CLI commands may be using it. The daemon self-terminates via idle detection. Add `Manager.StopDaemon()` for explicit teardown.

## Implementation Steps

1. **Add PID file management** (`internal/hostproxy/daemon.go`)
   - `writePIDFile()`, `readPIDFile()`, `removePIDFile()`, `isProcessAlive(pid)`
   - Platform-aware process checking (kill -0 on Unix)
   - Heartbeat touch/check functions

2. **Add hidden `host-proxy serve` subcommand** (`internal/cmd/hostproxy/serve.go`)
   - Creates server, writes PID, handles signals, runs heartbeat checker
   - Flags: `--port`, `--pidfile`, `--heartbeat-file`, `--idle-timeout`, `--log-file`

3. **Register in root command** (`internal/cmd/root/root.go`)
   - Add hidden `host-proxy` command group

4. **Refactor `Manager.EnsureRunning()`** (`internal/hostproxy/manager.go`)
   - Check PID file + health instead of in-process server state
   - Start daemon subprocess if not running
   - Touch heartbeat file on every call
   - Remove in-process `*Server` reference from Manager

5. **Update `Manager.Stop()`**
   - Change to only clean up local state (not kill daemon)
   - Add `StopDaemon()` for explicit daemon teardown

6. **Add path helpers** (`internal/config/home.go`)
   - `HostProxyPIDFile()` and `HostProxyHeartbeatFile()` (optional, could live in daemon.go)

7. **Add `host-proxy status/stop` commands** (optional, nice-to-have)

8. **Update tests** (`internal/hostproxy/manager_test.go`)

9. **Update documentation** (CLAUDE.md files, rules)

## Edge Cases

1. **Stale PID file:** Process died without cleanup. `isDaemonRunning()` checks process liveness (`kill -0`) AND health endpoint. If PID file points to dead process, clean up and restart.
2. **Port conflict:** Another service on 18374. Error with clear message.
3. **Permissions:** PID file / heartbeat in `~/.local/clawker/` -- same permissions as other state files.
4. **Multiple users:** Each user has their own `~/.local/clawker/hostproxy.pid`. No conflict.
5. **Binary location:** Use `os.Executable()` (not `os.Args[0]`) for subprocess spawn.
6. **Graceful upgrade:** New clawker version. Old daemon may be running. Health check includes version? Or just restart on version mismatch.

## Blast Radius Inventory

### Direct API Changes (MUST update)

| File | What Changes |
|------|-------------|
| `internal/hostproxy/manager.go` | Core refactor -- delegate to daemon subprocess instead of in-process server |
| `internal/hostproxy/daemon.go` | **NEW** -- PID file mgmt, subprocess start, heartbeat touch, process liveness |
| `internal/cmd/hostproxy/serve.go` | **NEW** -- hidden `host-proxy serve` subcommand (daemon entry point) |
| `internal/cmd/root/root.go` | Add hidden `host-proxy` command group registration |
| `internal/cmd/factory/default.go:92-103` | `hostProxyFunc()` -- may need to pass binary path to Manager |
| `internal/cmd/factory/default_test.go:123-132` | `TestFactory_HostProxy` -- verify factory still produces working Manager |
| `internal/hostproxy/manager_test.go` | **MUST update** -- tests now verify daemon subprocess behavior |

### Consumers of `Manager.EnsureRunning()` (behavior change, no API change)

Call signature unchanged. Proxy now persists after CLI exit (improvement, not breakage).

| File | Line | Command |
|------|------|---------|
| `internal/cmd/container/run/run.go` | 221 | `clawker run` |
| `internal/cmd/container/create/create.go` | 199 | `clawker create` |
| `internal/cmd/container/start/start.go` | 110 | `clawker start` |
| `internal/cmd/container/attach/attach.go` | 116 | `clawker attach` |

### Options Structs (type unchanged, no code changes)

Declare `HostProxy func() *hostproxy.Manager` -- type doesn't change.

| File | Struct |
|------|--------|
| `internal/cmdutil/factory.go:32` | `Factory.HostProxy` |
| `internal/cmd/container/run/run.go:35` | `RunOptions.HostProxy` |
| `internal/cmd/container/create/create.go:31` | `CreateOptions.HostProxy` |
| `internal/cmd/container/start/start.go:25` | `StartOptions.HostProxy` |
| `internal/cmd/container/attach/attach.go:24` | `AttachOptions.HostProxy` |

### Test Files (may need updates)

| File | Impact |
|------|--------|
| `internal/cmd/container/run/run_test.go:817-836` | Uses `hostproxy.NewManager()` -- should still work if Manager API unchanged |
| `test/harness/factory.go:33` | Test harness creates `hostproxy.NewManager()` -- should still work |
| `test/commands/container_create_test.go` | Command integration -- depends on Manager behavior |
| `test/commands/container_run_test.go` | Command integration -- depends on Manager behavior |

### Test Infrastructure (no changes expected)

| File | Why Safe |
|------|----------|
| `test/harness/builders/config_builder.go:131` | Sets `EnableHostProxy` config flag -- unrelated to Manager |
| `test/internals/scripts_test.go` | Uses `hostproxytest.NewMockHostProxy(t)` -- mock, not Manager |
| `test/internals/sshagent_test.go` | Uses mock -- unaffected |
| `test/internals/firewall_test.go` | Uses mock -- unaffected |
| `internal/hostproxy/hostproxytest/hostproxy_mock.go` | Mock implementation -- unchanged |

### Config/Schema (no changes)

| File | Why Safe |
|------|----------|
| `internal/config/schema.go:169-179` | `EnableHostProxy` field + `HostProxyEnabled()` -- read-only config |
| `internal/config/home.go` | May add path helper functions for PID/heartbeat files |

### Server Internals (unchanged, move to daemon)

All server code runs inside daemon subprocess, code itself is unchanged:
- `internal/hostproxy/server.go`
- `internal/hostproxy/session.go`
- `internal/hostproxy/callback.go`
- `internal/hostproxy/browser.go`
- `internal/hostproxy/git_credential.go`
- `internal/hostproxy/ssh_agent.go`

### Container-Side Scripts (no changes)

Scripts call same HTTP endpoints -- daemon serves identical API:
- `internal/hostproxy/internals/host-open.sh`
- `internal/hostproxy/internals/cmd/callback-forwarder/main.go`
- `internal/hostproxy/internals/cmd/ssh-agent-proxy/main.go`
- `internal/hostproxy/internals/git-credential-clawker.sh`
- `internal/hostproxy/internals/embed.go`

### Workspace Integration (no changes)

- `internal/workspace/git.go` -- env var setup, unaffected
- `internal/workspace/ssh.go` -- macOS SSH agent forwarding, same endpoints

### Documentation (update needed)

| File | Action |
|------|--------|
| `internal/hostproxy/CLAUDE.md` | Document daemon architecture, PID file, heartbeat |
| `.claude/rules/hostproxy.md` | Mention daemon lifecycle |
| Root `CLAUDE.md` | Add `host-proxy` to CLI commands if not hidden-only |

### Summary Counts

- **Files to create:** 2 (`daemon.go`, `serve.go`)
- **Files to modify:** ~5 (`manager.go`, `root.go`, `default.go`, `manager_test.go`, `home.go`)
- **Files to update docs:** 2-3
- **Files unchanged but verified safe:** ~30+
- **Total blast radius:** ~40 files reference hostproxy, ~8 actually changed

## Testing Strategy

- Unit tests for PID file operations (daemon.go)
- Unit tests for heartbeat file operations
- Integration test: start daemon, verify health, simulate CLI exit, verify daemon survives
- Integration test: idle timeout causes daemon exit
- Integration test: stale PID file cleanup and restart
