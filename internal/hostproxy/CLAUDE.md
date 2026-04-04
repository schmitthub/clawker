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
const SessionIDLength = 16
const DefaultCallbackTTL = 5 * time.Minute
var ErrCallbackAlreadyReceived error
```

Port comes from config (`host_proxy.daemon.port`, default 18374) — no more `DefaultPort` constant.

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

type ContainerLister interface {
    ContainerList(ctx, options) (ContainerListResult, error)
    io.Closer
}

// Functional options for Daemon (CLI flag overrides without mutating config)
type DaemonOption func(*Daemon)
func WithDaemonPort(port int) DaemonOption
func WithPollInterval(d time.Duration) DaemonOption
func WithGracePeriod(d time.Duration) DaemonOption
```

## Interface

```go
// HostProxyService is the interface for host proxy operations used by container commands.
// Concrete implementation: Manager. Mock: hostproxytest.MockManager.
type HostProxyService interface {
    EnsureRunning() error
    IsRunning() bool
    ProxyURL() string
}
```

## Constructors

```go
func NewManager(cfg config.Config) (*Manager, error)       // validates port; returns error for invalid config
func NewDaemon(cfg config.Config, opts ...DaemonOption) (*Daemon, error) // reads all settings from cfg.HostProxyConfig()
func NewServer(port int, log *logger.Logger, rulesFilePath string) *Server // rulesFilePath empty = no egress enforcement
func NewSessionStore() *SessionStore  // Starts cleanup goroutine; must call Stop()
func NewCallbackChannel(store *SessionStore) *CallbackChannel
```

**Config pattern**: `Manager` and `Daemon` store `cfg config.Config` on the struct. All settings read from `cfg.HostProxyConfig()` (port, poll interval, grace period, max consecutive errors). PID file from `cfg.HostProxyPIDFilePath()`, log file from `cfg.HostProxyLogFilePath()`, labels from `cfg.LabelManaged()`, etc. CLI flags override via functional options (`WithDaemonPort`, `WithPollInterval`, `WithGracePeriod`) — config object is never mutated.

**Validation**: Both `NewManager` and `NewDaemon` validate port at construction via shared `validatePort()` helper. `NewDaemon` also validates poll interval (>0), grace period (>=0), and max consecutive errors (>0).

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
| `/open/url` | POST | Open URL in host browser (egress-checked) |
| `/git/credential` | POST | Git credential get/store/erase (injection-sanitized) |
| `/callback/register` | POST | Register OAuth callback session |
| `/callback/{session}/data` | GET | Poll for captured callback |
| `/callback/{session}` | DELETE | Cleanup session |
| `/cb/{session}/{path...}` | GET | Receive OAuth callbacks |

## Egress Enforcement (`egress_check.go`)

The `/open/url` endpoint enforces egress rules before opening URLs in the host browser. This closes a proven exfil vector where a container agent could encode stolen secrets in URL query params and use the host browser as an out-of-band channel (bypassing the Envoy+CoreDNS firewall entirely).

### How it works

1. `handleOpenURL` calls `CheckURLAgainstEgressRules(targetURL, rulesFilePath)` before `openBrowser()`
2. The function reads `egress-rules.yaml` just-in-time on every request (no caching — rules change at runtime)
3. File read is protected by shared flock (`gofrs/flock`) to avoid torn reads from concurrent writes
4. URL is parsed and matched against rules: scheme→proto, host→dst (exact + wildcard), port, path (longest prefix)
5. If no matching allow rule → 403 "blocked by egress policy"
6. If `rulesFilePath` is empty (firewall not enabled) → skip check, allow all (backwards compatible)

### Key design decisions

- **Leaf package**: Does NOT import `internal/firewall` or `internal/storage`. Reads YAML directly with `os.ReadFile` + `yaml.Unmarshal`. Mirror types for `EgressRulesFile`/`EgressRule`/`PathRule` are intentional copies.
- **Fail-closed**: Missing/unreadable rules file → block all URLs. Rule action validation uses `!strings.EqualFold(action, "allow")` to reject typos.
- **No caching**: Rules are a moving target (CLI adds/removes, project configs merge during startup). Each request reads fresh.
- **Userinfo rejection**: URLs with `user:pass@host` are rejected — no legitimate browser URL uses this and it enables smuggling.

### Git Credential Injection Protection

`handleGitCredential` rejects requests where any credential field (Protocol, Host, Path, Username, Password) contains `\n`, `\r`, or `\0` (400 response). `formatGitCredentialInput` also sanitizes as defense-in-depth.

## OAuth Callback Flow

Container registers session via `/callback/register`. Server starts dynamic listener on requested port. Browser redirects to `localhost:PORT/path`, listener captures request. Container polls `/callback/{session}/data` to retrieve data.

## Git Credential Forwarding

- **HTTPS**: `git-credential-clawker` → POST `/git/credential` → host `git credential fill` → OS Keychain
- **Git Config**: `~/.gitconfig` mounted read-only, entrypoint copies filtering `credential.helper`
- **SSH/GPG**: Handled by `internal/socketbridge` package (muxrpc over `docker exec`, not via host proxy)

## Test Doubles (`hostproxytest/`)

### MockManager (HostProxyService interface)

For unit tests — no subprocess spawning, no network I/O:

```go
mock := hostproxytest.NewMockManager()          // Starts not running; EnsureRunning transitions to running
mock := hostproxytest.NewRunningMockManager(url) // Running with given URL
mock := hostproxytest.NewFailingMockManager(err) // EnsureRunning returns error
```

### MockHostProxy (HTTP test server)

For integration tests — real HTTP server with endpoint handlers:

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
| `clawker-socket-server` | Unix socket server for SSH/GPG agent forwarding (muxrpc protocol) |
