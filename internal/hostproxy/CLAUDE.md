# Host Proxy Package

HTTP service mesh mediating interactions between containers and the host machine.

## Components

| Component | File | Purpose |
|-----------|------|---------|
| `Server` | `server.go` | HTTP server handling proxy requests |
| `SessionStore` | `session.go` | Generic session management with TTL and cleanup |
| `CallbackChannel` | `callback.go` | OAuth callback registration, capture, and retrieval |
| `Manager` | `manager.go` | Lifecycle management of the proxy server |
| -- | `git_credential.go` | Git credential forwarding handler (route on Server) |
| -- | `ssh_agent.go` | SSH agent forwarding handler (route on Server) |
| `MockHostProxy` | `hostproxytest/hostproxy_mock.go` | Test mock implementing all proxy endpoints |

## Constants & Errors

```go
const DefaultPort         = 18374
const SessionIDLength     = 16
const CallbackSessionType = "callback"
const DefaultCallbackTTL  = 5 * time.Minute

var ErrCallbackAlreadyReceived error // Callback already captured for session
```

## Types

```go
type CallbackData struct {
    Method     string            `json:"method"`
    Path       string            `json:"path"`
    Query      string            `json:"query"`
    Headers    map[string]string `json:"headers,omitempty"`
    Body       string            `json:"body,omitempty"`
    ReceivedAt time.Time         `json:"received_at"`
}

type Session struct {
    ID, Type  string
    CreatedAt, ExpiresAt time.Time
    Metadata  map[string]any
}
```

### Session Methods

```go
(*Session).GetMetadata(key string) (any, bool)
(*Session).SetMetadata(key string, value any)
(*Session).CaptureOnce(receivedKey string) bool  // Atomic first-capture check
(*Session).IsExpired() bool
```

## Constructors

```go
func NewManager() *Manager                           // Uses DefaultPort
func NewManagerWithPort(port int) *Manager           // Custom port (for testing)
func NewServer(port int) *Server
func NewSessionStore() *SessionStore                 // Starts cleanup goroutine; must call Stop()
func NewCallbackChannel(store *SessionStore) *CallbackChannel
```

## Manager Methods

```go
(*Manager).ProxyURL() string             // http://host.docker.internal:<port>
(*Manager).IsRunning() bool
(*Manager).Port() int
(*Manager).EnsureRunning() error         // Lazy start with health check
(*Manager).Stop(ctx context.Context) error
```

## SessionStore Methods

```go
(*SessionStore).Create(sessionType string, ttl time.Duration, metadata map[string]any) (*Session, error)
(*SessionStore).Get(id string) *Session
(*SessionStore).Delete(id string)
(*SessionStore).Count() int
(*SessionStore).Stop()                              // Stop cleanup goroutine
(*SessionStore).SetOnDelete(fn func(*Session))      // Hook for session deletion
```

## CallbackChannel Methods

```go
(*CallbackChannel).Register(port int, path string, ttl time.Duration) (*Session, error)
(*CallbackChannel).Capture(sessionID string, r *http.Request) error
(*CallbackChannel).GetData(sessionID string) (*CallbackData, bool)
(*CallbackChannel).GetPort(sessionID string) (int, bool)
(*CallbackChannel).GetPath(sessionID string) (string, bool)
(*CallbackChannel).Delete(sessionID string)
(*CallbackChannel).IsReceived(sessionID string) bool
```

## Server Methods

```go
(*Server).Start() error                  // Listens on IPv4+IPv6 loopback
(*Server).Stop(ctx context.Context) error
(*Server).IsRunning() bool
(*Server).Port() int
```

## API Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/open/url` | POST | Open URL in host browser |
| `/health` | GET | Health check (returns `clawker-host-proxy` service ID) |
| `/git/credential` | POST | Forward git credential get/store/erase to host |
| `/ssh/agent` | POST | Forward SSH agent requests to host (macOS) |
| `/callback/register` | POST | Register OAuth callback session + start dynamic listener |
| `/callback/{session}/data` | GET | Poll for captured callback data |
| `/callback/{session}` | DELETE | Cleanup session |
| `/cb/{session}/{path...}` | GET | Receive OAuth callbacks from browser |

## OAuth Callback Flow

Container registers session via POST `/callback/register` with port/path. Server starts a dynamic listener on that port. Browser redirects to `localhost:PORT/path`, dynamic listener captures the request. Container polls GET `/callback/{session}/data` to retrieve the captured callback, then forwards it to the local auth server.

## Git Credential Forwarding

**HTTPS** (via host proxy):
```
Container -> git-credential-clawker get -> POST /git/credential -> git credential fill -> OS Keychain
```

**SSH** (agent forwarding):
- Linux: Bind mount `$SSH_AUTH_SOCK` to `/tmp/ssh-agent.sock`
- macOS: SSH agent proxy Go binary -> POST /ssh/agent -> `net.Dial(SSH_AUTH_SOCK)`

**Host Git Config**: `~/.gitconfig` mounted read-only to `/tmp/host-gitconfig`, entrypoint copies filtering `credential.helper`.

## Test Mock (`hostproxytest/`)

`MockHostProxy` is a self-contained HTTP server implementing all proxy endpoints for integration tests.

```go
import "github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"

mock := hostproxytest.NewMockHostProxy(t)
mock.URL() string                                  // Base URL
mock.GetOpenedURLs() []string                      // Captured /open/url requests
mock.GetGitCreds() []GitCredRequest                // Captured git credential requests
mock.SetCallbackReady(sessionID, path, query string)
mock.SetHealthOK(ok bool)
```

**Types:**
- `CallbackData` -- Session with `SessionID`, `OriginalPort`, `CallbackPath`, `CapturedPath`, `CapturedQuery`, `Ready`
- `GitCredRequest` -- Captured git credential request with `Action`, `Host`, `Protocol`, `Username`

## Container Scripts

| Script | Purpose |
|--------|---------|
| `host-open` | Opens URLs, detects OAuth flows, rewrites callbacks |
| `callback-forwarder` | Polls proxy and forwards callbacks to local server |
| `git-credential-clawker` | Git credential helper forwarding to host proxy |
| `ssh-agent-proxy` | SSH agent proxy binary forwarding via host proxy (macOS) |
