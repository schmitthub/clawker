# Brainstorm: The Control Plane and clawkerd

> **Status:** Active
> **Created:** 2026-02-16 (POC phase)
> **Last major rewrite:** 2026-04-11 (primitive CP + terminal-state vision)

## Evolution note

This file originated 2026-02-16 as a scratchpad for the two-gRPC-server POC
(clawkerd ↔ CP, validated in `test/controlplane/`). It has been rewritten as
of 2026-04-11 to reflect where the design has actually landed:

- The primitive clawker CP (v1, in flight) ships the **final auth shape**
  from day 1 — mTLS + embedded OIDC provider + JWT bearer + per-method
  scope enforcement. No throwaway auth gets built.
- The CP has been reframed from "a registration endpoint for clawkerd" to
  **the authoritative daemon for all clawker state on the machine**.
- The firewall is a **subsystem of the CP**, not its parent. Monitoring,
  hostproxy, socketbridge, agent lifecycle, and image management are
  peers of the firewall inside the CP.
- A "k8s-lite" mental model emerged: CLI is kubectl + a local
  image-builder/container-runner; CP is api-server + controller-manager +
  authoritative state store. Not 1:1 but close enough for intuition.
- BPF-layer isolation of managed containers from the CP was dropped — it
  can't work (clawkerd will run *inside* managed containers as a
  legitimate caller, same cgroup as any rogue process we'd try to block).
  The only defensible layer for distinguishing legitimate from rogue is
  application-layer crypto auth, which is exactly what the JWT + scope
  check does.

POC context (2026-02-16) preserved at the bottom under "## POC Origins" for
historical lineage. Most of the POC's open questions now have answers; the
remaining ones block specifically on clawkerd-in-managed-agents (phase 5+).

---

## Current architecture

### The CP is the daemon

The clawker control plane (`clawker-cp`) is a containerized, privileged,
long-lived Go daemon. It is **not** "a component that does firewall stuff."
It is **the clawker daemon for the machine**, responsible for:

- Authentication and authorization (mTLS + OIDC + JWT + scopes)
- Subsystem orchestration (firewall, monitoring, hostproxy, socketbridge, ...)
- Agent registration and post-handoff lifecycle tracking
- Docker API event subscription + reconciliation
- Audit logging (subsystem-agnostic)
- State management across all subsystems

v1 ships the primitive: just the firewall subsystem wired up to the full
auth stack. Everything else is phase-2+.

### Subsystems

Each subsystem is a self-contained tenant inside the CP:

| Subsystem | Scope | Status |
|---|---|---|
| **firewall** | Envoy + CoreDNS + eBPF + MITM CA + rules store + clawker-net | v1: scaffolding in place, fold-in starts phase 2 |
| **monitor** | Grafana + Prometheus + Loki + OTEL collector | phase 9 |
| **hostproxy** | Host-proxy binary lifecycle (process stays on host) | phase 10 |
| **socketbridge** | SSH/GPG agent forwarding sessions | phase 10 |
| **agents** | Managed agent lifecycle tracking (post-register) | phase 4–5 |
| **auth** | CP's own auth layer (CA, OIDC signing key, scopes) | v1 ✅ |

Subsystems are mutually independent. Each has its own namespaced state
directory. Deleting one does not affect others. Adding a new one follows
the same `Subsystem` interface pattern (EnsureRunning, Stop, Status,
RegisterGRPC).

### CLI ↔ CP split (k8s-lite)

| Concern | Owner | Why |
|---|---|---|
| Parse `clawker.yaml` / walk-up CWD resolution | **CLI** | CP has no concept of "where the user is" |
| Parse `settings.yaml` | **Both** | CLI writes (via `clawker settings edit`), CP reads via RO bind mount |
| Build agent images (whail + buildkit) | **CLI** | Image build needs workspace bind mount from host FS |
| Create / start / attach **managed agent** containers | **CLI** | Terminal attachment + workspace + per-project context are host-local |
| Firewall stack containers (Envoy, CoreDNS, ebpf) | **CP** | Long-lived, single-owner, authoritative for BPF |
| Monitoring stack containers | **CP** | Same orchestration pattern |
| Host-proxy **lifecycle** | **CP (managed), host (runs)** | RPC start/stop; binary must run on host to bridge |
| Socket bridge sessions | **CP** | `docker exec` into target agent works from inside the CP |
| Rules store (`egress-rules.yaml`) | **CP** | Eliminates dual-writer race; single source of truth |
| Daemon / health-watcher loops | **CP** | In-process goroutines replace PID-file daemon |
| clawker-net creation | **CP** | Managed as firewall-subsystem infra |
| Post-create agent registration | **CLI → CP via gRPC** | Handoff; see "Agent lifecycle" below |
| Reconciliation when CLI crashes mid-create | **CP via Docker events** | Resilience path |

