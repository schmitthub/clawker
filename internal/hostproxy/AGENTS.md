# Host Proxy Package

Runs host actions for agents: open a URL in the host browser, run the host git credential helper, and capture OAuth callbacks.

**Agents never connect to it, and it has no listener for actions.** The daemon dials out to the CP admin listener (`127.0.0.1:<control_plane.admin_port>`, host-published by the CP container; works on macOS, rootful Linux, and rootless Linux) and holds one `HostProxyService.Connect` stream (`api/hostproxy/v1`). The CP sends each authorized action down the stream; `Connector` runs it on `Actions` and sends the result back. Identity: the host proxy's own CLI-CA client leaf (CN `consts.HostProxyClientName`, `auth.LoadHostProxyClientCert`) and its own Hydra client (`consts.ClientIDHostProxy`, signing key `auth.LoadHostProxySigningKey`, scope `hostproxy:serve`), on the connection from `controlplane/adminclient.DialHostProxy`, which the `serve` command wraps with `hostproxyv1.NewHostProxyServiceClient`. The path is agent → clawkerd host relay → CP AgentService (all authorization: label grants and callback-session ownership) → `controlplane/hostgateway` → this stream. This package keeps the egress check on URLs, the redirect check, the callback port bind, and the browser open. The only host ports it binds are the short-lived OAuth callback listeners on loopback.

## Architecture: Daemon Subprocess

The host proxy runs as a **daemon subprocess** that persists beyond CLI command lifetime. `Manager.EnsureRunning()` spawns `clawker host-proxy serve` as a detached process if not already running, then waits (`daemonStartTimeout`, 3s) until the PID file names a live process. The daemon holds an exclusive `flock` on the PID file for its whole life (`lockPIDFile`): a second daemon gets `errDaemonAlreadyRunning` and exits, so a check-then-start race between two CLI commands cannot leave two daemons. The kernel drops the lock when the process dies. The daemon polls Docker every 30s and auto-exits when no clawker containers are running (after 60s grace period) or after 10 consecutive Docker errors.

`Connector.Run` reconnects with backoff (0.5s doubling to 30s; back to 0.5s after a stream that stayed up a minute) when the stream ends: CP restart, network fault, or a newer stream replaced it (the CP keeps one stream; the newest wins). The CP accepts a stream by sending the `hostproxyv1.AcceptedHeader` response header; a refused stream (wrong CN, scope, or client ID) ends trailers-only. Keepalive pings (`consts.HostProxyKeepaliveTime`) find a dead connection while the stream idles; the admin server's enforcement policy allows them (`consts.AdminKeepaliveMinClientPing`). With no host proxy connected, the CP answers host actions `Unavailable`.

**Hidden CLI:** `clawker host-proxy serve|status|stop`

## Components

| Component | File | Purpose |
|-----------|------|---------|
| `Actions` | `actions.go` | Runs one host action (`Handle`) and owns the OAuth callback listeners |
| `Connector` | `connector.go` | Holds the `HostProxyService.Connect` stream to the CP; reconnects with backoff |
| `Manager` | `manager.go` | Spawns/manages daemon subprocess |
| `Daemon` | `daemon.go` | Background process: PID lock, connector, container watcher, egress-rules readiness gate |
| `SessionStore` | `session.go` | Generic session management with TTL |
| `CallbackChannel` | `callback.go` | OAuth callback registration and capture |
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

