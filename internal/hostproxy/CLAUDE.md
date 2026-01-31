# Host Proxy Package

HTTP service mesh mediating interactions between containers and the host machine.

## Components

| Component | File | Purpose |
|-----------|------|---------|
| `Server` | `server.go` | HTTP server handling proxy requests |
| `SessionStore` | `session.go` | Generic session management with TTL and cleanup |
| `CallbackChannel` | `callback.go` | OAuth callback registration, capture, and retrieval |
| `Manager` | `manager.go` | Lifecycle management of the proxy server |
| — | `git_credential.go` | Git credential forwarding handler (route on Server) |
| — | `ssh_agent.go` | SSH agent forwarding handler (route on Server) |

## Constants & Errors

```go
const DefaultPort          = 18374
const SessionIDLength      = 16
const CallbackSessionType  = "callback"
const DefaultCallbackTTL   = 5 * time.Minute

var ErrCallbackAlreadyReceived  // Returned when capturing a callback for a session that already has one
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

type CallbackChannel struct { store *SessionStore }
```

## Constructors

```go
func NewServer(port int) *Server
func NewManagerWithPort(port int) *Manager
```

## Manager Methods

```go
(*Manager).ProxyURL() string            // Returns http://host.docker.internal:<port>
(*Manager).IsRunning() bool
(*Manager).Port() int
(*Manager).EnsureRunning() error        // Lazy start with sync.Once
(*Manager).Stop(ctx context.Context) error
```

## API Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/open/url` | POST | Open URL in host browser |
| `/health` | GET | Health check |
| `/git/credential` | POST | Forward git credential get/store/erase to host |
| `/ssh/agent` | POST | Forward SSH agent requests to host (macOS) |
| `/callback/register` | POST | Register OAuth callback session |
| `/callback/{session}/data` | GET | Poll for captured callback data |
| `/callback/{session}` | DELETE | Cleanup session |
| `/cb/{session}/{path...}` | GET | Receive OAuth callbacks from browser |

## OAuth Callback Flow

```
CONTAINER                              HOST PROXY (:18374)                    BROWSER
    │                                         │                                  │
    │ 1. Claude Code starts auth server       │                                  │
    │                                         │                                  │
    │ 2. host-open detects OAuth URL ────────►│                                  │
    │    POST /callback/register              │                                  │
    │         │                               │                                  │
    │         │◄────────────────────────────── │ Returns session_id              │
    │         │                               │                                  │
    │    Rewrites callback URL ───────────────┼─────────────────────────────────►│
    │                                         │              3. Opens in browser │
    │                                         │                                  │
    │                                         │◄─────────────────────────────────│
    │                                         │ 4. Redirect to proxy callback    │
    │                                         │    GET /cb/SESSION/callback      │
    │                                         │                                  │
    │    callback-forwarder polls ───────────►│                                  │
    │    GET /callback/SESSION/data           │                                  │
    │         │                               │                                  │
    │         │◄────────────────────────────── │ Returns callback data           │
    │         │                               │                                  │
    │ 5. Forwards to localhost:PORT           │                                  │
    │    Claude Code receives callback!       │                                  │
```

## Git Credential Forwarding

**HTTPS** (via host proxy):
```
Container → git-credential-clawker get → POST /git/credential → git credential fill → OS Keychain
```

**SSH** (agent forwarding):
- Linux: Bind mount `$SSH_AUTH_SOCK` to `/tmp/ssh-agent.sock`
- macOS: SSH agent proxy Go binary → POST /ssh/agent → `net.Dial(SSH_AUTH_SOCK)`

**Host Git Config**: `~/.gitconfig` mounted read-only to `/tmp/host-gitconfig`, entrypoint copies filtering `credential.helper`.

## Container Scripts

| Script | Purpose |
|--------|---------|
| `host-open` | Opens URLs, detects OAuth flows, rewrites callbacks |
| `callback-forwarder` | Polls proxy and forwards callbacks to local server |
| `git-credential-clawker` | Git credential helper forwarding to host proxy |
| `ssh-agent-proxy` | SSH agent proxy binary forwarding via host proxy (macOS) |