**CLI** = `kubectl` + local-builder (user agent orchestration stays host-local).
**CP** = `api-server` + `controller-manager` (infra, registry, reconciliation).

The CLI keeps direct Docker API access for user agents; the CP has direct
Docker API access for infra containers. **Two Docker clients to the same
daemon, distinguished by labels** (`dev.clawker.managed=agent` vs
`dev.clawker.purpose=firewall`). This is already the convention today;
the CP-side watcher must respect it to avoid trying to manage user agents
as infra or vice versa.

### Directory layout (XDG-based, no separate "CP dir")

The CP's state **is** clawker state. There is no separate "CP data
directory" — the CP container bind-mounts the existing clawker XDG dirs
and consumes them. The on-host layout is unchanged; the CP just mounts it
into the container at FHS-standard paths.

| Host (XDG) | Container (FHS) | Mode |
|---|---|---|
| `~/.local/share/clawker/` | `/var/lib/clawker/` | **RW** |
| `~/.local/state/clawker/` | `/var/run/clawker/` | **RW** |
| `~/.cache/clawker/` | `/var/cache/clawker/` | **RW** |
| `~/.config/clawker/` | `/etc/clawker/` | **RO** |

The RO mount on `~/.config/clawker/` is a **real security property**, not
cosmetic: the CP is structurally prevented (at kernel mount layer) from
mutating user config. Settings changes happen only via the CLI (which has
the file writable). The CP picks up changes on its next read; no explicit
push RPC in v1.

Inside the data dir, organization is **by subsystem namespace** — same
pattern already used today:

```
~/.local/share/clawker/
├── firewall/                  # SUBSYSTEM: TLS inspection + egress enforcement
│   ├── ca/                    # MITM CA (Envoy TLS inspection — DISTINCT from auth CA)
│   │   ├── ca-cert.pem
│   │   └── ca-key.pem
│   ├── egress-rules.yaml      # rules store
│   ├── envoy/
│   │   └── envoy.yaml         # generated config
│   └── coredns/
│       └── Corefile           # generated config
├── monitor/                   # SUBSYSTEM: monitoring stack state
│   ├── grafana/
│   ├── prometheus/
│   └── loki/
├── auth/                      # CP-LEVEL: auth material (NOT a subsystem — CP core)
│   ├── ca/
│   │   ├── ca.pem             # identity root — signs mTLS certs
│   │   └── ca.key
│   ├── oidc/
│   │   └── signing.key        # JWT signing key (RS256)
│   └── certs/
│       ├── server.pem         # TLS server cert (gRPC + OIDC listeners)
│       ├── server.key
│       └── cli/
│           ├── cert.pem       # CLI's mTLS client cert
│           └── key.pem
└── agents/                    # CP-LEVEL: post-register agent tracking
    └── <agent-id>/
        └── ...                # per-agent state; cert lives in clawkerd memory
```

State dir (transient runtime):

```
~/.local/state/clawker/
├── sockets/
│   ├── grpc.sock              # gRPC UDS
│   └── oidc.sock              # OIDC HTTP UDS
├── ready                      # readiness marker
└── audit/
    └── audit.log              # rotatable, append-only
```

Cache dir (regenerable — wipe at will):

```
~/.cache/clawker/
└── firewall/
    └── certs/                 # per-domain MITM certs (regen from CA + rules)
```

Config dir (RO into CP):

```
~/.config/clawker/
├── settings.yaml              # CLI writes, CP reads
└── projects.yaml              # CLI writes, CP reads
```

**Invariant**: subsystems write only to their own namespaced subdirs
under `<DataDir>/<subsystem>/`. The CP's auth layer (`auth/`) never
touches subsystem dirs; subsystems never reach into `auth/`. This is the
filesystem analog of the package/responsibility separation.

**Open naming question**: the auth namespace is tentatively `auth/`.
Alternatives considered: `identity/`, `pki/`, `tls/`. `tls/` is rejected
because it collides with firewall TLS-inspection concepts. `auth/` is
the current default; user to confirm.

---

## Auth (final shape from v1)

### Three-layer defense

1. **mTLS** — every gRPC connection authenticates the channel (peer cert
   signed by the auth CA)
2. **JWT bearer** — every RPC call carries a short-lived access token
   (RS256 signed by OIDC signing key, 5-min TTL)
3. **Per-method scope enforcement** — static `methodScopes` map, fail-closed
   on unmapped methods

