# Hostproxy Internals Package

Container-side scripts and binaries that communicate with the clawker host proxy server and socketbridge. These are embedded at Docker image build time and run inside containers.

## Key Files

| File | Purpose |
|------|---------|
| `embed.go` | `go:embed` directives + exported vars |
| `host-open.sh` | BROWSER handler — opens URLs via host proxy, intercepts OAuth callbacks |
| `git-credential-clawker.sh` | Git credential helper — forwards to host proxy `/git/credential` |
| `cmd/callback-forwarder/main.go` | OAuth callback polling — polls host proxy, forwards to local port with dual-stack fallback |
| `cmd/callback-forwarder/main_test.go` | Unit tests for callback-forwarder (URL building, IPv4/IPv6 fallback, error aggregation) |
| `cmd/clawker-socket-server/main.go` | Unix socket server — creates SSH/GPG sockets with symlink-safe `*at` writes, forwards via muxrpc protocol over stdin/stdout |
| `cmd/clawker-socket-server/main_test.go` | Unit tests for the symlink-safe home-directory writes (Linux only) |

## API

```go
// Embedded script/source variables
var HostOpenScript string           // host-open.sh
var GitCredentialScript string      // git-credential-clawker.sh
var CallbackForwarderSource string  // cmd/callback-forwarder/main.go
var SocketForwarderSource string    // cmd/clawker-socket-server/main.go
```

## Architecture

This is a **leaf package** (stdlib + embed only). It exports embedded content as string vars consumed by the `internal/bundler` package during Docker build context assembly.

The Go binaries under `cmd/` are standalone `package main` programs compiled inside the Docker image during multi-stage builds. They use only stdlib — no imports from the clawker module (there is no `go.mod` in that build stage, so importing `internal/consts` or any other module package breaks the image build). Their inline literals are therefore intentional and exempt from the no-hardcoded-strings policy; each file carries a TRIPWIRE header comment stating this.

## Callback Forwarder (`cmd/callback-forwarder/main.go`)

The callback forwarder polls the host proxy for captured OAuth callbacks and forwards them to a local HTTP server inside the container. Key types and behavior:

### Types

```go
type CallbackData struct {
    Method, Path, Query string
    Headers map[string]string
    Body, ReceivedAt string
}

type CallbackDataResponse struct {
    Received bool
    Callback *CallbackData
    Error    string
}
```

### Dual-Stack IPv4/IPv6 Fallback

`forwardCallback` tries forwarding to three hosts sequentially: `localhost`, `127.0.0.1`, `::1`. This ensures OAuth callbacks reach local servers regardless of whether they bind to IPv4, IPv6, or both. `buildLocalCallbackURL` handles IPv6 bracket notation (e.g., `[::1]:8080`).

If all three hosts fail, errors are aggregated into a single message listing each host's failure.

### Functions

| Function | Purpose |
|----------|---------|
| `forwardCallback(client, port, data)` | Tries localhost, 127.0.0.1, ::1 sequentially |
| `forwardCallbackToHost(client, host, port, data)` | Forwards to a single host, reconstructs the HTTP request |
| `buildLocalCallbackURL(host, port, data)` | Builds URL with IPv6 bracket notation support |
| `flagWasSet(name)` | Checks if a CLI flag was explicitly provided |

## Socket Server (`cmd/clawker-socket-server/main.go`)

