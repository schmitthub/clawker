# Host Proxy Package

HTTPS service on the host that runs host actions for agents: open a URL in the host browser, run the host git credential helper, and capture OAuth callbacks.

**Agents never connect to it.** It serves mTLS only (server leaf `clawker-host-proxy` from `consts.AuthHostProxyDir`, signed by the CLI CA; client certs must chain to the CLI CA). Every action route is wrapped by `requireControlPlane` (`tls.go`), which admits only a client cert with CN `consts.ContainerCP` and logs `event=host_proxy_caller_rejected` otherwise; `/health` accepts any CLI-CA client (the CLI uses its client cert). The path is agent → clawkerd host relay → CP AgentService (all authorization: label grants and callback-session ownership) → `controlplane/hostgateway` → this server. This package keeps the egress check on URLs, the redirect check, the port bind, and the browser open.

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
| `TLS` | `tls.go` | `ServerTLSConfig`, `ClientTLSConfig`, `requireControlPlane` |
| `MockManager` | `hostproxytest/` | `Service` test double |

## Constraints

- `Manager` lazy-inits with `sync.Once`; call `EnsureRunning()` before any container command.
- Daemon scope is strictly the host proxy lifecycle. The firewall stack (Envoy + CoreDNS) is owned by the CP daemon (`controlplane/firewall`, run by `cmd/clawkercp`).

## Constants

```go
const SessionIDLength = 16
const DefaultCallbackTTL = 5 * time.Minute
var ErrCallbackAlreadyReceived error
```

Port comes from config: `host_proxy.port` (default 18374). The daemon binds it, `Manager` spawns the daemon with it, and the CP dials it. The `serve --port` flag overrides it for one run.

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

// ContainerLister is the minimal Docker subset the daemon's container watcher
// needs; satisfied by *docker.Client. Keeps hostproxy a leaf package.
type ContainerLister interface {
    ContainerList(ctx, options) (ContainerListResult, error)
    io.Closer
}

// Functional Daemon options — CLI flag overrides without mutating config.
type DaemonOption func(*Daemon)
func WithDaemonPort(port int) DaemonOption
func WithPollInterval(d time.Duration) DaemonOption
func WithGracePeriod(d time.Duration) DaemonOption
```

## Interface

```go
// Service is the interface for host proxy operations used by container commands.
// Concrete implementation: Manager. Mock: hostproxytest.MockManager.
type Service interface {
    EnsureRunning() error
    IsRunning() bool
    // Mirrors handleGitCredential's host prerequisites: `git` on PATH +
    // a configured credential helper. The command layer warns before
    // container start when the configured HTTPS lane cannot be served.
    PrecheckGitCredential(ctx context.Context) error
}
```

## Constructors

```go
func NewManager(cfg config.Config, log *logger.Logger) (*Manager, error)       // validates port; returns error for invalid config
func NewDaemon(cfg config.Config, log *logger.Logger, opts ...DaemonOption) (*Daemon, error) // reads all settings from cfg.HostProxyConfig()
func NewServer(port int, log *logger.Logger, rulesFilePath string, tlsConfig *tls.Config) *Server // rulesFilePath empty = no egress enforcement; Start errors on a nil tlsConfig
func NewSessionStore() *SessionStore  // Starts cleanup goroutine; must call Stop()
func NewCallbackChannel(store *SessionStore, log *logger.Logger) *CallbackChannel
```

**Config pattern**: `Manager` and `Daemon` store `cfg config.Config` on the struct. All settings read from `cfg.HostProxyConfig()` (port, poll interval, grace period, max consecutive errors). PID file from `cfg.HostProxyPIDFilePath()`, log file from `cfg.HostProxyLogFilePath()`, labels from `cfg.LabelManaged()`, etc. CLI flags override via functional options (`WithDaemonPort`, `WithPollInterval`, `WithGracePeriod`) — config object is never mutated.

**Validation**: Both `NewManager` and `NewDaemon` validate port at construction via shared `validatePort()` helper. `NewDaemon` also validates poll interval (>0), grace period (>=0), and max consecutive errors (>0).

## Core methods

- **`Manager`** — `IsRunning() bool` (PID file + mTLS health check with the CLI client cert), `Port() int`, `EnsureRunning() error` (spawns daemon if needed), `Stop()` (no-op — daemon self-terminates), `StopDaemon() error` (explicit stop).
- **`Daemon`** — `Run(ctx) error` blocks until signal or auto-exit. Package helpers: `IsDaemonRunning(pidFile)`, `GetDaemonPID(pidFile)`, `StopDaemon(pidFile)`.
- **`SessionStore`** — `Create(sessionType, ttl, metadata) (*Session, error)`, `Get(id)`, `Delete(id)`, `Count() int`, `Stop()`, `SetOnDelete(fn)`.
- **`CallbackChannel`** — `Register(port, path, ttl) (*Session, error)`, `Capture(sessionID, *http.Request) error`, `GetData(sessionID) (*CallbackData, bool)`, `GetPort`/`GetPath`/`Delete`/`IsReceived(sessionID)`.
- **`Server`** — `Start()` (TLS listeners on IPv4+IPv6 loopback), `Stop(ctx)`, `IsRunning() bool`, `Port() int`.

## API Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/health` | GET | Health check (any CLI-CA client) |
| `/open/url` | POST | Open URL in host browser (egress-checked; CP only, as are the rows below) |
| `/git/credential` | POST | Git credential get/store/erase (injection-sanitized) |
| `/callback/register` | POST | Start an OAuth flow: check the auth URL, bind the redirect port, open the browser |
| `/callback/{session}/data` | GET | Poll for captured callback |
| `/callback/{session}` | DELETE | Cleanup session |