**Cross-layer check**: mTLS peer CN is verified against JWT `sub`. This
is the stolen-cert + stolen-token defense — compromising one layer alone
is insufficient.

### Two CAs (load-bearing distinction)

| CA | File | Purpose | Rotation |
|---|---|---|---|
| **auth CA** | `~/.local/share/clawker/auth/ca/ca.pem` | Identity root. Signs mTLS certs for authenticating gRPC peers (CLI, agents, future callers). | Annual-ish; requires restarting all auth peers |
| **firewall MITM CA** | `~/.local/share/clawker/firewall/ca/ca-cert.pem` | Envoy TLS inspection. Signs per-domain server certs Envoy presents during HTTPS interception. | `clawker firewall rotate-ca`; independent |

These are **not the same** and never cross-reference. Compromising one
does not cascade. Rotation schedules are independent. Documented in
`internal/controlplane/CLAUDE.md` (or should be) as the load-bearing fact
nobody should muddle.

### Scope naming

Scopes follow `<subsystem>:<action>` pattern:

- `firewall:admin`, `firewall:enable`, `firewall:rotate-ca`
- `monitor:admin`, `monitor:status`
- `agent:announce`, `agent:self:report`, `agent:exec`
- `cp:health`, `cp:audit:read`

JWT claims carry scopes. Interceptor matches the called method's declared
scope against the token's scopes. Unknown method → `PermissionDenied`
(fail-closed, prevents "forgot to add scope" privilege holes).

### Client registry

v1: one client, `clawker-cli`. Future clients are added as new
`ClientRegistration` entries in `oidc_clients.go`. The auth stack doesn't
change when callers are added — registration grows.

Planned future clients:

- **`clawkerd-<agent-id>`** — per-agent registration, minted via PKCE
  handshake (phase 4+). Scopes: `agent:self:*`, maybe `peer:discover`.
- **`clawker-webui`** — browser-delivered, uses `authorization_code` +
  PKCE flow against the CP's OIDC provider. Requires mounting the
  `/authorize` handler (currently not mounted in v1). Scopes: `cp:*:read`.

### Proto service split (phase 2)

v1's `ControlPlaneService` contains only firewall methods. This is a v1
beachhead — the name is aspirational, the content is one subsystem. In
phase 2 this splits into per-subsystem services on the same gRPC listener:

```proto
service ControlPlaneService {          // CP-level concerns
  rpc Health(...);
  rpc Version(...);
  rpc Status(...);                     // aggregates subsystem statuses
  rpc AnnounceAgent(...);
  rpc Register(...);                   // from clawkerd
}

service FirewallService {              // firewall subsystem
  rpc EnableContainerFirewall(...);
  rpc DisableContainerFirewall(...);
  rpc BypassContainer(...);
  rpc SyncRoutes(...);
  rpc AddRules(...);
  rpc RemoveRules(...);
  rpc ListRules(...);
  rpc ResolveHostname(...);
  rpc RotateCA(...);                   // MITM CA, NOT auth CA
}

service MonitorService { ... }
service AgentService { ... }
service HostProxyService { ... }
service SocketBridgeService { ... }
```

All served on the same gRPC listener, same auth interceptor, same auth
material. The split is organizational — it mirrors subsystem boundaries
and makes the method namespace self-organizing
(`/clawker.agent.v1.FirewallService/AddRules`).

---

## Agent lifecycle

### Pre-announcement + PKCE handshake

Trust flow for a new managed agent:

```
CLI                         CP                          clawkerd (not yet alive)
 │                           │                              │
 │─AnnounceAgent({           │                              │
 │   agent_id,               │                              │
 │   code_challenge,  ← S256 │                              │
 │   code_challenge_method,  │                              │
 │   project_snapshot,       │                              │
 │   init_spec               │                              │
 │ })───────────────────────▶│                              │
 │                           │ [create pending slot         │
 │                           │  keyed by agent_id;          │
 │                           │  challenge stored;           │
 │                           │  TTL 60s]                    │
 │◀────ok─────────────────── │                              │
 │                           │                              │
 │ [write code_verifier +    │                              │
 │  ca.pem +                 │                              │
 │  cp-address               │                              │
 │  to container bind mount] │                              │
 │ [docker create + start]   │                              │
 │                           │                              │
 │                           │                              │ [clawkerd boots]
 │                           │                              │ [reads verifier]
 │                           │                              │ [dials CP,
 │                           │                              │  one-way TLS]
 │                           │                              │
 │                           │◀──Register({agent_id,        │
 │                           │     code_verifier,           │
 │                           │     listen_addr,             │
 │                           │     version})────────────────│
 │                           │                              │
 │                           │ [Consume(agent_id, verifier):│
 │                           │   - compute SHA256           │
 │                           │   - compare to slot          │
 │                           │     challenge (const-time)   │
 │                           │   - atomic delete]           │
 │                           │                              │
 │                           │ [mint per-agent cert         │
 │                           │  signed by auth CA,          │
 │                           │  CN = agent_id]              │
 │                           │                              │
 │                           │──RegisterResponse({          │
 │                           │    agent_cert,               │
 │                           │    agent_key,                │
 │                           │    init_spec})──────────────▶│
 │                           │                              │
 │                           │                              │ [delete verifier]
 │                           │                              │ [reconnect w/ mTLS]
 │                           │                              │
 │                           │◀═══(mTLS + JWT channel)═════ │
```

