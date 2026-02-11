# SocketBridge Package

SSH/GPG agent forwarding via muxrpc protocol over `docker exec` stdin/stdout.

## Architecture

```
Host                                    Container
┌──────────────┐   docker exec -i   ┌──────────────────────┐
│  Manager     │──────────────────→ │ clawker-socket-server │
│  (per-CLI)   │                    │ (per-container)       │
│              │   muxrpc protocol  │                       │
│  Bridge ←────┼───stdin/stdout────→│  Unix socket listeners│
│  (per-ctr)   │                    │  ~/.ssh/agent.sock    │
│              │                    │  ~/.gnupg/S.gpg-agent │
└──────────────┘                    └──────────────────────┘
```

**Manager** spawns detached `clawker bridge serve` subprocesses (one per container).
**Bridge** handles the muxrpc session: multiplexes socket connections between host agents and container sockets.
**clawker-socket-server** runs inside the container, creates Unix sockets, and forwards connections via the protocol.

## Interface

```go
type SocketBridgeManager interface {
    EnsureBridge(containerID string, gpgEnabled bool) error
    StopBridge(containerID string) error
    StopAll() error
    IsRunning(containerID string) bool
}
```

Concrete implementation: `Manager`. Mock: `socketbridgetest.MockManager`.

## Key Files

| File | Purpose |
|------|---------|
| `manager.go` | `Manager` -- spawns/tracks bridge daemon subprocesses via PID files |
| `bridge.go` | `Bridge` -- host-side muxrpc session over docker exec |
| `bridge_test.go` | Unit tests for Bridge, sendMessage, readLoop |
| `manager_test.go` | Unit tests for Manager, PID file handling, process checks |
| `socketbridgetest/mock.go` | `MockManager` -- test fake with call tracking |

## Protocol

Wire format: `[4-byte length][1-byte type][4-byte streamID][payload]`

| Type | Value | Direction | Purpose |
|------|-------|-----------|---------|
| DATA | 1 | Both | Socket data |
| OPEN | 2 | Container->Host | New connection (payload = socket type) |
| CLOSE | 3 | Both | Connection closed |
| PUBKEY | 4 | Host->Container | GPG public key data |
| READY | 5 | Container->Host | Forwarder initialized |
| ERROR | 6 | Container->Host | Error message |

Constants: `ProtocolVersion`, `readBufSize` (64KB), `maxMessageSize` (1MB).

## Manager Lifecycle

1. `EnsureBridge(containerID, gpgEnabled)` -- idempotent; checks in-memory tracking, then PID file, then spawns new daemon
2. Daemon runs `clawker bridge serve --container <id> --pid-file <path> [--gpg]`
3. Daemon is detached (`Setsid: true`), persists across CLI invocations
4. `StopBridge(containerID)` -- kills process, removes PID file
5. `StopAll()` -- scans bridges directory for all PID files

**Lifecycle integration with container commands:**
- `run`, `start`, `exec` call `EnsureBridge` to start the daemon
- `stop`, `remove` call `StopBridge` before the Docker operation to prevent stale bridges
- Without stop-side cleanup, a quick restart reuses the old bridge whose docker exec session is dead

**Three-layer lifecycle defense:**

| Layer | Where | Covers |
|-------|-------|--------|
| Docker events stream | Bridge daemon (`bridge serve`) | ALL deaths (crash, kill, OOM, Docker restart, stop, rm) |
| Stop/rm hooks | `container stop`, `container rm` | Happy-path CLI usage |
| EnsureBridge container inspect | Manager.EnsureBridge | Safety net for missed events (future) |

The bridge daemon subscribes to Docker `die` events for the target container. On `die` (or stream disconnect), it calls `bridge.Stop()` and cancels context, ensuring the PID file is cleaned up immediately. This covers crashes, external kills, OOM, and Docker restarts — not just happy-path CLI stop/remove.

## Testing

```go
mock := socketbridgetest.NewMockManager()
mock.EnsureBridgeFn = func(id string, gpg bool) error { return nil }
// Use in Factory:
f.SocketBridge = func() socketbridge.SocketBridgeManager { return mock }
// Assert (returns bool, no *testing.T):
assert.True(t, mock.CalledWith("EnsureBridge", containerID))
// Inspect raw calls:
// mock.Calls []Call — each Call has Method string and Args []any
```

**Call tracking**: `Call` struct (`Method string`, `Args []any`). `MockManager.Calls []Call` records all invocations in order. `CalledWith(method, containerID) bool` checks if a method was called with the given container ID as first arg.

## Gotchas

- `EnsureBridge` must receive container **ID** (not name) for consistent PID file keying
- Context cancellation after `bridge.Start()` kills the exec'd process -- don't cancel on success
- GPG needs `pubring.kbx` **file** (not directory) with exported public key
- File ownership (`chown`) matters for GPG sockets inside containers
