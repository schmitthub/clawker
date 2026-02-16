# clawkerd — Container Control Plane

## Status: POC IN PROGRESS — SchedulerService next

**POC iteration 1 validated: Register + RunInit + OTEL pipeline to Loki/Grafana. Next iteration: SchedulerService endpoints for container lifecycle management.**

## Origin

While trying to address noisy entrypoint output during container init, it was uncovered that the previous approach — injecting `[clawker:step]` markers into stdout/stderr, intercepting streams, and allocating TTYs behind the user's back — was fundamentally flawed. It was jerry-rigging the user's output streams as an event transport. The problem focus has evolved from "suppress init noise" into solving the root problem: **clawker has no proper communication channel with its containers.** The init progress display becomes just the first consumer of this channel, not the driver of the architecture.

The old plan file at `/Users/andrew/.claude/plans/reflective-gathering-anchor.md` and any `[clawker:step]`/`initprotocol`/`initstream` approach is **obsolete and rejected**.

## Core Principles (from user, non-negotiable)

1. **Streams are sacred.** stdout/stderr belong to the user and the tools running inside the container. Clawker NEVER redirects, suppresses, or alters where scripts and their tools write output.
2. **Never violate expected state.** `-t` means TTY, no `-t` means no TTY. Never allocate TTY features without explicit user flag. Docker CLI never does this — confirmed via deepwiki.
3. **Clawker needs events, but the user doesn't see them.** Internal container-to-host communication must use a side channel, not the user's output streams.
4. **No hackish shortcuts.** Build the right architecture now rather than accumulating technical debt.

## Architecture: K8s-Inspired Flattened Container Orchestrator (DECIDED)

### The Insight

Clawker is a **simplified, flattened container orchestrator** purpose-built for Claude Code containers. The K8s model maps cleanly when you collapse the tiers:

```
K8s                              Clawker
─────────────────────────────    ─────────────────────────────────
kubectl                          clawker CLI
  "submit intent"                  "clawker run, create, stop"

kube-scheduler                   also clawker CLI
  "decide where it runs"            "resolve project, build image,
                                      pick config, choose workspace"

kube-apiserver                   clawker control plane (daemon)
  "central state, API gateway"     "state tracking, command relay,
                                      persistent brain"

controller-manager               also control plane
  "reconcile desired vs actual"    "Docker Events → detect drift,
                                      track connected agents"

kubelet (on each node)           clawkerd (in each container)
  "per-node agent, reports up,     "per-container agent, reports up,
   executes commands down"           executes commands down"

containerd + CRI                 Docker Engine + Docker API
  "container runtime"              "container runtime (already there)"

etcd                             (none — ephemeral state, no DB)
  "persistent state store"         "reconstructable from Docker on restart"
```

Note: Docker Engine uses containerd internally — we already have containerd on the host. We don't replace it, we talk through Docker's API layer which abstracts over containerd.

### Two Communication Paths (DECIDED)

**Control path** — orchestration goes through the control plane:
```
clawker CLI ───► Control Plane ───► Docker Engine
                     │
  "create this"      │  "docker create"
  "start this"       │  "docker start"
  "stop this"        │  "docker stop"
  "what's running?"  │  "docker ps + clawkerd state"
```

**Data path** — user streams go direct (sacred, untouched):
```
clawker CLI ═══════════════════► Docker Engine ═══► container
              hijacked connection
              stdin/stdout/stderr
              (control plane is NOT in this path)
```

The control plane NEVER touches the user's streams. The CLI attaches directly to Docker for interactive connections — same as today. Create/start/stop/inspect operations route through the control plane.

### Full Architecture Diagram

```
┌──────────────────────────────────────────────────────────────┐
│                        HOST MACHINE                          │
│                                                              │
│  clawker CLI (ephemeral)         Control Plane (daemon)      │
│  ┌─────────────┐                 ┌──────────────────────┐    │
│  │ • scheduler  │     gRPC       │ • Docker Events sub  │    │
│  │ • kubectl    │ ──────────────►│ • Agent registry     │    │
│  │ • user UI    │  "create X"    │ • State tracking     │    │
│  │              │  "start X"     │ • HTTP endpoints     │    │
│  │              │  "status?"     │   (git creds, OAuth) │    │
│  │              │◄────────────── │ • gRPC services      │    │
│  │              │  results +     │   - SchedulerService │    │
│  │              │  streamed      │   - AgentService     │    │
│  └──────┬───────┘  progress      ├──────────┐           │    │
│         │                        │          │           │    │
│         │ attach/exec            │  Docker  │           │    │
│         │ (direct, sacred)       │  API     │           │    │
│         │                        │          ▼           │    │
│         │                        │  ┌──────────────┐   │    │
│         └────────────────────────┼─►│ Docker Engine │   │    │
│                                  │  │ (containerd)  │   │    │
│                                  │  └───────┬───────┘   │    │
│                                  └──────────┼───────────┘    │
│                                             │                │
├─────────────────────────────────────────────┼────────────────┤
│  ┌──────────────────────────────────────────┼─────────┐      │
│  │              CONTAINER                   │         │      │
│  │                                          ▼         │      │
│  │  clawkerd (root, background)    main process       │      │
│  │  ┌───────────────┐             ┌───────────┐      │      │
│  │  │ • init mgmt   │             │ claude/sh │      │      │
│  │  │ • event report│── gRPC ──►  │ (user's)  │      │      │
│  │  │ • cmd handler │  to CP      └───────────┘      │      │
│  │  │ • system ops  │                                 │      │
│  │  └───────────────┘                                 │      │
│  └────────────────────────────────────────────────────┘      │
└──────────────────────────────────────────────────────────────┘
```

### Conversation Flow Example

```
CLI → CP:  "Create container: project=foo, agent=dev, config={...}"
CP:        (docker create, registers container, waits for Docker event)
CP → CLI:  "Created: container_id=abc123"

CLI:       (docker attach abc123 — direct hijacked connection to Docker)
CLI → CP:  "I've attached to abc123, start it"
CP:        (docker start abc123)
CP:        (Docker Event: container started)
CP:        (clawkerd connects from inside, reports init progress)
CP → CLI:  "Started. Init in progress..."
CP → CLI:  "Init step 1/5: firewall... done"
CP → CLI:  "Init step 2/5: git config... done"
CP → CLI:  "Ready."
```

### Control Plane Has Two Information Channels

1. **Docker Events** — external view ("container X started/died"). Authoritative for container existence.
2. **clawkerd gRPC connections** — internal view ("container X init step 3 completed, ready"). Application-level state.

State matrix:
| Docker Events | clawkerd | Meaning |
|---|---|---|
| `start` | not connected | Container booting, clawkerd hasn't connected yet |
| `start` | connected | Full communication — normal operating state |
| `start` | disconnected | Container running but clawkerd crashed/missing |
| `die` | was connected | Clean shutdown — both signals confirm |
| `die` | never connected | Container died before clawkerd could connect |