Properties:

- **CLI is the root of trust** for agent existence. No agent exists
  without a CLI announcement.
- **PKCE splits the secret across two paths**. `code_verifier` travels
  filesystem (CLI → container bind mount). `code_challenge` travels
  network (CLI → CP via mTLS gRPC). Single-channel compromise is
  insufficient to impersonate.
- **Slot consumption IS the nonce**. Atomic `delete` on successful
  Register eliminates replay. No separate nonce field needed — the slot
  record's existence and single-use consumption is the replay defense.
- **Long-lived credential never on disk**. The per-agent cert lives only
  in clawkerd's process memory after Register. Only the short-lived
  verifier ever touches the filesystem, and clawkerd deletes it
  immediately after reading.
- **S256-only**. No algorithm negotiation, no `plain` method, no
  downgrade path. Clawker is a closed system with one CLI implementation;
  there's no legacy to accommodate.
- **CP slot store is non-secret**. It contains only
  `SHA256(verifier)`-format challenges, which are computationally useless
  on their own. The slot store can be logged, metriced, core-dumped,
  backed up without credential leak. This is PKCE's defense-in-depth
  property against CP memory/storage compromise.

### PKCE slot data shape

```go
type PendingSlot struct {
    AgentID     string        // CLI-chosen logical identity
    Challenge   string        // BASE64URL(SHA256(verifier))
    Method      string        // "S256" — hardcoded, no negotiation
    AnnouncedAt time.Time
    ExpiresAt   time.Time     // announce + 60s
    ProjectSnapshot ...       // CP-relevant subset of clawker.yaml
    InitSpec    ...           // what clawkerd runs after registration
}

// Consume is the one-shot operation. Matches by agent_id, validates
// via constant-time challenge compare, atomically deletes on success.
func (r *SlotRegistry) Consume(agentID, verifier string) (*PendingSlot, error)
```

### CLI / agent bootstrap asymmetry (intentional, structural)

CLI and agents have **different trust origins** and **different bootstrap
mechanisms**. This is not an oversight — it reflects genuine differences.

| Aspect | CLI | Agent |
|---|---|---|
| Trust origin | Host filesystem (runs as host user) | CLI attestation (CLI announced it) |
| Credential delivery | Pull — reads cert from shared bind mount | Exchange — PKCE handshake |
| Credential on disk? | **Yes** — cp-client-cli.{pem,key} at 0640 | **No** — only verifier briefly; cert lives in process memory |
| Bootstrap frequency | None (cert persists across CLI invocations) | Per container boot |
| Scope | `firewall:admin`, `agent:announce`, etc. | `agent:self:*` only (never admin) |
| Cardinality | One (`clawker-cli`) | N (one per running agent) |

The CLI's trust is derived from host filesystem permissions — same
threat model as `~/.ssh/id_rsa`. Anyone with read access to the clawker
data dir IS the CLI from the CP's perspective. That's by design,
because the host user is already the root of trust for everything on
this machine. There's no higher authority to bootstrap against.

Agents' trust is mediated because there's no "host user" concept
inside a container. The CLI is the authority; PKCE is how that
authority is transferred.

**Do not try to unify these flows.** Attempting symmetry either:

- Adds CLI latency (round-trip on every invocation), or
- Moves CLI credentials to "bootstrap verifier on disk" — same credential,
  different name, no actual security improvement

The asymmetry is documented in `internal/controlplane/CLAUDE.md` (or
should be) to prevent well-meaning future refactors from flattening it.

### Post-register lifecycle tracking

Once an agent is registered, the CP tracks it via **three reconciliation
sources**:

| Source | Authoritative for |
|---|---|
| **Docker API `/events`** (subscription, not polling) | Container lifecycle: create / start / die / destroy. Covers rogue stops, OOM kills, host reboots. Filtered by `dev.clawker.managed=true` label. |
| **clawkerd gRPC heartbeats** | In-container state: process health, init progress, command results. Covers "container is alive but the inner process crashed." |
| **BPF ring buffer** | Kernel-level behavior: connection attempts, denied traffic, process exec. Covers security-relevant events at wire speed. |

