# clawkerd — Container Control Plane

## Status: BRAINSTORMING — No concrete plan yet. DO NOT IMPLEMENT.

**This memory captures an active brainstorming session between the user and agents. Nothing here is finalized. Do not start implementation. Do not write a plan. Engage the user in discussion and ask what they want to explore next.**

## Origin

While trying to address noisy entrypoint output during container init, it was uncovered that the previous approach — injecting `[clawker:step]` markers into stdout/stderr, intercepting streams, and allocating TTYs behind the user's back — was fundamentally flawed. It was jerry-rigging the user's output streams as an event transport. The problem focus has evolved from "suppress init noise" into solving the root problem: **clawker has no proper communication channel with its containers.** The init progress display becomes just the first consumer of this channel, not the driver of the architecture.

The old plan file at `/Users/andrew/.claude/plans/reflective-gathering-anchor.md` and any `[clawker:step]`/`initprotocol`/`initstream` approach is **obsolete and rejected**.

## Core Principles (from user, non-negotiable)

1. **Streams are sacred.** stdout/stderr belong to the user and the tools running inside the container. Clawker NEVER redirects, suppresses, or alters where scripts and their tools write output.
2. **Never violate expected state.** `-t` means TTY, no `-t` means no TTY. Never allocate TTY features without explicit user flag. Docker CLI never does this — confirmed via deepwiki.
3. **Clawker needs events, but the user doesn't see them.** Internal container-to-host communication must use a side channel, not the user's output streams.
4. **No hackish shortcuts.** Build the right architecture now rather than accumulating technical debt.

## What clawkerd Is

**clawkerd** (clawker daemon) is the container-side component of clawker, following the **agent pattern** from infrastructure management systems (puppet agent, salt minion, kubelet, datadog agent). It is a management agent that runs inside the container, connects back to the host clawker process, and acts on its behalf.

The relationship is **controller (host clawker CLI) ↔ agent (clawkerd inside container)**.

```
Host                                    Container
┌──────────────┐     WebSocket          ┌──────────────┐
│ clawker CLI  │◄═══════════════════════│  clawkerd    │ (root, background, PID managed by tini)
│ (controller) │     side channel       │  (agent)     │
└──────┬───────┘                        └──────────────┘
       │                                ┌──────────────┐
       │         Docker hijacked conn   │ main process │ (user's process: claude, sh, etc.)
       └───────────────────────────────►│ (stdin/out)  │
              user's streams            └──────────────┘
```

Two parallel channels, completely separate:
1. **User's streams** — Docker hijacked connection (stdin/stdout/stderr). Sacred. Untouched.
2. **Control plane** — WebSocket between clawker CLI and clawkerd. Invisible to user.

## What clawkerd Does (its concerns)

- **Connects outward** to the host clawker process via WebSocket on startup
- **Reports events** to the host: init progress, ready/error signals, status, health
- **Receives commands** from the host: process control (exit, restart), file operations, configuration changes
- **Runs as root** (started via sudo + sudoers entry in entrypoint, before any init logic)
- **Persists for container lifetime** (background process managed by tini as PID 1)
- **Handles connection lifecycle** — reconnects if WebSocket drops, buffers events if host disconnects

## What clawkerd Is NOT (not its concerns)

- **NOT a user-facing process** — users never interact with clawkerd directly
- **NOT involved in the user's streams** — stdin/stdout/stderr belong to the user's process. clawkerd communicates exclusively via WebSocket side channel.
- **NOT a replacement for the host proxy** — host proxy handles browser auth, OAuth, git credentials. clawkerd handles container management events and commands.
- **NOT a replacement for the socket bridge** — socket bridge handles SSH/GPG agent forwarding via muxrpc over docker exec. clawkerd handles general events and commands via WebSocket.
- **NOT a generic Docker feature** — this is clawker's innovation. Docker CLI has no concept of a management agent inside containers.

## Process Management: tini via HostConfig.Init

Docker's `HostConfig.Init` API injects **tini** as PID 1 — zombie reaping, signal forwarding, proper cleanup of background daemons. Clawker already has `--init` flag support (`internal/cmd/container/shared/container.go:309`).

**Decision: Clawker ALWAYS sets `HostConfig.Init = true` internally.** This is not a user choice — clawker is a domain-specific runtime, not a generic Docker wrapper. The `--init` CLI flag should be removed; it's always on.