## Egress Enforcement (`egress_check.go`)

The `/open/url` endpoint enforces egress rules before opening URLs in the host browser. This closes a proven exfil vector: a container agent could otherwise encode stolen secrets in URL query params and use the host browser as an out-of-band channel, bypassing the Envoy+CoreDNS firewall entirely.

`handleOpenURL` calls `CheckURLAgainstEgressRules(targetURL, rulesFilePath)` before `openBrowser()`. The function reads `egress-rules.yaml` just-in-time on every request (rules change at runtime — no caching). The firewall daemon writes the rules file atomically (temp+fsync+rename), so no locking is needed for concurrent reads. The URL is parsed and matched against rules: scheme→proto, host→dst (exact + wildcard), port, path (longest prefix). Empty `rulesFilePath` (firewall disabled) skips the check for backwards compat.

**Design constraints:**
- **Leaf package**: does NOT import `internal/controlplane/firewall` or `internal/storage`. Reads YAML directly with `os.ReadFile` + `yaml.Unmarshal`. Mirror types for `egressRulesFile`/`egressRule`/`pathRule` are unexported intentional copies.
- **Fail-closed, two tiers**: a *policy* deny (URL not in the allow list) → `log.Warn` + 403 `blocked by egress policy`. A *broken rules file* (missing, unreadable, unparseable, or an uncompilable `~` regex) is `errEgressRulesInvalid` — an infrastructure fault, not a policy decision → `log.Error` + 500 `egress rules unavailable`, never a silent skip. Action validation uses `!strings.EqualFold(action, "allow")` so typos fail closed.
- **Regex lockstep**: `compilePathRegex` (shared by `pathRuleMatches` and load-time validation) compiles a `~`-path to the same full-string-anchored RE2 form Envoy's `safe_regex` uses; a pattern that fails to compile makes `readEgressRules` reject the whole file, mirroring Envoy refusing a config with a bad `safe_regex`.
- **Userinfo rejection**: URLs with `user:pass@host` are rejected — no legitimate browser URL uses this and it enables smuggling.

### Startup readiness gate