Conflict resolution: Docker API is authoritative for "does it exist";
clawkerd is authoritative for "is it working"; BPF is authoritative for
"what did it do". The CP reconciles all three into one view.

**Resilience**: if the CLI crashes mid-create (after `docker create` but
before `RegisterAgent`), the CP's Docker event watcher notices the new
clawker-labeled container and reconciles by auto-registering. Dual-path
coverage — happy path is CLI handoff, fallback is event watch.

---

## Capabilities the vision unlocks

Once the full architecture is in place (phase 7+), the following become
possible with no new primitives — just combinations of authenticated
gRPC + BPF telemetry + CP-authoritative state:

1. **Hot-reload clawker.yaml for running agents**. Config change → CP
   pushes `RestartProcess` / `ReloadConfig` commands to affected
   clawkerd instances via `AgentCommandService`. Zero container restart.
2. **Kill-switch on suspicious behavior**. BPF event "container X tried
   crypto mining pool" → CP policy engine → choice of: (a) `docker kill`
   hard, (b) clawkerd graceful-shutdown, (c) surgical SIGTERM to the
   offending process inside the container.
3. **Inter-agent service discovery**. Agent A exposes a service, clawkerd
   registers `agent-A:8080` with CP. Agent B asks CP "how do I reach
   agent-A?". CP brokers address + optionally sets up BPF-routed peer
   connection. No external service mesh.
4. **Cascade propagation**. `clawker firewall add evil.com` → CP updates
   `route_map` in BPF → CP pushes `RestartProcess("nginx")` to any agent
   whose proxy depends on that rule. One command, coordinated cascade.
5. **Compliance audit trail**. Every `execve` in every managed container
   streams via BPF → CP audit bus → persisted. `clawker audit --agent foo
   --since 24h` becomes a real query. This is "security telemetry"
   framing of the metrics work originally deferred in v1.
6. **Cert rotation without restart**. CP rotates auth CA → pushes new
   per-agent certs via command channel → agents hot-reload TLS
   material in-place. Long-running agents never break session.
7. **Cross-agent command execution**. `clawker exec --agent foo --cmd
   "git status"` → CP → clawkerd on `foo` → stream output back.
   Replaces `docker exec` with an authenticated, auditable, routable
   pipe.
8. **Remote CP**. Once the CP owns everything, it doesn't have to run on
   the same host as the CLI. Add a TCP listener + the existing auth stack
   works as-is. `clawker --cp https://build-host:7443 run @`.

Each capability reuses the same three primitives. None require rewriting
the auth layer, the transport, or the state model.

---

## Phase sequencing

Each phase is independently shippable. Each makes the next easier
because patterns harden.

| Phase | Focus | Status |
|---|---|---|
| **v1** | Primitive CP; final auth shape (mTLS+OIDC+JWT+scopes); ebpf-as-CP-feature; hot-reload pinning bug fix; Dockerfile.controlplane pinned build; `make clawker` wired | **🟡 In flight** |
| **Phase 2** | Firewall subsystem fold-in. Rules store ownership, Envoy/CoreDNS container orchestration moves CP-side, `<firewallDataDir>` split into per-concern dirs, proto split into `FirewallService`, host-side `internal/firewall/manager.go` collapses to thin gRPC client | |
| **Phase 3** | Docker `/events` subscription. CP watches clawker-labeled containers; `ListAgents` / `Status` become cheap. First real reconciliation. | |
| **Phase 4** | `AnnounceAgent` RPC + PKCE slot registry + per-agent cert minting. Cert pipeline established. CLI bind-mounts verifier into agent containers. **No clawkerd consumer yet** — the pipeline is dead code until phase 5. | |
| **Phase 5** | clawkerd in managed agents. Every clawker-managed container runs clawkerd. clawkerd reads verifier, calls `Register`, receives per-agent cert + init spec. `init.sh` retires. POC shape fully wakes up. | |
| **Phase 6** | BPF ring buffer → CP event bus. First consumer: audit log. Second consumer: policy engine (phase 7). | |
| **Phase 7** | `AgentCommandService` generalization beyond `RunInit`: `RunCommand`, `ReloadConfig`, `RestartProcess`, `TerminateGracefully`. CP becomes a real orchestrator with active enforcement. | |
| **Phase 8** | Inter-agent networking. Service registration, peer discovery, BPF-routed connections. | |
| **Phase 9** | Monitoring subsystem fold-in (Grafana/Prometheus/Loki/OTEL). | |
| **Phase 10** | Hostproxy + socketbridge subsystem fold-in. Lifecycle RPCs; hostproxy binary still runs on host. | |
| **Phase 11** | TCP listener + remote CP. Unlocks multi-host. Pure addition — auth shape unchanged. | |