Container lifecycle with tini:
1. tini is PID 1 (injected by Docker, zero config)
2. entrypoint.sh starts `clawkerd &` (background, as root via sudo + sudoers entry)
3. entrypoint.sh does init work (firewall, config, git, post-init)
4. entrypoint.sh `exec`s into main process (claude, or user command)
5. clawkerd runs alongside main process for container's lifetime
6. On exit, tini sends SIGTERM to clawkerd, reaps cleanly

No systemd, no s6-overlay, no new dependencies. Docker ships tini.

## WebSocket Communication

**clawkerd connects outward** to the host clawker CLI process:
- Host clawker starts a WebSocket server on a dynamic port before container creation
- Port injected as env var (e.g. `CLAWKER_DAEMON_URL=ws://host.docker.internal:{port}`)
- clawkerd connects on startup, retries with backoff if needed
- `host.docker.internal` is explicitly allowed by the firewall (iptables rules in init-firewall.sh)

**Host-side connection lifecycle**:
- `clawker run` already stays open (ContainerWait blocks until container exits)
- WebSocket server starts before container, listens for clawkerd connection
- Race between: WebSocket accept (clawkerd connected) vs ContainerWait (container died)
- If container dies before clawkerd connects → error/fallback
- ContainerWait uses long-polling on `/containers/{id}/wait`, NOT Docker events API (confirmed via deepwiki on moby/moby)

## Naming

- Binary: `clawkerd` (Unix daemon convention: `dockerd`, `containerd`, `systemd`)
- Host-side package: TBD — `internal/clawkerd/` or `internal/websocket/`
- Container-side source: `internal/bundler/assets/clawkerd.go` (single-file Go, same pattern as `clawker-socket-server.go`)
- Dockerfile: multi-stage builder (same pattern as socket-server and callback-forwarder)
- Pattern: **agent pattern** (infrastructure management)

## Open Questions (brainstorming, not decided)

- **Host-side package design**: What does the `internal/websocket` or `internal/clawkerd` package API look like? Factory noun? Interface for DI?
- **Message protocol**: What do messages look like on the wire? JSON? Event types, command types?
- **`clawker start` (no `-ai`)**: CLI starts container and exits — nobody listening for clawkerd. Does clawkerd handle this gracefully (no connection = no-op)?
- **`clawker exec`**: Container already running, clawkerd already connected to whoever started it. Can exec piggyback on existing connection?
- **Stream suppression**: Once clawkerd reports "ready", how does the CLI gate the user's streams? (drain in TTY mode, gate writers in non-TTY mode — brainstormed but not finalized)
- **Relationship to existing infra**: Does clawkerd eventually subsume the socket bridge or host proxy roles, or do all three coexist permanently?

## Key Code Paths (reference)

- `internal/cmd/container/shared/container.go:309` — `--init` flag (to be removed, hardcode Init=true)
- `internal/cmd/container/shared/container.go:790-792` — `HostConfig.Init` assignment
- `internal/bundler/assets/Dockerfile.tmpl` — multi-stage builder pattern, binary COPY, sudoers entries
- `internal/bundler/assets/entrypoint.sh` — where clawkerd would be started as background process
- `internal/cmd/container/run/run.go` — attachThenStart, ContainerWait, stream handling
- `internal/hostproxy/` — existing host proxy (NOT being modified for clawkerd)
- `internal/socketbridge/` — existing socket bridge (separate concern from clawkerd)

## Lessons Learned

- Docker CLI NEVER allocates TTY without explicit `-t` — this is a trust contract
- Docker merges stdout+stderr in TTY mode, uses stdcopy multiplexing in non-TTY mode (confirmed via deepwiki)
- `HijackedResponse.Reader` is `*bufio.Reader` wrapping `net.Conn`
- The socket bridge starts AFTER container start (docker exec) — wrong timing for init events
- The host proxy starts BEFORE container creation — right timing for network access
- `host.docker.internal` is explicitly firewalled as allowed
- ContainerWait is long-polling on a dedicated endpoint, not the Docker events API
- Docker's `HostConfig.Init` injects tini as PID 1 — zero-config process supervision

---

**IMPERATIVE**: This is a BRAINSTORMING record. Do NOT implement anything. Engage the user in discussion. Always check with the user before starting any work.
