# GPG Agent Forwarding Implementation Guide

## DIRECTIVE FOR IMPLEMENTERS

**Your mission:** Implement GPG agent forwarding in clawker that mirrors VS Code's devcontainer approach.

**Rules:**
1. Review the VS Code architecture diagrams below
2. Implement using clawker internals (hostproxy, container-side components)
3. **YOU ARE NOT DONE UNTIL `go test -run TestGpgAgentForwarding_EndToEnd ./test/internals/... -v` PASSES**
4. **YOU MUST NEVER MODIFY THE TEST** - fix your implementation until it passes (TDD)

---

## VS Code Architecture (Reference Implementation)

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                   HOST (macOS)                                   │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  ┌─────────────────────────────────────┐     ┌─────────────────────────────────┐│
│  │         VS Code Application         │     │        Host GPG Agent           ││
│  │          (Electron/Node)            │     │        (gpg-agent daemon)       ││
│  │                                     │     │                                 ││
│  │  ┌───────────────────────────────┐  │     │  Socket:                        ││
│  │  │ Remote Containers Extension   │  │     │  ~/.gnupg/S.gpg-agent           ││
│  │  │                               │  │     │                                 ││
│  │  │ - Injects server.js into ctr  │  │     │  - Holds secret keys            ││
│  │  │ - Sets REMOTE_CONTAINERS_*    │  │     │  - Performs crypto ops          ││
│  │  │ - Exports public key          │  │     │  - Responds to Assuan           ││
│  │  │ - Maintains muxrpc channel    │◄─┼─────┼─►                               ││
│  │  └───────────────────────────────┘  │     │                                 ││
│  └──────────────────┬──────────────────┘     └─────────────────────────────────┘│
│                     │                                                            │
│                     │ docker exec (stdin/stdout bidirectional)                   │
│                     │                                                            │
└─────────────────────┼────────────────────────────────────────────────────────────┘
                      │
                      │ muxrpc over stdin/stdout
                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                            CONTAINER                                             │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  ┌───────────────────────────────────────────────────────────────────────────┐  │
│  │                    vscode-remote-containers-server.js                      │  │
│  │                           (PID 267, node)                                  │  │
│  │                                                                            │  │
│  │  Environment:                                                              │  │
│  │  REMOTE_CONTAINERS_SOCKETS=[                                               │  │
│  │    "/tmp/vscode-ssh-auth-xxx.sock",                                        │  │
│  │    "/home/node/.gnupg/S.gpg-agent"     ◄── Created by this server          │  │
│  │  ]                                                                         │  │
│  │                                                                            │  │
│  │  On startup:                                                               │  │
│  │  1. Parse REMOTE_CONTAINERS_SOCKETS                                        │  │
│  │  2. For each path: net.createServer().listen(path)                         │  │
│  │  3. Forward connections via muxrpc to VS Code host                         │  │
│  └────────────────────────────────┬──────────────────────────────────────────┘  │
│                               │                                                  │
│                               │ creates & listens                                │
│                               ▼                                                  │
│  ┌────────────────────────────────────────────────────────────────────────────┐ │
│  │                        ~/.gnupg/S.gpg-agent                                 │ │
│  │                    (Unix socket, owned by socket server)                    │ │
│  └────────────────────────────────────────────────────────────────────────────┘ │
│                               ▲                                                  │
│                               │ connects                                         │
│  ┌────────────────────────────┴───────────────────────────────────────────────┐ │
│  │                              GPG Binary                                     │ │
│  │                                                                             │ │
│  │  1. Reads ~/.gnupg/pubring.kbx (674 bytes) → has public key                 │ │
│  │  2. Connects to ~/.gnupg/S.gpg-agent                                        │ │
│  │  3. Sends "HAVEKEY <keygrip>" Assuan command                                │ │
│  │  4. Server forwards to host, host agent responds "OK"                       │ │
│  │  5. GPG displays "sec" (secret key available via agent)                     │ │
│  └─────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                  │
│  ~/.gnupg/ contents:                                                             │
│  ├── S.gpg-agent       (socket, created by server.js)                            │
│  ├── pubring.kbx       (674 bytes, public key only - NOT a directory)            │
│  └── trustdb.gpg       (trust levels)                                            │
│                                                                                  │
│  NO private-keys-v1.d/ - all crypto via forwarded agent                          │
└──────────────────────────────────────────────────────────────────────────────────┘
```

## Data Flow: Signing Operation

```
CONTAINER                                    HOST
─────────────────────────────────────────────────────────────────────────────────
1. gpg --armor --detach-sign
      │
2. GPG reads pubring.kbx → finds keygrip
      │
3. GPG connects to S.gpg-agent socket
      │
4. Assuan: "SIGKEY <keygrip>", "PKSIGN"
      │
      └─────────────────────────────────────► 5. Socket server receives
                                                    │
                                              6. Forwards to host GPG agent
                                                    │
                                              7. Host agent signs with private key
                                                    │
      ◄─────────────────────────────────────── 8. Returns "D <signature>" + "OK"
      │
