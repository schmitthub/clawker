# GPG Agent Forwarding Implementation Guide

## ✅ IMPLEMENTATION STATUS: FULLY INTEGRATED

**Test Status:** All SSH and GPG agent forwarding tests pass (unit + integration)

**Completed:** 2026-02-05

**Phase 2 (Lifecycle Integration):** SocketBridge.Manager wired into run/start/exec commands.
Legacy SSH/GPG proxy code fully removed. Only socketbridge path remains.

---

## Implemented Components

### 1. Host-side Bridge (`internal/socketbridge/bridge.go`)
- muxrpc-style protocol over docker exec stdin/stdout
- Message types: DATA, OPEN, CLOSE, PUBKEY, READY, ERROR
- Sends PUBKEY message with exported GPG public key
- Handles OPEN/DATA/CLOSE for bidirectional socket forwarding
- Connects to host GPG/SSH agents via Unix sockets

### 2. Container-side Server (`internal/hostproxy/internals/cmd/clawker-socket-server/main.go`)
- Reads `CLAWKER_REMOTE_SOCKETS` env var (JSON array of socket configs)
- Creates Unix socket listeners at specified paths
- Forwards connections via muxrpc protocol to host
- Handles file ownership (chown to target user for GPG access)
- Creates pubring.kbx with exported public key

### 3. RuntimeEnv Integration (`internal/docker/env.go`)
- Added `GPGForwardingEnabled`, `SSHForwardingEnabled` to RuntimeEnvOpts
- Produces `CLAWKER_REMOTE_SOCKETS` JSON env var from config

### 4. Entrypoint Updates (`internal/bundler/assets/entrypoint.sh`)
- Legacy SSH/GPG proxy functions removed
- Socket forwarding handled entirely by clawker-socket-server started via docker exec

### 5. SocketBridge Manager (`internal/socketbridge/manager.go`)
- Per-container bridge daemon process manager with PID file tracking
- `EnsureBridge()` is idempotent — spawns `clawker bridge serve` as detached subprocess
- Wired into `run`, `start`, `exec` commands via Factory DI

### 6. Bridge Command (`internal/cmd/bridge/bridge.go`)
- Hidden CLI: `clawker bridge serve --container <id> [--gpg]`
- Writes PID file, creates bridge, handles graceful shutdown

### 7. Test Harness (`test/harness/client.go`)
- Auto-detects GPG/SSH availability on host
- Sets `CLAWKER_REMOTE_SOCKETS` env var
- Starts socket bridge via `StartSocketBridge()`

---

## Architecture

```
HOST                                    CONTAINER
┌─────────────────────┐                ┌─────────────────────────────────┐
│  socketbridge       │                │     clawker-socket-server       │
│                     │                │                                 │
│  Bridge.Start()     │◄──docker exec──►│  1. Parse CLAWKER_REMOTE_SOCKETS│
│  - Sends PUBKEY msg │   stdin/stdout │  2. Write pubring.kbx           │
│  - Handles OPEN     │                │  3. Create Unix socket listeners│
│  - Handles DATA     │                │  4. Forward via muxrpc protocol │
│  - Handles CLOSE    │                │                                 │
│                     │                │  Creates:                       │
│  Connects to:       │                │  - ~/.gnupg/S.gpg-agent (socket)│
│  - GPG extra socket │                │  - ~/.gnupg/pubring.kbx (file)  │
│  - SSH_AUTH_SOCK    │                │  - ~/.ssh/agent.sock (socket)   │
└─────────────────────┘                └─────────────────────────────────┘
```

---

## Protocol

Message format: `[4-byte length][1-byte type][4-byte stream ID][payload]`

| Type | Value | Description |
|------|-------|-------------|
| DATA | 1 | Socket data (bidirectional) |
| OPEN | 2 | New connection (payload = socket type) |
| CLOSE | 3 | Connection closed |
| PUBKEY | 4 | GPG public key data |
| READY | 5 | Forwarder ready |
| ERROR | 6 | Error message |

---

## Configuration

Socket forwarding is controlled by clawker.yaml via `GitCredentialsConfig`:

```yaml
security:
  git_credentials:
    forward_gpg: true   # Enable GPG agent forwarding
    forward_ssh: true   # Enable SSH agent forwarding
```

This flows through `RuntimeEnvOpts` → `RuntimeEnv()` → `CLAWKER_REMOTE_SOCKETS` env var.

---

## Test Verification

```bash
# Run the TDD test
go test -run TestGpgAgentForwarding_EndToEnd ./test/internals/... -v -timeout 3m

# Expected output: PASS
```

---

## Key Files

| File | Purpose |
|------|---------|
| `internal/socketbridge/bridge.go` | Host-side muxrpc bridge |
| `internal/socketbridge/manager.go` | Per-container bridge daemon manager |
| `internal/cmd/bridge/bridge.go` | Hidden `clawker bridge serve` command |
| `internal/hostproxy/internals/cmd/clawker-socket-server/main.go` | Container-side socket server |
| `internal/docker/env.go` | RuntimeEnvOpts with socket forwarding fields + SSH_AUTH_SOCK |
| `internal/bundler/assets/entrypoint.sh` | Entrypoint (legacy proxy code removed) |
| `test/harness/client.go` | Test harness with auto-detection |
| `test/internals/gpgagent_test.go` | GPG agent TDD test |
| `test/internals/sshagent_test.go` | SSH agent integration tests |

## Removed Files (Legacy Proxies)

| File | Was |
|------|-----|
| `internal/workspace/ssh.go` | SSH agent mount logic |
| `internal/workspace/gpg.go` | GPG agent mount logic |
| `internal/hostproxy/ssh_agent.go` | SSH HTTP handler |
| `internal/hostproxy/gpg_agent.go` | GPG HTTP handler |
| `internal/hostproxy/internals/cmd/ssh-agent-proxy/` | Container SSH proxy binary |
| `internal/hostproxy/internals/cmd/gpg-agent-proxy/` | Container GPG proxy binary |