### CLI Lifecycle with Control Plane

CLI calls `EnsureControlPlane()` before any container operation (same pattern as current `EnsureHostProxy()`). Control plane auto-starts if needed. Stays up while containers exist (cross-referencing Docker Events with connected agents).

CLI also notifies control plane of attach events informationally ("I attached to X") — control plane doesn't track CLI session liveness (CLI is ephemeral) but records that an attach occurred.

### Naming (DECIDED)
- **Clawker Control Plane** — host-side singleton daemon. Replaces the host proxy entirely (absorbs its HTTP endpoints). gRPC services, Docker Events subscriber, connection registry, source of truth for container state.
- **clawkerd** — container-side **system daemon**. NOT an "agent" — in clawker's domain, "agent" refers to a Claude Code instance running in the container. clawkerd is the management daemon that reports to the CP and executes system-level commands. Named specifically to avoid confusion with the Claude Code agent it manages.
- **clawker CLI** — user-facing tool. Scheduler + kubectl. Sends requests to control plane, attaches directly to Docker for streams.

### Host Proxy Retirement (DECIDED)
The current host proxy (`internal/hostproxy/`) is **replaced by the control plane**. Its existing HTTP endpoints (git credentials, OAuth callbacks, browser open, health) become endpoints on the control plane. The "host proxy" name retires. Same daemon slot, bigger role.

### Docker Events API (DECIDED — replaces polling)
The host proxy's current approach — polling `ContainerList()` every 30s — is brittle and retired.

The control plane uses **Docker Events API** — a persistent streaming HTTP connection (chunked JSON):
- `client.Events(ctx, options)` returns `<-chan events.Message` + `<-chan error`
- **Label filtering**: `label=dev.clawker.managed=true` — only clawker container events
- Real-time: `create`, `start`, `stop`, `die`, `destroy`, `restart`, `health_status`, `exec_create`, etc.
- **Reconnection is manual** — control plane needs a reconnect loop for Docker daemon restarts

## What clawkerd Does (its concerns)

- **Connects outward** to the control plane via gRPC on startup
- **Reports events** to control plane: init progress, ready/error signals, process lifecycle, health
- **Receives commands** from control plane: shutdown, exec, config updates, credential notifications
- **Runs as root** — the entrypoint itself runs as root (Dockerfile pattern change). Management agents run as root — kubelet, salt minion, puppet agent all do. clawkerd needs full control over the container's internals.
- **Persists for container lifetime** (background process managed by tini as PID 1)
- **Handles connection lifecycle** — reconnects if gRPC stream drops, buffers events if control plane unreachable

## What clawkerd Is NOT (not its concerns)

- **NOT a user-facing process** — users never interact with clawkerd directly
- **NOT involved in the user's streams** — stdin/stdout/stderr belong to the user's process. clawkerd communicates exclusively via gRPC side channel.
- **NOT a replacement for the socket bridge** — socket bridge handles SSH/GPG agent forwarding via muxrpc over docker exec. clawkerd handles general events and commands via gRPC. (Long-term: may subsume socket bridge, but not initially.)
- **NOT a generic Docker feature** — this is clawker's innovation. Docker CLI has no concept of a management agent inside containers.

## Process Management: tini via HostConfig.Init

Docker's `HostConfig.Init` API injects **tini** as PID 1 — zombie reaping, signal forwarding, proper cleanup of background daemons. Clawker already has `--init` flag support (`internal/cmd/container/shared/container.go:309`).

**Decision: Clawker ALWAYS sets `HostConfig.Init = true` internally.** This is not a user choice — clawker is a domain-specific runtime, not a generic Docker wrapper. The `--init` CLI flag should be removed; it's always on.

### Entrypoint Runs as Root + gosu Drop (DECIDED)

Follows the official Docker image pattern (postgres, redis, etc.):

**Dockerfile change:** Entrypoint runs as root. The `USER ${USERNAME}` directive (currently line 260) is repositioned or the entrypoint is configured to run as root. The user switch happens at exec time via gosu/su-exec, not via Dockerfile `USER`.

**Container lifecycle with tini:**
1. tini is PID 1 (injected by Docker via `HostConfig.Init = true`)
2. entrypoint.sh runs as **root** (no sudo needed for anything)
3. entrypoint.sh starts `clawkerd &` (background, root — native, no sudo)
4. entrypoint.sh does init work as root (firewall, config, git, post-init — no sudoers entries needed)
5. entrypoint.sh drops privileges: `exec gosu ${USERNAME} "$@"` (or su-exec on Alpine)
6. Main process (claude, or user command) runs as claude user
7. clawkerd continues running as root alongside main process
8. On exit, tini sends SIGTERM to clawkerd, reaps cleanly

**Benefits over previous approach:**
- Eliminates ALL sudoers entries (firewall, clawkerd)
- Battle-tested pattern used by official Docker images
- Clean privilege boundary: root for system management, user for application

**gosu vs su-exec:** gosu (Go, ~1.8MB, works on Alpine+Debian) vs su-exec (C, ~10KB, native on Alpine, must compile on Debian). Decision TBD — may use su-exec on Alpine, gosu on Debian, or just gosu everywhere.

**Future evolution:** Option 3 (clawkerd as custom init, replacing tini via `HostConfig.InitPath`) is the long-term path. clawkerd becomes PID 1 with signal forwarding + zombie reaping. Not for v1.

No systemd, no s6-overlay. Docker ships tini for now.

## gRPC Communication (DECIDED)

**Why gRPC:** Infrastructure control planes use gRPC (kubelet, Telepresence, containerd). Protobuf schema = single source of truth, generated code for both sides (zero drift), first-class bidirectional streaming, built-in health checks.

**gRPC for everything (DECIDED):** Both CLI→CP and clawkerd→CP use gRPC. One protocol, one server, generated clients for all consumers. Type safety everywhere.

**Three gRPC services across two servers (DECIDED — Option A, containerd shim v2 pattern):**