The control plane and firewall stack boot **before** the host proxy in container bootstrap, so the host proxy's readiness gate (below) can assume the firewall is already coming up instead of racing it — starting the host proxy first would deadlock cold start, since its gate waits on the firewall. `Daemon.Run` binds the server **first** (`/health` answers immediately), then runs `ensureEgressRulesReady` in a background goroutine. Binding first is safe because `/open/url` is fail-closed per request regardless (see two-tier fail-closed above).

`ensureEgressRulesReady` is a **three-stage sequential gate** — each stage is its own bounded loop and only starts once the prior one passes, so a later stage never races ahead of its prerequisite:

1. **Firewall container running** — Docker poll on the `PurposeFirewall` label. Budget: `consts.HostProxyFirewallRunningTimeout` (= `FirewallStackBringupTimeout`).
2. **Envoy healthy** — GET the host-published health listener (`EnvoyHealthHostPort`, reached by port — no firewall-package import). Budget: `consts.HostProxyEnvoyHealthTimeout` (= `FirewallStackHealthTimeout`).
3. **Rules file readable + valid** — re-read `egress-rules.yaml` each tick so an in-flight atomic write settles; an unreadable/unparseable/bad-regex file is `errEgressRulesInvalid`. Budget: `consts.HostProxyRulesReadTimeout` (10s, ~"10 tries every second").

All stages poll at `consts.HostProxyReadyPollInterval` (1s); budgets derive from the firewall's own bringup/health timeouts so the host proxy never gives up before the firewall could plausibly be up. On exhaustion the gate goroutine signals `Run`, which logs `Error` and shuts the daemon down — until then, request-time `/open/url` enforcement already fails closed. `firewallRunningProbe`/`envoyHealthProbe` are injectable fields for tests; `readyTimeout` overrides every stage's budget uniformly (tests only). `NewDaemon` likewise returns an error when the firewall is enabled but the egress rules path cannot be resolved — never a silently unchecked `/open/url`.

**Git credential injection protection**: `handleGitCredential` rejects requests where any field (`Protocol`/`Host`/`Path`/`Username`/`Password`) contains `\n`, `\r`, or `\0` (400). `formatGitCredentialInput` sanitizes as defense-in-depth.

## OAuth Callback Flow

One call: `POST /callback/register {authUrl, ttlSeconds}`. The server egress-checks `authUrl`, takes the port and path from its `redirect_uri` (or `callback`) parameter, which must be `http://localhost|127.0.0.1:<port>` with port ≥ 1024 and no userinfo, caps the TTL at `DefaultCallbackTTL`, binds a listener on that port, opens the browser, and returns `{sessionId, port, expiresAt}`. The listener closes after the first capture. The container polls `/callback/{session}/data` (through the CP, which checks session ownership) and `callback-forwarder` replays the request to the local server.

## Git Credential Forwarding

- **HTTPS**: `git-credential-clawker` → clawkerd relay → CP `GitCredential` (needs the `dev.clawker.git_https` grant) → POST `/git/credential` → host `git credential fill|approve|reject` → OS Keychain
- **Git Config**: `~/.gitconfig` mounted read-only, entrypoint copies filtering `credential.helper`
- **SSH/GPG**: Handled by `internal/socketbridge` package (muxrpc over `docker exec`, not via host proxy)

## Test Doubles (`hostproxytest/`)

### MockManager (Service interface)

For unit tests — no subprocess spawning, no network I/O:

```go
mock := hostproxytest.NewMockManager()          // Starts not running; EnsureRunning transitions to running
mock := hostproxytest.NewFailingMockManager(err) // EnsureRunning returns error
```

## Container Scripts

| Script | Purpose |
|--------|---------|
| `host-open` | Opens URLs; for a localhost `redirect_uri`, one `/callback/register` call, then spawns `callback-forwarder` |
| `callback-forwarder` | Polls proxy, forwards callbacks to local server |
| `git-credential-clawker` | Git credential helper |
| `clawker-socket-server` | Unix socket server for SSH/GPG agent forwarding (muxrpc protocol) |
