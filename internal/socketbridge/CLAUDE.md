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

## Testing

```go
mock := &socketbridgetest.MockManager{
    EnsureBridgeFn: func(id string, gpg bool) error { return nil },
}
// Use in Factory:
f.SocketBridge = func() socketbridge.SocketBridgeManager { return mock }
// Assert:
mock.AssertCalledWith(t, "EnsureBridge", containerID, true)
```

## Gotchas

- `EnsureBridge` must receive container **ID** (not name) for consistent PID file keying
- Context cancellation after `bridge.Start()` kills the exec'd process -- don't cancel on success
- GPG needs `pubring.kbx` **file** (not directory) with exported public key
- File ownership (`chown`) matters for GPG sockets inside containers