Phases 9/10 can slot anywhere after phase 3 without blocking on other
phases. Phases 4–8 are more tightly coupled because they progressively
wake up clawkerd and its capabilities.

---

## What v1 delivers (current PR)

- `clawker-cp` binary + container, replacing `clawker-ebpf` `sleep infinity`
- `ControlPlaneService` gRPC over UDS with mTLS + JWT authz
- OIDC `/token` + `/keys` + `/.well-known/openid-configuration` over UDS with mTLS
- Full auth stack from day 1: auth CA, OIDC signing key, client registry, method-scope map, unary + stream interceptors, mTLS peer CN ↔ JWT sub cross-check
- Package move: `internal/ebpf/` → `internal/controlplane/ebpf/` (ebpf as a CP feature)
- CP owns `Manager.Load()` lifetime — **hot-reload pinning bug fixed by construction**
- `ebpfExec("init")` calls dropped from firewall manager
- Cherry-picked `AgentReportingService` + `AgentCommandService` protos sit in tree, unreachable in v1 (no TCP listener, no clawkerd yet)
- `Dockerfile.controlplane` pinned multi-stage build: `ebpf-manager` + `coredns-clawker` + `clawker-cp` stages, plus BPF bindings extract
- `make cp-binary` target, integrated into `make clawker` dependency chain
- Proto regeneration wired into `make clawker` via Make file-target rule
- Item #1 bundle fix: `clawker firewall enable --agent` resolves container name → ID before cgroup resolution
- End-to-end auth pipeline test at `internal/firewall/cp_client_test.go`

### Known debts the terminal-state vision will correct

These are explicitly acknowledged in v1 and will land during phase 2:

1. **`internal/firewall/manager.go` as CP bootstrap**. v1's host-side
   orchestrator that creates the `clawker-cp` container lives in
   `internal/firewall/`. Conceptually wrong (firewall is a subsystem,
   not the bootstrapper), but tolerated in v1 because that's where the
   cherry-pick landed. Phase 2 splits into a minimal host-side CP
   bootstrap + a thin gRPC client wrapper.
2. **`ControlPlaneService` contains firewall methods only**. Name is
   aspirational; content is one subsystem. Phase 2 splits into
   `FirewallService` + siblings.
3. **CLAUDE.md framing inversion**. `internal/firewall/CLAUDE.md`
   frames the CP as a dependency of the firewall
   ("firewall no longer uses docker exec..."). Phase 2 inverts the
   framing: firewall is a subsystem of the CP, documented from that angle.

---

## Resolved from POC brainstorm (2026-02-16)

Items flagged open in the POC brainstorm that now have answers:

| POC question | Resolution |
|---|---|
| Package split `controlplane/` vs `clawkerd/`? | ✅ `internal/controlplane/` for CP code (with `ebpf/` nested as a CP feature); `internal/clawkerd/protocol/v1/` for wire contract |
| Address resolution on clawker-net? | ✅ CP side — CP has a static IP on clawker-net; CLI dials over UDS via bind mount, not network. Agent-side resolution deferred to phase 5 |
| Hostproxy retirement timeline? | ✅ shape resolved. Lifecycle moves to CP (`HostProxyService`); binary still runs on host because it has to bridge. Phase 10 |
| Migration from `CreateContainer()` to CP-mediated? | ✅ model resolved. K8s-lite split — CLI keeps `CreateContainer` for user agents; CP owns infra; agent registration is a post-create handoff RPC. Not a migration, a responsibility split |
| No auth on clawkerd↔CP | ✅ Full mTLS + OIDC + JWT + scopes lands in v1 for CLI↔CP. Per-agent PKCE bootstrap planned for phase 4 |
| Two-gRPC-server pattern | ✅ Validated by POC; protos cherry-picked to v1 tree; handlers registered but unreachable until phase 5 wakes clawkerd up |

### Still open (mostly blocking on phase 5 — clawkerd in managed agents)

1. **Production entrypoint behavior** — block on clawkerd ready before
   dropping privileges, or fire-and-forget like POC? Undecided.
2. **`HostConfig.Init=true` vs explicit tini** — POC used explicit tini in
   Dockerfile. Not yet evaluated for production.
3. **Container ID handling** — clawkerd needs to read its own full
   container ID (probably from `/proc/self/cgroup`) rather than rely on
   the 12-char truncated Docker hostname.
