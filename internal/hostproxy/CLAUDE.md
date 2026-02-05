# Host Proxy Package

HTTP service mesh mediating interactions between containers and the host machine.

## Architecture: Daemon Subprocess

The host proxy runs as a **daemon subprocess** that persists beyond CLI command lifetime. `Manager.EnsureRunning()` spawns `clawker host-proxy serve` as a detached process if not already running. The daemon polls Docker every 30s and auto-exits when no clawker containers are running (after 60s grace period) or after 10 consecutive Docker errors.

**Hidden CLI:** `clawker host-proxy serve|status|stop`

## Components

| Component | File | Purpose |
|-----------|------|---------|
| `Server` | `server.go` | HTTP server handling proxy requests |
| `Manager` | `manager.go` | Spawns/manages daemon subprocess |
| `Daemon` | `daemon.go` | Background process with container watcher |
| `SessionStore` | `session.go` | Generic session management with TTL |
| `CallbackChannel` | `callback.go` | OAuth callback registration and capture |
| `MockHostProxy` | `hostproxytest/` | Test mock implementing all endpoints |

## Constants

```go
const DefaultPort = 18374
const SessionIDLength = 16
const DefaultCallbackTTL = 5 * time.Minute
const gpgReadTimeout = 30 * time.Second  // Timeout for GPG agent response reads
var ErrCallbackAlreadyReceived error
```

## Types

```go
type CallbackData struct {
    Method, Path, Query string
    Headers map[string]string
    Body string
    ReceivedAt time.Time
}

type Session struct {
    ID, Type string
    CreatedAt, ExpiresAt time.Time
    Metadata map[string]any
}
// Methods: GetMetadata, SetMetadata, CaptureOnce, IsExpired

type DaemonOptions struct {
    Port, MaxConsecutiveErrs int
    PIDFile string
    PollInterval, GracePeriod time.Duration
    DockerClient ContainerLister  // Inject mock for testing
}

type ContainerLister interface {
    ContainerList(ctx, options) (ContainerListResult, error)
    io.Closer
}
```

## Constructors

```go
func NewManager() *Manager
func NewManagerWithPort(port int) *Manager
func NewManagerWithOptions(port int, pidFile string) *Manager
func NewDaemon(opts DaemonOptions) (*Daemon, error)
func DefaultDaemonOptions() DaemonOptions
func NewServer(port int) *Server
func NewSessionStore() *SessionStore  // Starts cleanup goroutine; must call Stop()
func NewCallbackChannel(store *SessionStore) *CallbackChannel
```

## Manager Methods

```go
(*Manager).ProxyURL() string      // http://host.docker.internal:<port>
(*Manager).IsRunning() bool       // PID file + health check
(*Manager).Port() int
(*Manager).EnsureRunning() error  // Spawns daemon if needed
(*Manager).Stop()                 // No-op (daemon self-terminates)
(*Manager).StopDaemon() error     // Explicit stop
```

## Daemon Methods & Utilities

```go
(*Daemon).Run(ctx) error           // Blocks until signal or auto-exit
func IsDaemonRunning(pidFile) bool
func GetDaemonPID(pidFile) int
func StopDaemon(pidFile) error
```

## SessionStore Methods

```go
(*SessionStore).Create(sessionType, ttl, metadata) (*Session, error)
(*SessionStore).Get(id) *Session
(*SessionStore).Delete(id)
(*SessionStore).Count() int
(*SessionStore).Stop()
(*SessionStore).SetOnDelete(fn func(*Session))
```

## CallbackChannel Methods

```go
(*CallbackChannel).Register(port, path, ttl) (*Session, error)
(*CallbackChannel).Capture(sessionID, *http.Request) error
(*CallbackChannel).GetData(sessionID) (*CallbackData, bool)
(*CallbackChannel).GetPort(sessionID) (int, bool)
(*CallbackChannel).GetPath(sessionID) (string, bool)
(*CallbackChannel).Delete(sessionID)
(*CallbackChannel).IsReceived(sessionID) bool
```

## Server Methods

```go
(*Server).Start() error  // Listens on IPv4+IPv6 loopback
(*Server).Stop(ctx) error
(*Server).IsRunning() bool
(*Server).Port() int
```

## API Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/health` | GET | Health check |
| `/open/url` | POST | Open URL in host browser |
| `/git/credential` | POST | Git credential get/store/erase |
| `/ssh/agent` | POST | SSH agent forwarding (macOS) |
| `/gpg/agent` | POST | GPG agent forwarding (macOS) |
| `/callback/register` | POST | Register OAuth callback session |
| `/callback/{session}/data` | GET | Poll for captured callback |
| `/callback/{session}` | DELETE | Cleanup session |
| `/cb/{session}/{path...}` | GET | Receive OAuth callbacks |

## OAuth Callback Flow

Container registers session via `/callback/register`. Server starts dynamic listener on requested port. Browser redirects to `localhost:PORT/path`, listener captures request. Container polls `/callback/{session}/data` to retrieve data.

## Git/SSH/GPG Credential Forwarding

- **HTTPS**: `git-credential-clawker` → POST `/git/credential` → host `git credential fill` → OS Keychain
- **SSH Linux**: Bind mount `$SSH_AUTH_SOCK` to `/tmp/ssh-agent.sock`
- **SSH macOS**: `ssh-agent-proxy` binary → POST `/ssh/agent` → host SSH agent
- **GPG Linux**: Bind mount GPG extra socket to `~/.gnupg/S.gpg-agent`
- **GPG macOS**: `gpg-agent-proxy` binary → POST `/gpg/agent` → host GPG extra socket
- **Git Config**: `~/.gitconfig` mounted read-only, entrypoint copies filtering `credential.helper`

## Test Mock (`hostproxytest/`)

```go
mock := hostproxytest.NewMockHostProxy(t)
mock.URL() string
mock.GetOpenedURLs() []string
mock.GetGitCreds() []GitCredRequest
mock.SetCallbackReady(sessionID, path, query)
mock.SetHealthOK(ok bool)
```

## Container Scripts

| Script | Purpose |
|--------|---------|
| `host-open` | Opens URLs, detects OAuth, rewrites callbacks |
| `callback-forwarder` | Polls proxy, forwards callbacks to local server |
| `git-credential-clawker` | Git credential helper |
| `ssh-agent-proxy` | SSH agent proxy binary (macOS) |
| `gpg-agent-proxy` | GPG agent proxy binary (macOS) |