The socket server is the container-side component of the socketbridge system. It:
1. Receives configuration via `CLAWKER_REMOTE_SOCKETS` env var (JSON array of `{path, type}`)
2. Creates Unix sockets at specified paths (e.g., `~/.ssh/agent.sock`, `~/.gnupg/S.gpg-agent`)
3. Receives GPG public key data via muxrpc protocol and writes to `~/.gnupg/pubring.kbx`, `gpg.conf` (no-autostart), and `gpg-agent.conf` (sensible container defaults: no-grab, disable-scdaemon)
4. Kills any pre-existing gpg-agent via `gpgconf --kill gpg-agent` (GPG's sanctioned mechanism — targets only the agent for the specific GNUPGHOME, no sudo needed)
5. Forwards socket connections through muxrpc messages over stdin/stdout to the host-side bridge
6. Logs to both stderr AND `/var/log/clawker/socket-server.log` (simple 1 MiB rotation)

The binary runs as root inside the agent-owned home, so every write is symlink-safe: `resolveSocketTarget` accepts only `/home/<user>/<dir>/<file>`; `openUserDir` opens the home and the directory with `O_DIRECTORY|O_NOFOLLOW` (creating the directory 0700 with `Mkdirat` when absent) and refuses a symlink, a non-directory, a foreign owner, or group/other write bits. Only a directory that this call created is chowned to the user, and only when it is empty: the user can rename another entry into its place between the mkdir and the open. An existing directory that the user does not own (for example a root-owned `~/.ssh` from the image) is refused, never taken over. `userDir.writeFile` and `userDir.listenUnix` use an `O_PATH|O_NOFOLLOW` stat, `Unlinkat`/`Openat(O_CREAT|O_EXCL|O_NOFOLLOW)` relative to that descriptor and bind the socket through `/proc/self/fd/<dirfd>/<name>` under a `0177` umask. Ownership is always changed through a descriptor (`Fchown` on the dir and file; `O_PATH` open checked as `S_IFSOCK` then `Fchownat(AT_EMPTY_PATH)` for the socket node), never by name, so a hard link swapped in by the agent cannot redirect a chown. Any refusal fails the bridge start with the path in the log. The file is `//go:build linux` because these syscalls are Linux-only. Tests: `cmd/clawker-socket-server/main_test.go`.

The host-side bridge (`internal/socketbridge`) launches this binary via `docker exec` and communicates using a binary muxrpc protocol.

### Muxrpc Protocol Constants

```go
const ProtocolVersion = 1

// Message types
const (
    MsgData   = 0x01  // Stream data
    MsgOpen   = 0x02  // Open stream
    MsgClose  = 0x03  // Close stream
    MsgPubkey = 0x04  // GPG public key transfer
    MsgReady  = 0x05  // Server ready signal
    MsgError  = 0x06  // Error message
)
```

### Key Types

```go
type SocketConfig struct {
    Path string  // Unix socket path
    Type string  // "ssh-agent" or "gpg-agent"
}

type Message struct {
    Type     byte
    StreamID uint32
    Payload  []byte
}

type Forwarder struct { /* manages streams, socket listeners, muxrpc I/O */ }
```

### GPG Socket Conflict Prevention (Multi-Layered)

| Layer | Mechanism | Purpose |
|-------|-----------|---------|
| 1 | `gpg.conf` with `no-autostart` | Prevents GPG from spawning gpg-agent on any GPG operation |
| 2 | `gpgconf --kill gpg-agent` | Kills any agent that started before our config was in place |
| 3 | `gpg-agent.conf` with `no-grab`, `disable-scdaemon` | Sensible container defaults (do NOT prevent socket binding) |

**Important:** GnuPG 2.1+ mandates the standard socket — no `gpg-agent.conf` directive can prevent socket binding. The real protection is layers 1 and 2.

### Troubleshooting Logs

Inside the container, the socket-server writes logs to:
- **stderr** (visible via `docker logs` or bridge stderr capture)
- **`/var/log/clawker/socket-server.log`** (persistent file, survives bridge restarts)

Log rotation: when the log exceeds 1 MiB, it is renamed to `socket-server.log.1` on next startup.

To inspect logs inside a running container:
```bash
docker exec <container> cat /var/log/clawker/socket-server.log
```

## Dependencies

- Imports: `embed` (stdlib only)
- Imported by: `internal/bundler`
- Does NOT import: `internal/hostproxy` or any other internal package