4. **Init spec population** — POC hardcoded in test. Production source
   probably `clawker.yaml` `agent.post_init` + CP-side defaults merge.
   Undesigned.
5. **`Register` RPC synchronous Docker inspect** — latent risk from POC
   pattern. Must move off critical path before phase 5.
6. **`go s.runInitOnAgent()` goroutine lifecycle** — no errgroup, no
   structured cancellation. Fine in v1 (dead code). Must fix before
   phase 5.
7. **Event stream backpressure** — BPF ring buffer events can be
   high-volume. Drop policy vs buffering vs per-consumer queues TBD.
   Phase 6 concern.
8. **Command channel scope model** — `AgentCommandService.RunCommand` is
   "arbitrary root exec inside a container". Needs the tightest possible
   scope + mandatory audit. Phase 7 design work.
9. ~~**Auth namespace directory name**~~ — resolved: `auth/`.
10. **Decouple consts from Config** — the CP (and other packages) need
    access to static values like label keys, network names, container
    names, scope strings, default ports, env var names, and file names
    without importing `internal/config` and constructing an instance.
    Pattern: `internal/consts/` leaf package (stdlib only, zero internal
    imports). True `const` values move there; Config methods that return
    computed/env-dependent values stay on the interface. Config methods
    that currently just return a static string become either deprecated
    or trivial wrappers around `consts.X`. Should land as part of v1
    since the CP needs these immediately.

---

## Security considerations

Worth flagging because phase 7 grows the CP's privilege significantly —
the CP with `AgentCommandService.RunCommand` is effectively "root on every
managed container on the host." Mitigations to plan before that phase:

1. **Auth CA private key protection**. v1 writes the key to disk in the
   data dir. Phase 7+ should evaluate kernel keyring / hardware token so
   even CP process compromise doesn't leak the signing authority.
2. **`RunCommand` scope gating**. Most restrictive possible scope (e.g.
   `agent:exec:<id>`). Not granted to `clawker-cli` by default — only
   specific operator commands like `clawker exec` issue JWTs with that
   scope, and only scoped to the one agent being targeted.
3. **Audit log out-of-CP replication**. Every command pushed to any agent
   is logged and streamed to Loki immediately. Compromise detection.
   Non-secret slot store (PKCE property) makes this clean.
4. **Two-party authorization for destructive commands**. `RunCommand("rm
   -rf /")` requires CLI + interactive human confirmation, not just CLI
   authority. Policy engine concern.
5. **Auth CA ≠ MITM CA**. Separate files, separate rotation, no
   cross-reference. Compromise of one does not cascade.
6. **RO `settings.yaml` mount**. CP cannot mutate user config. Kernel-
   level enforcement via mount flag, not code-review enforcement.
7. **CLI cert on disk at 0640**. Known limitation. Accepted because host
   user is already the root of trust for everything on this machine.
   Analogous to `~/.ssh/id_rsa` threat model.
8. **Per-agent cert in process memory only**. Long-lived credential never
   touches host disk. Only the short-lived PKCE verifier does, and
   clawkerd deletes it immediately after consumption.

---

## Mental model summary

> **The clawker XDG directories are the CP's state namespace. The CP
> container bind-mounts all four dirs (RW for data/state/cache, RO for
> config) at FHS-standard paths inside the container. Organization is by
> subsystem namespace (`firewall/`, `monitor/`, `auth/`, `agents/`, ...).
> The CP is the clawker daemon for the machine; the firewall is one
> subsystem among peers. The auth shape from v1 (mTLS + OIDC + JWT +
> scopes) is final and extends to new callers as additive-only
> `ClientRegistration` entries. Per-agent identity uses PKCE-bridged cert
> exchange with slot consumption as replay defense. CLI and agent
> bootstrap flows are structurally asymmetric because their trust origins
> differ — the CLI's authority comes from host filesystem permissions,
> the agent's comes from CLI attestation.**

---

## POC Origins (2026-02-16, preserved for lineage)

> Original content preserved below as historical record. The POC validated
> the two-gRPC-server transport pattern but predates the "CP as primary
> clawker daemon" framing, the full auth stack, PKCE, subsystem
> architecture, XDG-FHS directory model, and the k8s-lite mental model.

### POC Results (from test/controlplane/)