There is no host proxy port setting: the daemon dials the CP admin port (`control_plane.admin_port`). The legacy `host_proxy.manager.port` / `host_proxy.daemon.port` keys are removed by a silent settings migration.

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
func NewManager(cfg config.Config, log *logger.Logger) (*Manager, error)       // errors on a nil config
func NewDaemon(cfg config.Config, log *logger.Logger, cpClient hostproxyv1.HostProxyServiceClient, opts ...DaemonOption) (*Daemon, error) // reads all settings from cfg.HostProxyConfig()
func NewActions(log *logger.Logger, rulesFilePath string) *Actions  // rulesFilePath empty = no egress enforcement; Close releases listeners + session store
func NewConnector(client hostproxyv1.HostProxyServiceClient, actions *Actions, log *logger.Logger) *Connector
func NewSessionStore() *SessionStore  // Starts cleanup goroutine; must call Stop()
func NewCallbackChannel(store *SessionStore, log *logger.Logger) *CallbackChannel
```

**Config pattern**: `Manager` and `Daemon` store `cfg config.Config` on the struct. All settings read from `cfg.HostProxyConfig().Daemon` (poll interval, grace period, max consecutive errors). PID file from `cfg.HostProxyPIDFilePath()`, log file from `cfg.HostProxyLogFilePath()`, labels from `cfg.LabelManaged()`, etc. CLI flags override via functional options (`WithPollInterval`, `WithGracePeriod`) — config object is never mutated.

**Validation**: `NewDaemon` validates poll interval (>0), grace period (>=0), and max consecutive errors (>0).

## Core methods

- **`Manager`** — `IsRunning() bool` (the PID file names a live process; there is no port to probe), `EnsureRunning() error` (spawns daemon if needed), `Stop()` (no-op — daemon self-terminates), `StopDaemon() error` (explicit stop).
- **`Daemon`** — `Run(ctx) error` takes the PID lock, starts the connector, blocks until signal or auto-exit. Package helpers: `IsDaemonRunning(pidFile)`, `GetDaemonPID(pidFile)`, `StopDaemon(pidFile)`.
- **`Actions`** — `Handle(ctx, *HostActionRequest) *HostActionResult` (echoes the request id), `Close(ctx)`.
- **`Connector`** — `Run(ctx)` until ctx ends.
- **`SessionStore`** — `Create(sessionType, ttl, metadata) (*Session, error)`, `Get(id)`, `Delete(id)`, `Count() int`, `Stop()`, `SetOnDelete(fn)`.
- **`CallbackChannel`** — `Register(port, path, ttl) (*Session, error)`, `Capture(sessionID, *http.Request) error`, `GetData(sessionID) (*CallbackData, bool)`, `GetPort`/`GetPath`/`Delete`/`IsReceived(sessionID)`.

## Host actions

Each action is one `HostActionRequest` oneof (the AgentService request messages). A refused or failed action is a `HostActionError` with a gRPC code; the CP maps it (`controlplane/hostgateway.resultError`).

| Action | Result / codes |
|--------|----------------|
| `open_url` | `OpenURLResult`; `InvalidArgument` (empty, unparseable, not http/https), `PermissionDenied` (egress deny), `Internal` (broken rules file, browser failure) |
| `git_credential` | `GitCredentialResult` (credentials for get); `InvalidArgument` (bad action, missing field, injection characters), `FailedPrecondition` (the host `git credential` command failed) |
| `register_callback` | `RegisterCallbackResult` (session, port, expiry); `InvalidArgument` (redirect check), `PermissionDenied` (egress deny), `Internal` (session, listener, or browser failure) |
| `get_callback_data` | `GetCallbackDataResult`; `NotFound` (unknown or expired session) |
| `delete_callback` | `DeleteCallbackResult` (idempotent) |

## Egress Enforcement (`egress_check.go`)

The `open_url` action enforces egress rules before opening URLs in the host browser. This closes a proven exfil vector: a container agent could otherwise encode stolen secrets in URL query params and use the host browser as an out-of-band channel, bypassing the Envoy+CoreDNS firewall entirely.

`Actions.openURL` calls `CheckURLAgainstEgressRules(targetURL, rulesFilePath)` before `openBrowser()`. The function reads `egress-rules.yaml` just-in-time on every request (rules change at runtime — no caching). The firewall daemon writes the rules file atomically (temp+fsync+rename), so no locking is needed for concurrent reads. The URL is parsed and matched against rules: scheme→proto, host→dst (exact + wildcard), port, path (longest prefix). Empty `rulesFilePath` (firewall disabled) skips the check for backwards compat.

**Design constraints:**
- **Leaf package**: does NOT import `internal/controlplane/firewall` or `internal/storage`. Reads YAML directly with `os.ReadFile` + `yaml.Unmarshal`. Mirror types for `egressRulesFile`/`egressRule`/`pathRule` are unexported intentional copies.
- **Fail-closed, two tiers**: a *policy* deny (URL not in the allow list) → `log.Warn` + `PermissionDenied` `blocked by egress policy`. A *broken rules file* (missing, unreadable, unparseable, or an uncompilable `~` regex) is `errEgressRulesInvalid` — an infrastructure fault, not a policy decision → `log.Error` + `Internal` `egress rules unavailable`, never a silent skip. Action validation uses `!strings.EqualFold(action, "allow")` so typos fail closed.
- **Regex lockstep**: `compilePathRegex` (shared by `pathRuleMatches` and load-time validation) compiles a `~`-path to the same full-string-anchored RE2 form Envoy's `safe_regex` uses; a pattern that fails to compile makes `readEgressRules` reject the whole file, mirroring Envoy refusing a config with a bad `safe_regex`.
- **Userinfo rejection**: URLs with `user:pass@host` are rejected — no legitimate browser URL uses this and it enables smuggling.

### Startup readiness gate

The control plane and firewall stack boot **before** the host proxy in container bootstrap, so the host proxy's readiness gate (below) can assume the firewall is already coming up instead of racing it — starting the host proxy first would deadlock cold start, since its gate waits on the firewall. `Daemon.Run` starts the connector **first**, then runs `ensureEgressRulesReady` in a background goroutine. Serving first is safe because `open_url` is fail-closed per request regardless (see two-tier fail-closed above).

`ensureEgressRulesReady` is a **three-stage sequential gate** — each stage is its own bounded loop and only starts once the prior one passes, so a later stage never races ahead of its prerequisite:

1. **Firewall container running** — Docker poll on the `PurposeFirewall` label. Budget: `consts.HostProxyFirewallRunningTimeout` (= `FirewallStackBringupTimeout`).
2. **Envoy healthy** — GET the host-published health listener (`EnvoyHealthHostPort`, reached by port — no firewall-package import). Budget: `consts.HostProxyEnvoyHealthTimeout` (= `FirewallStackHealthTimeout`).
3. **Rules file readable + valid** — re-read `egress-rules.yaml` each tick so an in-flight atomic write settles; an unreadable/unparseable/bad-regex file is `errEgressRulesInvalid`. Budget: `consts.HostProxyRulesReadTimeout` (10s, ~"10 tries every second").

All stages poll at `consts.HostProxyReadyPollInterval` (1s); budgets derive from the firewall's own bringup/health timeouts so the host proxy never gives up before the firewall could plausibly be up. On exhaustion the gate goroutine signals `Run`, which logs `Error` and shuts the daemon down — until then, request-time `open_url` enforcement already fails closed. `firewallRunningProbe`/`envoyHealthProbe` are injectable fields for tests; `readyTimeout` overrides every stage's budget uniformly (tests only). `NewDaemon` likewise returns an error when the firewall is enabled but the egress rules path cannot be resolved — never a silently unchecked `open_url`.

**Git credential injection protection**: `Actions.gitCredential` rejects requests where any field (`Protocol`/`Host`/`Path`/`Username`/`Password`) contains `\n`, `\r`, or `\0` (`InvalidArgument`). `formatGitCredentialInput` sanitizes as defense-in-depth.

## OAuth Callback Flow

One action: `register_callback {auth_url, ttl_seconds}`. The host proxy egress-checks `auth_url`, takes the port and path from its `redirect_uri` (or `callback`) parameter, which must be `http://localhost|127.0.0.1:<port>` with port ≥ 1024 and no userinfo, caps the TTL at `DefaultCallbackTTL`, binds a listener on that port, opens the browser, and returns the session id, port and expiry. The listener closes after the first capture. The container polls `/callback/{session}/data` on the clawkerd relay (through the CP, which checks session ownership, as `get_callback_data`) and `callback-forwarder` replays the request to the local server.

## Git Credential Forwarding

- **HTTPS**: `git-credential-clawker` → clawkerd relay → CP `GitCredential` (needs the `dev.clawker.git_https` grant) → `git_credential` on the stream → host `git credential fill|approve|reject` → OS Keychain
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