9. GPG outputs -----BEGIN PGP SIGNATURE-----
```

---

## Clawker Equivalent Architecture (TO IMPLEMENT)

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                   HOST                                           │
├─────────────────────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────┐     ┌─────────────────────────────────┐│
│  │           clawker hostproxy         │     │        Host GPG Agent           ││
│  │                                     │     │                                 ││
│  │  - Receives forwarded connections   │     │  Socket: ~/.gnupg/S.gpg-agent   ││
│  │  - Connects to host GPG agent       │◄────┼─►                               ││
│  │  - Exports public key at startup    │     │                                 ││
│  └──────────────────┬──────────────────┘     └─────────────────────────────────┘│
│                     │                                                            │
│                     │ HTTP/WebSocket (via CLAWKER_HOST_PROXY env var)            │
│                     │                                                            │
└─────────────────────┼────────────────────────────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                            CONTAINER                                             │
├─────────────────────────────────────────────────────────────────────────────────┤
│  ┌───────────────────────────────────────────────────────────────────────────┐  │
│  │                    clawker-socket-server (or similar)                      │  │
│  │                                                                            │  │
│  │  Environment:                                                              │  │
│  │  CLAWKER_REMOTE_SOCKETS='["/home/claude/.gnupg/S.gpg-agent"]'              │  │
│  │  CLAWKER_HOST_PROXY="http://host.docker.internal:18374"                    │  │
│  │                                                                            │  │
│  │  On startup:                                                               │  │
│  │  1. Parse CLAWKER_REMOTE_SOCKETS                                           │  │
│  │  2. For each path: create Unix socket listener                             │  │
│  │  3. Forward connections to hostproxy                                       │  │
│  └────────────────────────────────┬──────────────────────────────────────────┘  │
│                               │                                                  │
│                               ▼                                                  │
│  ┌────────────────────────────────────────────────────────────────────────────┐ │
│  │                   /home/claude/.gnupg/S.gpg-agent                           │ │
│  │                   (Unix socket, created by socket server)                   │ │
│  └────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                  │
│  /home/claude/.gnupg/ contents (CREATED BY CLAWKER):                             │
│  ├── S.gpg-agent       (socket, created by socket server)                        │
│  ├── pubring.kbx       (~674 bytes, exported public key - MUST BE A FILE)        │
│  └── trustdb.gpg       (trust levels)                                            │
└──────────────────────────────────────────────────────────────────────────────────┘
```

---

## Test Requirements (What You Must Implement)

The test at `test/internals/gpgagent_test.go` checks these steps IN ORDER:

| Step | Requirement | Test Assertion |
|------|-------------|----------------|
| 2 | `CLAWKER_REMOTE_SOCKETS` env var set | Must contain GPG socket path |
| 3 | Socket server process running | `ps aux` shows clawker/socket-server process |
| 4 | GPG socket exists | `/home/claude/.gnupg/S.gpg-agent` is a socket file |
| 5 | Public key exported | `pubring.kbx` > 32 bytes (not empty stub) |
| 6 | GPG sees secret key | `gpg --list-secret-keys` shows "sec" marker |
| 7 | GPG can sign | `gpg --detach-sign` produces valid signature |
| 8 | Git signed commit | `git commit -S` succeeds |
| 9 | Signature verifies | `git log --show-signature` shows signature |

---

## ⚠️ INVALID APPROACHES - DO NOT ATTEMPT ⚠️

### 1. Direct Socket Bind Mounting
**Why it fails:** Docker Desktop on macOS doesn't properly forward Unix sockets via SDK's `HostConfig.Mounts`. Socket appears but connections fail.

### 2. Using Only Extra Socket (`S.gpg-agent.extra`)
**Why it fails:** Extra socket can sign but `KEYINFO --list` is restricted. Without pubring.kbx, `gpg --list-secret-keys` returns empty.

### 3. Host's pubring.kbx Structure
**Why it fails:** Modern hosts use keyboxd (pubring.kbx is a DIRECTORY). Container needs traditional keyring (pubring.kbx as FILE with exported public key).

---

## Key Files

| File | Purpose |
|------|---------|
| `test/internals/gpgagent_test.go` | **TDD test - DO NOT MODIFY** |
| `internal/hostproxy/gpg_agent.go` | Hostproxy GPG forwarding (needs work) |
| `internal/hostproxy/internals/cmd/gpg-agent-proxy/` | Container-side proxy binary |
| `internal/workspace/gpg.go` | Current broken mount configuration |

---

## Verification Commands

```bash
# Run the TDD test (must pass when implementation is complete)
go test -run TestGpgAgentForwarding_EndToEnd ./test/internals/... -v -timeout 3m

# Compare with working VS Code devcontainer
docker exec eager_murdock gpg --list-secret-keys
docker exec eager_murdock bash -c 'echo "test" | gpg --armor --detach-sign'
```

---

## IMPERATIVE

**YOU MUST NOT MODIFY `test/internals/gpgagent_test.go`.**

The test defines the contract. Your implementation must satisfy it. This is TDD - write code until the test passes.

When the test passes, GPG agent forwarding works correctly.