#### What was built
- **Proto schema** (`internal/clawkerd/protocol/v1/agent.proto`): AgentReportingService (Register), AgentCommandService (RunInit)
- **clawkerd binary** (`clawkerd/main.go`): Container-side agent — starts gRPC server, registers with CP, handles RunInit (executes bash commands, streams progress, writes ready file)
- **Control plane server** (`internal/controlplane/`): server.go + registry.go — accepts Register, resolves container IP via Docker inspect, connects back to clawkerd's gRPC server, calls RunInit, consumes progress stream
- **Test Dockerfile** (`test/controlplane/testdata/Dockerfile`): Two-stage build (Go builder → Alpine), installs su-exec + tini, root entrypoint with gosu/su-exec drop
- **Test entrypoint** (`test/controlplane/testdata/entrypoint.sh`): Starts clawkerd in background, drops to claude user via su-exec
- **Integration test** (`test/controlplane/controlplane_test.go`): Full end-to-end — builds image, starts CP in-process, runs container, verifies registration, init progress, privilege separation
- **Harness extensions**: WithNetwork(), WithPortBinding(), network join on start
- **Makefile**: `make test-controlplane`, `make proto` (buf generate), excluded from unit tests

#### What was validated
1. Two-gRPC-server pattern works across Docker network (host → container via port mapping)
2. Address discovery works (clawkerd registers with listen port, CP resolves via Docker inspect + port binding)
3. Server-streaming RunInit progress flows correctly (STARTED → COMPLETED per step → READY)
4. Root entrypoint + su-exec privilege drop works (clawkerd runs as root UID 0, main process as claude UID 1001)
5. tini as PID 1 (via Dockerfile ENTRYPOINT, not HostConfig.Init in POC) manages both processes
6. Ready file signal mechanism works (/var/run/clawker/ready)
7. Init step command execution with stdout/stderr capture works

#### What was NOT validated / deferred
- HostConfig.Init (POC uses explicit tini in Dockerfile ENTRYPOINT instead)
- Graceful degradation (clawkerd falling back to baked-in defaults when CP unreachable)
- Reconnection logic (gRPC stream drops)
- Docker Events integration
- Watermill message queue
- SchedulerService (CLI → CP resource management)
- Entrypoint waiting on clawkerd ready signal (POC entrypoint is fire-and-forget)

### POC Open Items / Questions (most now resolved above)
- How to handle the entrypoint wait? Current POC starts clawkerd & then immediately drops privileges. Should it wait for ready file before exec su-exec?
- Should we move to HostConfig.Init=true (Docker injects tini) or keep explicit tini in entrypoint? POC uses explicit.
- What's the plan for the `internal/controlplane/` vs `internal/clawkerd/` package split? Currently CP is in `controlplane/`, agent protocol in `clawkerd/protocol/`. Is this the right layout long-term? **[RESOLVED — see table above]**
- The test uses `host.docker.internal:host-gateway` for container→host communication. In production, the CP listens on clawker-net. How does address resolution change? **[RESOLVED — see table above]**
- Container ID mismatch: Docker hostname is 12-char truncated ID, but Docker API uses full ID. The test handles both — should clawkerd send full ID (read from /proc or cgroup)?

### POC Decisions Made
- Two-gRPC-server pattern: VALIDATED by POC. CP and clawkerd each run their own gRPC server.
- su-exec over gosu: POC chose su-exec (Alpine native, ~10KB). Works.
- Root entrypoint + privilege drop: VALIDATED. Clean separation.
- Ready file at /var/run/clawker/ready: Works as signal mechanism.
- buf for protobuf generation: Configured (buf.yaml + buf.gen.yaml), `make proto` target added.
- Test harness extended with WithNetwork() and WithPortBinding() for control plane tests.

### POC Conclusions / Insights
- The two-server gRPC pattern is clean and works well across Docker networking boundaries.
- Port binding (host port mapping) is needed on macOS/Docker Desktop where container IPs aren't routable from host. The CP's resolveAgentAddress() handles both port mapping and direct IP fallback.
- The POC entrypoint is minimal (3 lines) — the complexity lives in Go, not bash. This validates the "init logic in Go, not bash" principle.
- clawkerd's RunInit handles step failures gracefully (logs, sends FAILED event, continues to next step).

### POC Gotchas / Risks (some still apply to phase 5)
- Container ID truncation: Docker sets hostname to 12-char prefix. Need consistent ID handling between clawkerd and CP. **[Still open for phase 5]**
- The CP currently does Docker inspect in the Register RPC handler — this is synchronous and could slow registration if Docker is slow. **[Still open — must fix before phase 5]**
- The `go s.runInitOnAgent()` goroutine in Register has no structured lifecycle management yet (no errgroup, no cancellation tracking). **[Still open — must fix before phase 5]**
- No auth on the clawkerd→CP gRPC connection beyond the shared secret in Register. The callback connection (CP→clawkerd) has no auth at all. **[RESOLVED by v1 auth stack + planned phase 4 PKCE bootstrap]**