1. **SchedulerService** (on CP's gRPC server) — CLI → CP. Container lifecycle operations (create, start, stop, remove), state queries, streaming progress.
2. **AgentReportingService** (on CP's gRPC server) — clawkerd → CP. Registration, progress reporting, heartbeat, events.
3. **AgentCommandService** (on clawkerd's gRPC server) — CP → clawkerd. RunInit, Shutdown, ExecCommand, ConfigUpdate, etc. Discrete unary RPCs per operation.

**Why two gRPC servers (DECIDED):**
- clawkerd runs a gRPC server inside the container (listens on clawker-net)
- CP runs a gRPC server on the host (listens on localhost for CLI + clawker-net for agents)
- clawkerd calls CP's AgentReportingService RPCs (Register, ReportProgress, etc.)
- CP calls clawkerd's AgentCommandService RPCs (RunInit, Shutdown, etc.)
- Each operation is its own RPC with own types — clean, testable, independently evolvable
- Per-operation deadlines, retries, interceptors — gRPC gives this for free with discrete RPCs
- Adding a future command = add one RPC, write handler, done. No growing oneof/switch statements.
- Follows containerd shim v2 pattern exactly (containerd calls shim's Task service, shim publishes events back)

**Rejected: single bidi Channel() stream (Option B).** Easy now, painful later — every new command is a new oneof variant, growing switch dispatch, no per-operation middleware, shared failure domain. Serves the task, not the project.

**Address discovery:** clawkerd includes its gRPC listen address in the `Register()` call to CP. CP connects back. Same pattern as containerd shim writing its socket address.

**Plus HTTP endpoints (retained):** git credentials, OAuth callbacks, browser open, health — used by container scripts via curl. Same daemon, dual listener (gRPC port + HTTP port).

**Protocol definition:**
```
internal/clawkerd/protocol/
  clawkerd.proto          ← single source of truth (protobuf schema)
  clawkerd.pb.go          ← generated, committed to repo
  clawkerd_grpc.pb.go     ← generated, committed to repo
```

### Protocol Design Research (from deepwiki analysis)

Studied five production agent systems:

1. **OpAMP (OpenTelemetry)** — Two fat messages with optional fields. Status compression. THE emerging agent management standard.
2. **containerd shim v2** — Unary RPCs per command + async typed event stream. Separate message per lifecycle event.
3. **CRI (kubelet ↔ containerd)** — ~25 unary RPCs + `GetContainerEvents` server-streaming RPC with enum-typed events.
4. **Telepresence** — Multiple purpose-specific RPCs. `ArriveAsAgent()` + `WatchIntercepts()` + `Tunnel()` bidi stream.
5. **BuildKit** — Vertex model for sequenced progress: steps have IDs, `started`/`completed` timestamps, sub-statuses, logs.

**Standard pattern found:** Commands = unary RPCs. Events = streaming RPC. All five systems agree on this split.

**Rejected approaches:**
- Discriminated union / tagged union envelope (too close to MCP messaging protocol, over-engineered for a domain-specific agent protocol)
- OpAMP fat messages (proven but doesn't feel right for our domain)
- containerd's `google.protobuf.Any` envelope (loses type safety)

### CP Owns ALL Docker Resource Lifecycle (DECIDED)

**CP is the single gateway to Docker for all resource lifecycle management.** CLI never talks to Docker directly except for container I/O (attach/exec streams).

This means CP owns:
- **Containers**: create, start, stop, remove, inspect, list, rename
- **Volumes**: create, remove, list, inspect (config volumes, history volumes, share volumes)
- **Networks**: create, remove, list, inspect, connect, disconnect (clawker-net)
- **Images**: build, pull, remove, list, inspect, tag

CLI's only direct Docker interaction is **container I/O** — attach and exec streams (the sacred data path).

```
CLI ──── gRPC ────► CP ──── Docker API ────► Docker Engine
 │                   │
 │  schedules:       │  executes:
 │  "build image"    │  docker build
 │  "create volume"  │  docker volume create
 │  "create net"     │  docker network create
 │  "create ctr"     │  docker create
 │  "start ctr"      │  docker start
 │  "stop ctr"       │  docker stop
 │  "rm volume"      │  docker volume rm
 │                   │
 └──── Docker API (direct) ────► container I/O
        attach, exec streams only
```

**Implication:** The `CreateContainer()` orchestration and ALL Docker operations currently in `internal/cmd/` command layer migrate to the CP. The CLI becomes a thin gRPC client for resource management.

**Next step:** Design the SchedulerService gRPC schema based on the inventory below.

## Docker API Call Inventory (from `internal/cmd/` → `docker.Client`)

Complete inventory of every `docker.Client` method call made by the command layer. These define what the CP's gRPC SchedulerService needs to expose.

### Container Lifecycle (→ CP)

| Method | Call Sites (in `internal/cmd/container/` unless noted) |
|--------|-------------------------------------------------------|
| `ContainerCreate` | `shared/container.go:1614` (single entry point via `CreateContainer()`) |
| `ContainerStart` | `run/run.go:241,331`, `start/start.go:232,366`, `restart/restart.go:128`, `loop/shared/runner.go:601` |
| `ContainerStop` | `stop/stop.go:143` |
| `ContainerKill` | `kill/kill.go:123`, `stop/stop.go:137`, `restart/restart.go:125` |
| `ContainerPause` | `pause/pause.go:116` |
| `ContainerUnpause` | `unpause/unpause.go:114` |
| `ContainerRestart` | `restart/restart.go:139` |
| `ContainerRemove` | `remove/remove.go:146`, `shared/container.go:1642,1658` (cleanup) |
| `RemoveContainerWithVolumes` | `remove/remove.go:142` |
| `ContainerRename` | `rename/rename.go:95` |
| `ContainerUpdate` | `update/update.go:168` |

### Container Queries (→ CP)

| Method | Call Sites |
|--------|-----------|
| `ContainerInspect` | `inspect/inspect.go:117`, `attach/attach.go:127`, `start/start.go:160,386` |
| `ListContainers` | `list/list.go:120` |
| `ListContainersByProject` | `list/list.go:118` |
| `ContainerListRunning` | `stats/stats.go:107` |
| `FindContainerByName` | 18 call sites across inspect, cp, pause, stop, unpause, top, wait, start, restart, remove, rename, kill, exec, update, stats, logs |
| `ContainerWait` | `wait/wait.go:118`, `start/start.go:343`, `run/run.go:432`, `loop/shared/runner.go:692` |
| `ContainerLogs` | `logs/logs.go:130` |
| `ContainerTop` | `top/top.go:103` |
| `ContainerStats` | `stats/stats.go:227` |
| `ContainerStatsOneShot` | `stats/stats.go:156` |
| `CopyToContainer` | `cp/cp.go:203,222`, `shared/containerfs.go:178` |
| `CopyFromContainer` | `cp/cp.go:167` |

### Container I/O — STAYS ON CLI (sacred data path)

| Method | Call Sites |
|--------|-----------|
| `ContainerAttach` | `attach/attach.go:153`, `run/run.go:288`, `start/start.go:190`, `loop/shared/runner.go:567` |
| `ContainerResize` | `run/run.go:352`, `start/start.go:259`, `attach/attach.go:163` |
| `ExecCreate` | `exec/exec.go:191` |
| `ExecStart` | `exec/exec.go:203` |
| `ExecAttach` | `exec/exec.go:229` |
| `ExecResize` | `exec/exec.go:239` |
| `ExecInspect` | `exec/exec.go:308` |
| `NewPTYHandler()` | `run:277`, `start:179`, `attach:145`, `exec:217` |

### Image Operations (→ CP)

| Method | Call Sites |
|--------|-----------|
| `ImageList` | `image/list/list.go:109` |
| `ImageInspect` | `image/inspect/inspect.go:70` |
| `ImageRemove` | `image/remove/remove.go:89` |
| `ImagesPrune` | `image/prune/prune.go:93` |
| `ImageExists` | `container/create/create.go:144`, `container/run/run.go:167`, `loop/shared/lifecycle.go:101` |
| `ResolveImageWithSource` | `container/create/create.go:128`, `container/run/run.go:151`, `loop/shared/lifecycle.go:85,198` |
| `BuildImage` | `init/init.go:249` |
| `BuildDefaultImage` | `container/create/create.go:155`, `container/run/run.go:178` (passed as closure) |
| `builder.Build` | `image/build/build.go:228,259` (via `docker.NewBuilder()`) |

### Volume Operations (→ CP)

| Method | Call Sites |
|--------|-----------|
| `VolumeCreate` | `volume/create/create.go:88` |
| `VolumeList` | `volume/list/list.go:72` |
| `VolumeInspect` | `volume/inspect/inspect.go:70` |
| `VolumeRemove` | `volume/remove/remove.go:76`, `shared/container.go:1546` (cleanup) |
| `VolumesPrune` | `volume/prune/prune.go:81` |
| `EnsureVolume` | `workspace/strategy.go:105,116`, `workspace/snapshot.go:72` (via CreateContainer path) |
| `CopyToVolume` | `shared/container.go:1567` (via CreateContainer path) |

### Network Operations (→ CP)

| Method | Call Sites |
|--------|-----------|
| `NetworkCreate` | `network/create/create.go:92` |
| `NetworkList` | `network/list/list.go:68` |
| `NetworkInspect` | `network/inspect/inspect.go:75` |
| `NetworkRemove` | `network/remove/remove.go:76` |
| `NetworksPrune` | `network/prune/prune.go:84` |
| `EnsureNetwork` | `monitor/up/up.go:92`, `start/start.go:234`, `restart/restart.go:130`, `shared/container.go:1620` |

### Docker Package Utility Functions (used by cmd layer)

| Function | Purpose |
|----------|---------|
| `docker.ContainerName()` | Naming convention (`clawker.project.agent`) |
| `docker.ContainerNamesFromAgents()` | Agent→container name resolution (13+ call sites) |
| `docker.VolumeName()` | Volume naming (`clawker.project.agent.type`) |
| `docker.ImageTag()` | Image tag convention |
| `docker.ImageLabels()` | Label generation |
| `docker.RuntimeEnv()` | Container environment setup |
| `docker.GenerateRandomName()` | Random agent name gen |
| `docker.BuildKitEnabled()` | BuildKit detection |
| `docker.NewBuilder()` | Builder factory |

## Open Topics: Communication Needs by Direction

### 1. CLI → CP (inventoried above)
**Status: DONE.** 45 Docker API calls migrate to CP as gRPC SchedulerService surface. 7 stay on CLI (attach/exec streams).

### 2. CP → CLI
**Status: IN PROGRESS.** CP never requests anything from CLI (CLI is ephemeral). CP pushes responses, progress, and events.

**Key insight:** CLI is purely the UI layer. Its `tui.RunProgress` / `tui.ProgressStep` components consume event channels for real-time rendering. Today those channels are fed by direct Docker API calls. Tomorrow they're fed by gRPC streams from CP. Therefore **every mutating RPC should be server-streaming**, returning progress events the CLI can render.

**Three categories of CP→CLI communication:**

**A. Server-streaming mutating RPCs (progress feedback):**
Every operation the user waits on streams progress events back to CLI for TUI rendering.
- `BuildImage()` → pulling layers, compiling stages, tagging
- `CreateContainer()` → resolving image, ensuring volumes, ensuring network, creating
- `StartContainer()` → starting, waiting for healthy
- `StopContainer()` → sending signal, waiting, stopped
- `RemoveContainer()` → stopping (if running), removing volumes, removed
- `ImagesPrune()` → removing image A, image B, reclaimed X bytes
- `VolumesPrune()` → removing volume A, volume B
- `NetworksPrune()` → removing network A
- Other mutating ops (Kill, Pause, Unpause, Restart, Rename, Update) — may be quick enough for unary, TBD

**B. Unary query RPCs (request/response):**
- List, Inspect, Top, Stats, Logs — return data, no streaming progress needed

**C. No separate WatchContainer RPC (DECIDED):**
WatchContainer as a standalone subscription is eliminated. CP folds Docker Events + clawkerd reports into the streaming responses of the RPCs that initiated the operations. CLI doesn't separately subscribe — it gets events as part of the operation it asked for.

**The `clawker run` flow (DECIDED):**
```
CLI → CP:      CreateContainer(config)       [server-streaming RPC]
CP → CLI:        ← resolving image...
CP → CLI:        ← ensuring volumes...
CP → CLI:        ← ensuring network...
CP → CLI:        ← created: container_id=abc123
                                              [RPC complete]

CLI → Docker:  attach(abc123)                [goroutine A — blocking, waits for Docker]
CLI → CP:      NotifyAttach(abc123)          [unary, informational, fire-and-forget]
CLI → CP:      StartContainer(abc123)        [goroutine B — streaming diagnostics]
CP → CLI:        ← starting...
CP → CLI:        ← clawkerd connected
CP → CLI:        ← init: firewall... done
CP → CLI:        ← init: git config... done
CP → CLI:        ← ready (or: error with details)

Happy path: Docker attach connects → CLI is live, closes/ignores CP stream
Failure path: Docker attach times out → CLI reads CP stream for rich diagnostics
             → prints detailed user error + logs full trace to file
```

Key decisions in this flow:
- CLI attaches to Docker **independently** of CP — doesn't wait for CP "ready" signal
- NotifyAttach is **informational** (no state change) — separate from StartContainer (state-changing)
- CLI never needs raw Docker Events directly — CP folds them into streaming RPC responses

**CP stream mode rule of thumb (DECIDED):**
- **Gate:** CLI waits for streaming RPC to complete — the stream IS the result. Used when CLI has no concurrent sacred path operation.
- **Diagnostic:** CP stream is a sidecar — CLI doesn't block on it. Fire-and-forget on success, gold mine on failure. Used when CLI has a concurrent Docker attach contract.

**The deciding factor is whether CLI has an active sacred path operation (Docker attach), NOT the command name:**
- `-it` / `-ia` flags → CLI has a Docker attach contract → CP stream is **diagnostic** sidecar
- No attach (detached, no TTY, no stdin) → CLI has nothing from Docker → CP stream is **gate**
- All non-attach lifecycle operations (create, stop, build, remove, prune) → always **gate**
- Examples:
  - `clawker run -it @` → create=gate, start=diagnostic (attach is concurrent)
  - `clawker run --detach @` → create=gate, start=gate (no attach)
  - `clawker start -ia agent` → start=diagnostic (attach is concurrent)
  - `clawker start agent` → start=gate (no attach)
  - `clawker stop agent` → always gate (no attach involved)

**Open sub-questions:**
- What's the progress event schema? Reuse `tui.ProgressStep` shape in protobuf?
- Where's the line between "quick enough for unary" and "needs streaming"?
- Does `ContainerLogs` need to be server-streaming too (tail -f)?
- Does `ContainerStats` need to be server-streaming (live stats)?

### 3. clawkerd → CP
**Status: OPEN.** What does the container agent need to report to the control plane?
- Init progress (step started/completed/failed/cached)?
- Ready signal?
- Health/heartbeat?
- Process lifecycle events (main process exited, crashed)?
- Error reports?
- Resource usage / metrics?
- Peer messages (to other agents)?
- What about log forwarding — does clawkerd send logs or does CP read them via Docker logs API?
- Does clawkerd report on socket bridge status?
- What events from inside the container does the host actually care about?

### 4. CP → clawkerd
**Status: OPEN.** What does the control plane need to tell/ask the container agent?
- Init config/spec (see init orchestration decision below)?
- Graceful shutdown command?
- Config updates (credential rotation, env changes)?
- Credential events (new OAuth token, git credential refresh)?
- Exec commands (run something inside as root)?
- Peer messages (from other agents)?
- Health check pings?
- Does CP ever need clawkerd to gather info and report back (request/response pattern)?
- What about the socket bridge — does CP tell clawkerd to start/stop it?
- Future: task assignments, prompt injection for inter-agent workflows?

## Init Orchestration: clawkerd Owns Init (DECIDED — Option C, declarative)

**clawkerd is the init orchestrator, not a reporter and not a puppet.**

The entrypoint becomes minimal — start clawkerd and wait. clawkerd connects to CP, receives an init spec (declarative config), executes it, reports progress, then signals the entrypoint to proceed with gosu privilege drop.

**Why Option C (rejected alternatives):**
- Option A (entrypoint owns init, clawkerd reports after): Shortcut. clawkerd has no agency, can't grow. Future capabilities require more bash scripts. Makes future work harder.
- Option B (CP sends step-by-step commands): Imperative puppeteering. Tight coupling, single point of failure during init, chatty round-trips. If CP hiccups mid-init, container is stuck.
- **Option C (CP sends spec, clawkerd owns execution): Declarative, K8s model.** kubelet gets a PodSpec, not SSH commands. clawkerd is a capable agent that owns container-side operations. CP connection is required — if unreachable during init, clawkerd exits with a user-facing error (no silent degradation).

**Flow:**
```
entrypoint.sh (root):
  1. start clawkerd (background)
  2. wait for clawkerd "init complete" signal (file, socket, or exit code)
  3. exec gosu ${USERNAME} "$@"

clawkerd (root, background):
  1. start gRPC server on clawker-net (AgentCommandService)
  2. connect to CP's gRPC server
  3. Register(container_id, secret, version, listen_address) — auth + receive ClawkerdConfiguration
     [if CP unreachable: user-facing stderr error + exit 1 — fatal, no degradation]
     [if rejected: user-facing stderr error + exit 1]
     [if accepted: initialize logger from ClawkerdConfiguration, set project/agent context]
     [CP now knows clawkerd's gRPC address, connects back]
  4. CP calls clawkerd's RunInit(spec) — server-streaming RPC on clawkerd's server
     (CP unreachable = fatal exit, no fallback — see lessons learned)
  5. clawkerd executes init steps, streams progress back on RunInit response:
       ← step: firewall started
       ← step: firewall completed
       ← step: git config started
       ← step: git config completed
       ← ready
       [stream completes]
  6. signal entrypoint "init complete"
  7. continue running — AgentCommandService accepts future RPCs from CP
```

**Progress belongs to the operation (DECIDED):** No generic ReportProgress() or ReportReady() RPCs. Progress is part of the operation's response stream. RunInit() is server-streaming — clawkerd streams init progress back to CP as part of the RunInit response. Ready is just the final message before the stream closes. Future operations follow the same pattern: RunFoo() streams foo progress. Consistent with CLI→CP pattern (CreateContainer streams creation progress).

**Unsolicited events (not tied to a CP-initiated operation):** Specific RPCs on AgentReportingService — Heartbeat(), ReportProcessExit(), etc. Still discrete, still operation-specific. No generic catch-all.

**Separation of concerns (DECIDED):** Register is auth + operational config delivery. RunInit is init + its progress. Each operation is its own RPC with its own progress stream. Containerd shim v2 pattern.

**Register delivers ClawkerdConfiguration (DECIDED):** Every reference implementation studied delivers operational config during registration:
- **OpAMP**: Server responds with `TelemetryConnectionSettings` (OTEL endpoints for logs/metrics/traces), `AgentRemoteConfig`, `AgentIdentification`
- **containerd shim v2**: `CreateTaskRequest` includes typed `options` field for shim-specific config
- **kubelet**: Gets `KubeletConfiguration` from API server (operational params, endpoints, feature gates)
- **Telepresence**: `ArriveAsAgent()` → `SessionInfo`, then `GetAgentConfig` with `LogLevel`, `ManagerHost`, `ManagerPort`

`RegisterResponse` includes a `ClawkerdConfiguration` message — everything clawkerd needs to operate. This includes identity (project, agent names for log context), OTEL endpoint/settings, and file logging config. The CP resolves all config from its `config.NewConfig()` gateway and translates to proto fields. **clawkerd does NOT import `internal/config`** — it stays thin (gRPC + logger only). Config resolution is the CP's job.

Note: `ClawkerdConfiguration`, NOT `AgentConfig` — "agent" in our domain means a Claude Code instance, not clawkerd. clawkerd is the system daemon.

**Benefits for future work:**
- Init logic in Go (testable, maintainable) not bash scripts
- CP can customize init per-container without rebuilding images
- clawkerd naturally takes on more responsibilities over time (health, process mgmt, socket bridge, peer messaging)
- Graceful degradation — container still boots with defaults if CP is down
- Progress reporting is natural — clawkerd is doing the work AND reporting

### Migration Summary

**Migrates to CP (control path):** 45+ distinct method calls across container lifecycle, queries, images, volumes, networks. The entire `CreateContainer()` orchestration (workspace setup, env injection, config volumes, network ensure) moves to CP.

**Stays on CLI (data path):** 7 methods — `ContainerAttach`, `ContainerResize`, `ExecCreate/Start/Attach/Resize/Inspect` + `PTYHandler`. These are the sacred stream operations.

**Key observation:** `FindContainerByName` has 18 call sites — every container subcommand uses it. This suggests the CP should handle name resolution server-side (CLI sends agent name, CP resolves to container ID).

### Config Flow Pattern (DECIDED — CRI pattern)

**CLI sends clawker domain config, CP translates to Docker types internally.** Same pattern as CRI: kubelet sends PodSpec, CRI runtime translates to container configs.

Research confirmed: CRI defines its own protobuf types (does NOT pass through Docker structs as bytes). containerd uses `google.protobuf.Any` for shim-specific typed protobuf messages (NOT raw JSON). Neither uses raw JSON bytes.

This means:
- `CreateContainer()` logic from `internal/cmd/container/shared/container.go` migrates to CP
- CLI becomes a thin gRPC client sending clawker config
- CP owns all Docker API interaction (container.Config, HostConfig, NetworkingConfig)
- Clean separation: CLI knows clawker, CP knows Docker, clawkerd knows container internals

### Schema Design (POC IMPLEMENTED — expanding)

**Implemented in POC:**
- `AgentReportingService.Register()` — unary RPC for auth (clawkerd → CP)
- `AgentCommandService.RunInit()` — server-streaming RPC for init (CP → clawkerd)
- `InitStep` message: name + bash command
- `RunInitResponse` stream: step_name + status enum (STARTED/COMPLETED/FAILED/READY) + output/error
- Protobuf at `internal/clawkerd/protocol/v1/agent.proto`, generated via `buf generate`

**Implemented (logger wiring):**
- `RegisterResponse` extended with `ClawkerdConfiguration` (identity, OTEL config, file logging config)
- CP populates `ClawkerdConfiguration` from resolved `config.NewConfig().Settings`
- clawkerd initializes `internal/logger` from CP-delivered config (no `internal/config` import)
- `OtelCollectorInternalEndpoint()` added to `MonitoringConfig` (bug fix — was missing host:port without scheme for container-side)
- All CP and clawkerd logger calls include `Str("component", ...)` for Loki filtering
- clawkerd uses `/var/log/clawkerd/` for file logs (Linux FHS, hardcoded — no config import)
- Pre-registration failures use `fatalf()` → stderr; post-registration uses structured logger

**Implemented (OTEL pipeline + CP dashboard):**
- `ServiceName` field on `logger.Options` — OTEL `service.name` resource attribute → `service_name="clawker"` in Loki
  - Uses `resource.NewSchemaless()` to avoid SchemaURL conflicts with `resource.Default()` during merge
- `ScopeName` field on `logger.Options` — OTEL instrumentation scope name → `scope_name` structured metadata in Loki
  - Host-side: `scope_name="clawker"` (default) — CP logs
  - Container-side clawkerd: `scope_name="clawkerd"` — agent daemon logs
- **Zerolog OTEL limitation**: zerolog's Hook interface (`Run(e *Event, level Level, msg string)`) provides write-only Event — otelzerolog bridge cannot read zerolog fields (`Str()`, `Int()`). Fields only appear in file writer JSON. Use `scope_name` for component filtering.
- Control Plane Grafana dashboard at `internal/monitor/templates/grafana-cp-dashboard.json`
  - Row 1: CP logs (`| scope_name = \`clawker\``)
  - Row 2: clawkerd logs (`| scope_name = \`clawkerd\``)
  - Template variables: `$lokidatasource`, `$project`, `$agent`
- Dashboard wired into `monitor init` (file write) and `compose.yaml.tmpl` (Grafana volume mount)
- Test Dockerfile fixed: copies `internal/logger/` (new clawkerd dependency)
- Test OTEL config: export interval 1s + 2s sleep before cleanup for container batch flush

**Still needed (next phase — SchedulerService):**
- SchedulerService RPCs (CLI → CP) — container lifecycle, image/volume/network management (see "Next Steps" section)
- Additional AgentCommandService RPCs (Shutdown, ExecCommand, etc.)
- Additional AgentReportingService RPCs (Heartbeat, ProcessExit, etc.)
- Proto message design: CLI sends clawker domain fields (NOT Docker types) in request messages
- CP owns Docker translation internally (CRI pattern)
- Progress event protobuf schema for streaming RPCs

## Security Model (DECIDED)

Threat model: local dev tool. Cheap wins only.

**clawkerd authentication:**
1. **Shared secret** — control plane generates token on startup, injected as container env var
2. **Pre-declared container ID** — CLI declares intent before creation, CP verifies against registry

**CLI → CP:** No auth. Localhost gRPC. If attacker has localhost access, user has bigger problems.

## In-Memory Message Queue (DECIDED)

**Watermill GoChannel** (`github.com/ThreeDotsLabs/watermill`) from day one. No raw Go channels, no "evolve later" shortcuts.

- Embedded in-process pub/sub — no external service (no Redis, no NATS)
- Topic-based routing: `topic = containerID` for point-to-point, broadcast topics for fan-out
- In-memory only mode (`Persistent: false`)
- Ack/Nack support for reliable delivery
- Clean `Publisher`/`Subscriber` interfaces

**Why now, not later:** Peer communication (inter-container message exchange through the control plane) is a known future requirement. Building on raw Go channels would create refactoring debt. Watermill's abstraction is right for the domain and serves the project long-term.

**Use cases:**
- CP → agent command routing (via agent's container ID topic)
- Agent → agent peer messaging (routed through CP hub)
- Future: broadcast notifications, fan-out events

## CLI ↔ Control Plane Lifecycle Protocol (DECIDED)

**Scope: Containers only.** No database — ephemeral state, reconstructable from Docker events on restart.

**Startup:** CLI calls `EnsureControlPlane()` — PID file + health check. Auto-starts if needed. No races.

**Pre-creation handshake:**
1. CLI declares intent → CP
2. CP starts timer, expects Docker `create` event within window
3. CLI does docker create/start (via CP's SchedulerService)
4. CP sees Docker events, matches to declared intent

**Shutdown:** Last container exits + grace period.

## Package Layout (DECIDED — validated by POC)

```
internal/
  clawkerd/                  ← protocol package (shared by both sides)
    protocol/
      v1/
        agent.proto          ← single source of truth (protobuf schema)
        agent.pb.go          ← generated, committed to repo
        agent_grpc.pb.go     ← generated, committed to repo
  controlplane/              ← host-side control plane server
    server.go                ← gRPC server, Register handler, RunInit orchestration
    registry.go              ← agent connection registry (thread-safe)
clawkerd/
  main.go                    ← container-side agent binary (gRPC server + client)
test/
  controlplane/              ← integration tests (living POC)
    controlplane_test.go     ← E2E: build, register, init, privilege verify
    main_test.go             ← test harness setup
    testdata/
      Dockerfile             ← two-stage build, su-exec, tini
      entrypoint.sh          ← clawkerd &, su-exec drop
```

Single Go module. No go.work, no replace directives. `clawkerd/main.go` imports `internal/clawkerd/protocol/v1`.

## Process Management (DECIDED)

- `HostConfig.Init = true` always (hardcoded, remove --init flag)
- tini as PID 1 (injected by Docker)
- Entrypoint runs as root (Dockerfile pattern: postgres, redis)
- clawkerd started as background process in entrypoint
- gosu/su-exec drops to claude user for main process
- Eliminates all sudoers entries

## Key Code Paths (reference)

- `internal/cmd/container/shared/container.go:309` — `--init` flag (to be removed)
- `internal/cmd/container/shared/container.go:789-792` — `HostConfig.Init` assignment
- `internal/cmd/container/shared/container.go:1482-1674` — `CreateContainer()` (to be refactored: delegates to CP)
- `internal/bundler/assets/Dockerfile.tmpl:260` — `USER ${USERNAME}` (to be repositioned for root entrypoint)
- `internal/bundler/assets/Dockerfile.tmpl:376` — `ENTRYPOINT ["entrypoint.sh"]`
- `internal/bundler/assets/Dockerfile.tmpl:242-247` — firewall sudoers (to be removed)
- `internal/bundler/assets/entrypoint.sh` — rework for root + gosu drop + clawkerd start
- `internal/bundler/dockerfile.go:335-469` — build context assembly (add clawkerd files)
- `internal/hostproxy/server.go` — evolves into control plane
- `internal/hostproxy/daemon.go` — daemon lifecycle (evolves into control plane daemon)
- `internal/cmd/container/run/run.go:261-414` — attachThenStart (CLI still attaches direct to Docker)

## Lessons Learned

- Docker CLI NEVER allocates TTY without explicit `-t` — trust contract
- Docker merges stdout+stderr in TTY mode, uses stdcopy multiplexing in non-TTY mode
- Docker's `HostConfig.Init` injects tini as PID 1
- Docker Events API is persistent streaming (chunked HTTP + JSON), supports label filtering
- gosu/su-exec only step DOWN (root→user), cannot step UP
- Official Docker images (postgres, redis) run entrypoint as root and use gosu to drop privileges
- containerd already exists on host as part of Docker Engine — no replacement needed
- containerd shims run on the HOST, clawkerd runs INSIDE the container — inverted direction
- kubelet's PLEG started as polling (1s interval), later added event-push — same evolution as our polling→Docker Events transition
- Don't take the easy way out. Build it right.
- Don't over-abstract. A domain-specific protocol beats a generic messaging framework.
- Port binding (host port mapping) is needed on macOS/Docker Desktop for host→container gRPC. CP's resolveAgentAddress() handles both port mapping and direct IP fallback.
- Container ID from os.Hostname() is 12-char truncated, Docker API uses full ID — need consistent handling.
- su-exec works well on Alpine (~10KB, native). gosu vs su-exec decision: su-exec for Alpine, gosu for Debian, or just su-exec everywhere (it compiles on Debian too).
- The POC entrypoint is 3 lines of bash — all complexity lives in Go. This validates the "init logic in Go, not bash" principle.
- `go s.runInitOnAgent()` goroutine lifecycle needs structured management (errgroup, cancellation) for production.
- buf + protoc-gen-go + protoc-gen-go-grpc toolchain works cleanly. `make proto` target added.
- **"Agent" means Claude Code instance, NOT clawkerd.** clawkerd is the system daemon. Proto messages use `ClawkerdConfiguration`, not `AgentConfig`. This naming distinction is non-negotiable.
- **Don't pass config via env vars when you have a gRPC channel.** The whole point of the CP architecture is that clawkerd phones home and gets everything it needs. Register delivers `ClawkerdConfiguration` — identity, OTEL config, logging config. clawkerd stays thin (no `internal/config` import).
- **CP connection is fatally important during initialization.** If clawkerd cannot reach the control plane during init, it's a lost cause. No graceful degradation, no retries-forever, no limping along. Exit code 1 with a **user-facing** stderr message — not a developer stack trace. The user sees this in `docker logs` and needs to understand what went wrong and what to do about it. Example: `[clawkerd] fatal: could not connect to control plane at host.docker.internal:9090 — is the control plane running? (clawker monitor status)`. **This is init-time only.** Runtime CP disconnection (e.g. 20 minutes into a session) must NOT crash clawkerd or disrupt the user's running session. clawkerd should handle runtime disconnection gracefully (buffer events, retry, degrade silently). A fallback failsafe for runtime resilience will be added as the system matures — not needed for POC.
- **`MonitoringConfig` was missing `OtelCollectorInternalEndpoint()`** — had `OtelCollectorEndpoint()` (host-side, `host:port`) but `OtelCollectorInternalURL()` returns full URL with scheme, which `otlploghttp.WithEndpoint()` doesn't accept. Bug from a previous agent that didn't properly handle internal vs external endpoints.

## POC Iteration 1: Register + RunInit + OTEL (VALIDATED)

**Goal:** Validate the two-gRPC-server architecture with a minimal end-to-end flow, including structured logging via OTEL pipeline.

**What was built** (commits `7599de7` → `3c8e00c`, ~2800 lines):

| Component | Location | Role |
|-----------|----------|------|
| Proto schema | `internal/clawkerd/protocol/v1/agent.proto` | AgentReportingService (Register) + AgentCommandService (RunInit) + ClawkerdConfiguration |
| clawkerd binary | `clawkerd/main.go` | Container-side agent — gRPC server, registers with CP, executes init steps, structured logging |
| Control plane | `internal/controlplane/server.go` + `registry.go` | Accepts registration, resolves container IP, connects back, calls RunInit, delivers config |
| Logger | `internal/logger/logger.go` | File + OTEL dual-destination logging, `ServiceName` + `ScopeName` fields |
| CP Dashboard | `internal/monitor/templates/grafana-cp-dashboard.json` | Grafana dashboard for CP + clawkerd logs (scope_name filtering) |
| Test Dockerfile | `test/controlplane/testdata/Dockerfile` | Two-stage build (Go builder → Alpine), su-exec + tini, root entrypoint |
| Test entrypoint | `test/controlplane/testdata/entrypoint.sh` | Starts clawkerd &, drops to claude via su-exec |
| Integration test | `test/controlplane/controlplane_test.go` | Full E2E: build, register, init, privilege verify, OTEL log verification |
| Harness extensions | `test/harness/client.go` | WithNetwork(), WithPortBinding() |
| Build infra | `Makefile`, `buf.yaml`, `buf.gen.yaml` | `make proto`, `make test-controlplane` |

### Validated Outcomes

1. **Two-server gRPC pattern across Docker network:** YES. CP on host, clawkerd in container, communicating via port mapping.
2. **Address discovery (Register → Docker inspect → connect back):** YES. CP resolves container IP via `ContainerInspect`, checks port bindings first (macOS), falls back to container network IP (Linux).
3. **Server-streaming RunInit progress:** YES. STARTED → COMPLETED per step → READY flows correctly.
4. **Root entrypoint + privilege drop:** YES. clawkerd runs as root (UID 0), main process as claude (UID 1001) via su-exec.
5. **tini + clawkerd + main process:** YES. tini as PID 1, manages both processes.
6. **CP-delivered config (ClawkerdConfiguration):** YES. Register response delivers identity (project, agent), OTEL config, file logging config. clawkerd initializes structured logger from it.
7. **End-to-end OTEL pipeline:** YES. Both CP and clawkerd logs flow through OTEL collector to Loki with `service_name="clawker"`. CP logs have `scope_name="clawker"`, clawkerd logs have `scope_name="clawkerd"`.
8. **Grafana dashboard:** YES. Control Plane dashboard shows CP and clawkerd logs in separate panels, filterable by project and agent template variables.
9. **Latency/connectivity:** No issues observed.
10. **Dockerfile builder stages:** Clean two-stage build works. CGO_ENABLED=0 for static binary.

### Key Technical Discoveries

- **OTEL resource.Merge SchemaURL conflict:** `resource.NewWithAttributes(semconv.SchemaURL, ...)` silently fails when merged with `resource.Default()` because they use different semconv versions. Fix: `resource.NewSchemaless()`.
- **Zerolog OTEL bridge limitation:** zerolog's `Hook` interface provides a write-only `Event`. The otelzerolog bridge cannot read accumulated fields (`Str()`, `Int()`, etc.) — they only appear in file JSON. Use `scope_name` (OTEL instrumentation scope) for component filtering, not zerolog fields.
- **Loki scope_name is structured metadata, not a stream label.** Must use pipeline syntax `| scope_name = \`value\`` not stream selector `{scope_name="value"}`.
- **Container OTEL batch flush timing:** Default export interval is 5s via settings, but containers in tests only live ~1s. BatchProcessor never flushes before SIGKILL. Fix: shorter export interval in tests + brief sleep before container cleanup.
- Container ID from `os.Hostname()` is 12-char truncated — need consistent handling across Docker API (full ID) and in-container (truncated).
- `host.docker.internal:host-gateway` needed for container→host communication.
- su-exec chosen over gosu for POC (Alpine native, ~10KB).
- Ready file at `/var/run/clawker/ready` works as signal mechanism.
- Init step failures are non-fatal — clawkerd sends FAILED event and continues.

### NOT validated (deferred to next phases)

- HostConfig.Init=true (POC uses explicit tini in ENTRYPOINT)
- Runtime CP disconnection resilience
- Reconnection logic (gRPC stream drops)
- Docker Events integration
- Watermill message queue
- SchedulerService (CLI → CP) — **NEXT PHASE**
- Entrypoint blocking on clawkerd ready signal (POC is fire-and-forget)

## POC Iteration 2 (Next): SchedulerService — Container Lifecycle Management

**Goal:** Design and implement the SchedulerService gRPC service on the control plane. This is what the CLI calls to manage containers instead of talking to Docker directly.

### Phase 1: Core Container Lifecycle RPCs

Based on the Docker API inventory above, prioritize the most critical container lifecycle operations:

1. **`CreateContainer`** (server-streaming) — The big one. Migrates the entire `CreateContainer()` orchestration from `internal/cmd/container/shared/container.go`. CLI sends clawker domain config (project, agent, workspace mode, etc.), CP translates to Docker types, creates volumes/networks/container, streams progress.

2. **`StartContainer`** (server-streaming) — Starts container, waits for clawkerd registration + RunInit completion, streams progress (init steps, ready signal). The CLI's `StartContainer` stream = CP's Docker Events + clawkerd's RunInit stream merged.

3. **`StopContainer`** (server-streaming) — Sends SIGTERM, waits for graceful shutdown, streams progress.

4. **`RemoveContainer`** (server-streaming) — Stops if running, removes container + associated volumes, streams progress.

5. **`ListContainers`** (unary) — Returns container list with clawkerd state enrichment (init status, ready state, connected/disconnected).

6. **`InspectContainer`** (unary) — Returns container details with clawkerd state overlay.

7. **`FindContainerByName`** (unary) — Name resolution server-side (CLI sends agent name, CP resolves to container ID). 18 call sites depend on this.

### Phase 2: Image + Volume + Network RPCs

8. **`BuildImage`** (server-streaming) — Image build with progress streaming.
9. **`ListImages`** / **`RemoveImage`** / **`PruneImages`** (unary/streaming)
10. **Volume CRUD** — `CreateVolume`, `ListVolumes`, `RemoveVolume`, `PruneVolumes`
11. **Network CRUD** — `CreateNetwork`, `ListNetworks`, `RemoveNetwork`, `PruneNetworks`

### Phase 3: Advanced Operations

12. **`KillContainer`**, **`PauseContainer`**, **`UnpauseContainer`**, **`RestartContainer`**
13. **`RenameContainer`**, **`UpdateContainer`**
14. **`ContainerLogs`** (server-streaming for tail -f)
15. **`ContainerStats`** (server-streaming for live stats)
16. **`ContainerTop`**, **`ContainerWait`**
17. **`CopyToContainer`** / **`CopyFromContainer`**

### Design Principles (from brainstorm decisions)

- **CLI sends clawker domain config, CP translates to Docker types** (CRI pattern)
- **Every mutating RPC is server-streaming** (progress events for TUI rendering)
- **Query RPCs are unary** (request/response)
- **Gate vs Diagnostic stream mode** depends on whether CLI has active Docker attach
- **Progress event schema** should map to existing `tui.ProgressStep` shape

### Open Design Questions for SchedulerService

- Proto message design for `CreateContainerRequest` — what clawker domain fields does CLI send?
- How does CP relay clawkerd's RunInit stream into CLI's StartContainer stream?
- Progress event protobuf schema — reuse `tui.ProgressStep` shape or new?
- Where's the line between "quick enough for unary" and "needs streaming"?
- Does `ContainerLogs` need to be server-streaming (tail -f)?
- Does `ContainerStats` need to be server-streaming (live stats)?
- How does `FindContainerByName` work with the CP's agent registry vs Docker API?


## Plan File

Previous POC plan completed. Next plan to be created for SchedulerService implementation.

---

**IMPERATIVE**: Always check with the user before starting each step. If all work is done, ask if they want to delete this memory.
