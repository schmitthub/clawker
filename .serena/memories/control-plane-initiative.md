# Spec: Control Plane & clawkerd Initiative

## Metadata
- **Task**: control-plane-initiative
- **Created**: 2026-04-12
- **Status**: draft
- **Branch**: feat/control-plane
- **Intensity**: high
- **Intensity reason**: trust boundary (CP is highest-privilege component), auth keywords (mTLS, OIDC, JWT, PKCE, certificate), security-critical infrastructure
- **Override**: none
- **Research**: null (internal architecture initiative, no external library research needed)
- **Branch specs**: `.correctless/specs/cp-initiative/branch-N-*.md` (created at each branch kickoff via `/cspec`)

## Context

Clawker's current architecture uses multiple independent PID-file daemon processes (firewall, hostproxy, socketbridge) and a monolithic `firewall.Manager` (77 responsibilities, 1816 lines) that bootstraps the control plane container as if it were a firewall dependency. This inverted ownership blocks the project from reaching stable release.

The initiative establishes the CP as the authoritative orchestrator of the clawker ecosystem — a containerized, privileged, long-lived daemon with Docker-outside-of-Docker (DooD) access. The firewall, monitoring, hostproxy, and socketbridge become subsystems of the CP. Then clawkerd (an agent-side daemon running as root in every managed container) replaces init scripts and enables remote management, observability, and authenticated agent-CP communication via PKCE-bootstrapped mTLS.

**"Out of alpha" means:** Full robust CP, agent daemon (clawkerd), monitoring, logging. A stable ecosystem to orchestrate sandboxed agent containers. Everything before this was MVP to prove the concept.

**No backward compatibility needed for eBPF/CP** — current releases use iptables with no CP. eBPF has never shipped in a release. Current release tag is stable primitive MVP; users have what they need while this lands.

**The iptables→eBPF migration already happened** as a clean break on main. The `clawker-ebpf` container (sleep infinity + docker exec) is what's live. Branch 1 converts it to a proper CP with gRPC.

## Key Decisions (2026-04-12 session)

1. **Two halves**: First establish CP and sunset old infra (daemons, docker-exec). Then establish clawkerd and sunset what it replaces (init scripts, entrypoint bash).

2. **Container start flow (target state)**: CLI builds image + creates container → CLI tells CP "new agent coming" (PKCE: challenge to CP, verifier to container) → container starts → clawkerd (root) registers with CP via PKCE → CP mints per-agent cert → clawkerd reconnects with mTLS.

3. **Ownership inversion is Branch 2, not Branch 1**: Branch 1's job is just "CP exists and works as a service." The firewall still bootstraps it (acknowledged tech debt). Branch 2 does the actual inversion.

4. **HIGH intensity**: Trust boundaries (CP is highest-privilege), auth (mTLS, OIDC, JWT, PKCE), security-critical infrastructure. This is a security tool built by a security engineer.

## Scope

**In scope:**
- CP as primary daemon with DooD, owning all infrastructure container lifecycle
- Firewall subsystem fold-in (Envoy, CoreDNS, eBPF, rules, certs, configs)
- Daemon consolidation (firewall daemon, hostproxy daemon → CP-managed)
- clawkerd in managed agent containers (PKCE registration, per-agent certs, init replacement)
- Release pipeline adaptation for CP build chain + build provenance + attestations
- Proto service split (ControlPlaneService → FirewallService + per-subsystem services)
- Data directory restructure (per-subsystem namespaces under XDG data dir)

**Not in scope:**
- Native IPv6 support (separate initiative, tracked in Serena `outstanding-features`)
- Inter-agent networking / service discovery (post-stable vision)
- Remote CP / TCP listener (post-stable vision)
- OTel metrics from eBPF ring buffer (nice-to-have, not blocking stable)
- WebUI OIDC client (Branch 7)

## Complexity Budget

- **Trust boundaries touched**: 3 (TB-001: CLI-CP mTLS+JWT, TB-002: CP-Docker DooD, TB-003: clawkerd-CP PKCE+mTLS)
- **Risk surface delta**: high (CP gains Docker socket access + becomes single point of failure for all agent lifecycle)

Per-branch complexity is captured during each branch's `/cspec` kickoff.

---

## Merge Strategy

**Trunk-based development**: One branch = one PR = one milestone. No sub-PRs. Each branch targets `main` directly.

**Per-branch initiative gate**: Each branch is treated as its own standalone initiative. Before implementation begins, it goes through a full `/cspec` → `/creview` cycle to produce a detailed branch spec. This is where assumptions from the roadmap get tested against actual code and concrete decisions get made (e.g., thin client vs direct gRPC, port separation, subsystem interface shape). Discoveries during branch kickoff feed back into this master document — the dependency graph, open questions, and scope may all be amended. Future branches may be reordered, merged, or split based on what earlier branches reveal.

**Living roadmap**: This spec describes intent and ordering, not a contract. Every branch will surface unknowns that reshape subsequent branches. Examples: Branch 2 might reveal that a thin gRPC client wrapper is wrong and we should port firewall commands to direct gRPC methods. Or we might discover we need admin commands on a separate port from agent commands. The spec is updated as decisions land — it captures current best understanding, not final answers.

**Highway construction pattern**: New infrastructure lands alongside old. Old code stays live and routed-to until the replacement is proven. Switchover and removal happen in the same or subsequent branch. No intermediate state where a pulled `main` can't run containers or enforce firewall rules. Legacy code paths are only removed when absolutely unavoidable — the preference is parallel operation until the replacement is proven.

**Branch sizing**: Alpha project — branches can be substantial. Each branch represents a meaningful milestone.

**Stability contract**: At every merge point, someone pulling main and running `make clawker && ./bin/clawker run @` gets a working container with firewall enforcement. The old path (firewall daemon, docker-exec, init scripts) continues to serve until the CP replacement is wired in.

---

## Branches

### Branch 1: CP as a Proper Running Service (current: `feat/control-plane`)

**Goal**: The CP exists, runs as a real service with structured logging, and serves authenticated gRPC. The CLI authenticates to the CP for firewall/eBPF operations. Replaces the `clawker-ebpf` sleep-infinity + docker-exec pattern.

**What lands**:
- `internal/consts/` leaf package (constants extracted from config)
- Proto definitions + buf tooling (`controlplane.proto`, `agent.proto`, generated code)
- `internal/ebpf/` moved to `internal/controlplane/ebpf/` (ebpf as CP feature)
- `internal/controlplane/` — full auth stack: CA + cert issuance, OIDC provider (/token, /keys, /.well-known), client registry, authz interceptor (mTLS CN↔JWT sub cross-check, fail-closed method scopes)
- `cmd/clawker-cp/main.go` — CP binary with structured zerolog to stderr (visible via `docker logs`)
- `internal/firewall/oidc_client.go` — CLI-side mTLS config, OIDC token source, UDS dialer
- `internal/firewall/manager.go` — `ebpfExecImpl` becomes gRPC dispatch shim, `cpClient()` lazy construction, `cpContainerConfig()`, `waitForCPReady()`
- `Dockerfile.controlplane`, `make cp-binary` target
- End-to-end auth pipeline test (`cp_client_test.go`)

**What stays the same (highway construction)**:
- `firewall.Manager` still bootstraps the CP container (ownership inverted — fixed in Branch 2)
- `firewall.Manager` still creates Envoy/CoreDNS containers directly
- Firewall daemon still runs (health checks, container watcher)
- Hostproxy, socketbridge, monitor — completely untouched
- `BootstrapServicesPreStart` flow unchanged except eBPF ops go through gRPC

**Stability**: `clawker run @` works. Every firewall command works. The only behavior change is transport: gRPC over mTLS+JWT instead of docker-exec. If the CP is unhealthy, the firewall manager's `waitForCPReady` fails and surfaces a clear error.

### Branch 2: Ownership Reversal — CP Owns Firewall

**Goal**: Invert the authority. The CP becomes the owner of the firewall subsystem. `firewall.Manager` becomes a thin gRPC client. The firewall daemon is sunset.

**What lands**:
- `internal/controlplane/bootstrap.go` — `EnsureControlPlane()` extracted from firewall.Manager as the host-side entry point
- New Factory noun: `f.ControlPlane()` on `cmdutil.Factory`
- Docker socket mounted into CP container (DooD — CP can manage Docker containers)
- CP gains firewall subsystem: Envoy/CoreDNS container lifecycle, config generation (envoy.yaml, Corefile), rules store ownership, MITM cert management, network management
- `controlplane.proto` gains: `EnsureFirewallStack`, `StopFirewallStack`, `AddRules`, `RemoveRules`, `ListRules`, `ReloadFirewall`, `RotateCA`, `FirewallStatus`
- `internal/firewall/manager.go` collapses from ~1800 lines to ~300 (thin gRPC client preserving `FirewallManager` interface)
- Code from `internal/firewall/{envoy,coredns,certs,network,rules}.go` moves to CP-accessible packages
- `BootstrapServicesPreStart` calls `EnsureControlPlane()` first, then firewall ops via thin client
- Firewall daemon sunset: health probes + container watcher move into CP process
- `firewall.EnsureDaemon()` / `StopDaemon()` replaced by `EnsureControlPlane()` / CP lifecycle
- `clawker firewall up/down` adapted to control CP instead of a PID-file daemon

**What stays the same (highway construction)**:
- `FirewallManager` interface unchanged — all 15 methods still work, just backed by gRPC now
- Hostproxy, socketbridge — still PID-file daemons, still managed by CLI. Untouched.
- Monitor stack — still Docker Compose, untouched.
- Container entrypoint — unchanged (no clawkerd yet)

**Stability**: `clawker run @` works. Every firewall command works. The flow changes from "CLI does everything" to "CLI ensures CP, CP manages infra." If someone pulls main, the firewall still enforces — just the orchestrator changed.

### Branch 3: Daemon Consolidation — Hostproxy + Socketbridge Under CP

**Goal**: Remaining PID-file daemons brought under CP management. Docker events subscription replaces polling.

**What lands**:
- Docker events subscription in CP (filtered by `dev.clawker.*` labels) — replaces polling-based container watchers
- Hostproxy lifecycle managed by CP (CP starts/stops the host-side binary as needed)
- Socketbridge lifecycle managed by CP (CP manages per-container bridge daemons)
- Remove PID-file daemon patterns from hostproxy and socketbridge managers
- `BootstrapServicesPostStart` routes socketbridge through CP

**What stays the same (highway construction)**:
- Hostproxy binary still runs on host (must — bridges browser auth). Only lifecycle management changes.
- Socketbridge still uses per-container muxrpc daemons. Only who starts/stops them changes.
- Container entrypoint — unchanged (no clawkerd yet)
- Monitor stack — unchanged

**Stability**: `clawker run @` works. SSH/GPG forwarding works. Browser auth flows work. The behavioral contract is identical — just the lifecycle manager changed from PID-files to CP.

### Branch 4: clawkerd Authentication + Registration

**Goal**: clawkerd exists as an agent-side daemon. It authenticates to the CP via PKCE-bootstrapped mTLS. Per-agent certs minted by CP. The registration handshake is proven.

**What lands**:
- `cmd/clawkerd/main.go` — agent-side daemon running as root
- PKCE slot registry on CP (PendingSlot with 60s TTL, S256-only, atomic consumption)
- `AnnounceAgent` RPC — CLI pre-announces before `docker create`, writes `code_verifier` to container bind mount
- `Register` RPC — clawkerd reads verifier, dials CP one-way TLS, CP validates PKCE + mints per-agent cert
- clawkerd reconnects with full mTLS after receiving cert
- `internal/bundler/` updated — clawkerd baked into newly-built agent images

**What stays the same (highway construction)**:
- Old images (no clawkerd) still work via existing entrypoint. No breakage.
- CLI calls `AnnounceAgent` if CP is available, silently skips if not (graceful degradation for old CP versions or during transition)
- Init scripts still work — clawkerd doesn't replace them yet
- Agent containers still use the existing entrypoint flow

**Stability**: `clawker run @` works with both old and new images. New images have clawkerd but it's additive — existing entrypoint still handles init. The PKCE registration is exercised but doesn't gate container functionality yet.

### Branch 5: Init Migration + Agent Lifecycle

**Goal**: clawkerd takes over init from bash scripts. CP tracks agent lifecycle. 

### Branch 6: Monitor Fold-in, Release Pipeline, Hardening

**Goal**: Final subsystem under CP management. Release pipeline adapted. Docs and tests comprehensive.

**What lands**:
- Monitoring subsystem managed by CP (replaces or wraps Docker Compose, `clawker monitor` commands route through CP)
- Release pipeline: `Dockerfile.controlplane` multi-stage, GoReleaser `builder: prebuilt`, SLSA attestation
- CI updates for proto regen, new packages in lint/security/test
- Data directory restructure to per-subsystem namespaces (with migration)
- All CLAUDE.md, architecture docs, Mintlify docs, threat model updated
- Comprehensive E2E tests for full CP + clawkerd lifecycle

**Stability**: Everything works. Release pipeline produces correct cross-platform binaries.

### Branch 7: WebUI + Kratos Identity

**Goal**: Web-based admin UI for the control plane. Kratos provides identity management for human users (login, sessions). Completes the "out of alpha" vision with a full user-facing interface.

**What lands**:
- Ory Kratos binary in CP container (identity server for human users)
- WebUI frontend (framework TBD) served by CP or standalone container
- `authorization_code` + PKCE flow for browser-based auth via Hydra
- Kratos login/consent flow integration with Hydra
- WebUI-scoped access tokens (separate from `admin` and `agent:*` scopes)
- Dashboard: container status, firewall rules, agent lifecycle, monitoring overview

**What stays the same (highway construction)**:
- CLI, clawkerd, and all M2M flows unchanged — Kratos is additive
- All existing CP admin API methods work as before

**Stability**: `clawker run @` works. All CLI and agent flows unchanged. WebUI is additive — the CP is fully functional without it.

---

## Branch Dependency Graph

```
Branch 1 (CP service) ──> Branch 2 (ownership reversal)
                                  |
                                  v
                          Branch 3 (daemon consolidation)
                                  |
                                  v
                          Branch 4 (clawkerd auth)
                                  |
                                  v
                          Branch 5 (init + lifecycle)
                                  |
                                  v
                          Branch 6 (monitor + release + hardening)
                                  |
                                  v
                          Branch 7 (WebUI + Kratos)
```

Strictly sequential — each branch depends on the previous. Release pipeline work within Branch 6 can start earlier if needed.

Branch ordering is amended during branch kickoff `/cspec` — the master graph is updated when changes land. Docs and tests are woven into each branch, not deferred.

---

## Architectural Invariants

These are initiative-level constraints that span multiple branches. Implementation-specific invariants are defined in each branch's `/cspec`.

### INV-001: CP is the single lifecycle owner for infrastructure containers
- **Type**: must
- **Category**: resource-lifecycle
- **Statement**: After Branch 2, only the CP process may create, start, stop, or restart Envoy, CoreDNS, and monitoring stack containers. The host CLI must not make Docker API calls for infrastructure container lifecycle.
- **Violated when**: Host-side code calls `client.ContainerCreate/Start/Stop/Restart` for any container with `purpose=firewall` or `purpose=monitoring` label
- **Test approach**: integration (grep for Docker API calls in firewall package, assert only gRPC calls remain)
- **Risk**: high

### INV-002: Auth shape is final from Branch 1 onward
- **Type**: must
- **Category**: security
- **Statement**: mTLS + OIDC + JWT + per-method scope enforcement. New callers add `ClientRegistration` entries and `methodScopes` entries. The interceptor, token format, cert management, and auth flow never change.
- **Violated when**: Auth interceptor is modified, token format changes, or cert management is rewritten
- **Test approach**: unit (authz_test.go, oidc_provider_test.go, ca_test.go)
- **Risk**: critical

### INV-005: CP container has Docker socket access (DooD trust boundary)
- **Type**: must
- **Category**: security
- **Statement**: The CP container mounts `/var/run/docker.sock` and uses it to manage infrastructure containers. This is a deliberate, documented trust boundary. The CP has root-equivalent Docker access.
- **Violated when**: Docker socket is mounted into any container other than the CP
- **Test approach**: unit (assert only cpContainerConfig includes Docker socket mount)
- **Risk**: high

### INV-007: Trunk stability at every PR boundary
- **Type**: must
- **Category**: functional
- **Statement**: Every PR must leave main in a fully working state. No feature flags, no half-wired code. Old code paths remain until the replacement is proven.
- **Violated when**: `make test` fails after merging any individual PR, or a command that worked before the PR no longer works
- **Test approach**: CI (full test suite on every PR)
- **Risk**: high

### INV-008: Two CAs never cross-reference
- **Type**: must-not
- **Category**: security
- **Statement**: The auth CA (identity, signs mTLS certs) and the firewall MITM CA (TLS inspection, signs per-domain certs) are separate files, separate rotation schedules, no cross-reference. Compromise of one must not cascade.
- **Violated when**: Auth CA signs a domain cert, MITM CA signs an identity cert, or either references the other's key material
- **Test approach**: unit (assert distinct file paths, distinct key material)
- **Risk**: critical

### INV-009: Fail-closed on unmapped gRPC methods
- **Type**: must
- **Category**: security
- **Statement**: The authz interceptor's `methodScopes` map must contain every registered gRPC method. A missing entry returns PermissionDenied, not a bypass.
- **Violated when**: A new RPC is added without a corresponding `methodScopes` entry
- **Test approach**: unit (enumerate all registered methods, assert each has a scope entry)
- **Risk**: critical

## Branch-Level Invariants (Deferred)

These constraints are design intent, not final specifications. They are validated and potentially revised during each branch's `/cspec` kickoff.

### INV-003: PKCE slot consumption is atomic and single-use → Branch 4
- PKCE slot consumed exactly once via atomic delete. Replay fails. 60s TTL.

### INV-004: Per-agent cert never touches disk → Branch 4
- Per-agent mTLS cert lives only in clawkerd's process memory. Only the short-lived PKCE verifier touches the filesystem.

### INV-006: Firewall BPF lifecycle ordering preserved → Branch 1/2
- CP must call `ebpf.Manager.Load()` and pin BPF maps before CoreDNS starts. The dnsbpf plugin opens the pinned `dns_cache` map on startup.

### INV-010: clawkerd runs as root, agent process does not → Branch 5
- clawkerd starts as root. Entrypoint drops to unprivileged `claude` user via gosu before agent process starts.

## Prohibitions

### PRH-001: No Docker API calls for infra containers from host CLI (after Branch 2)
- **Statement**: Host-side code must not call Docker Container/Image/Network APIs for `purpose=firewall` or `purpose=monitoring` containers. All infra lifecycle goes through CP gRPC.
- **Detection**: grep for `client.Container{Create,Start,Stop,Restart,Remove}` in `internal/firewall/`, `internal/cmd/`
- **Consequence**: Dual-owner race conditions, CP state inconsistency

### PRH-002: No `docker exec` into CP container
- **Statement**: The legacy `docker exec ebpf-manager <cmd>` pattern is permanently retired. All CP communication is via authenticated gRPC.
- **Detection**: grep for `ExecCreate` or `ExecStart` targeting CP container name
- **Consequence**: Bypasses auth layer, breaks audit trail

### PRH-003: No PID-file daemons for new services
- **Statement**: New services must not use the PID-file + detached subprocess pattern. All daemon lifecycle is CP-managed.
- **Detection**: grep for `writePIDFile`, `exec.Command` with `Setsid: true` in new code
- **Consequence**: Lifecycle fragmentation, defeats CP consolidation goal

### PRH-004: No auth downgrades
- **Statement**: No `insecure.NewCredentials()` on any production gRPC connection to the CP. No skipping JWT verification. No disabling mTLS.
- **Detection**: grep for `insecure.NewCredentials` in CP client code
- **Consequence**: Authentication bypass, security regression

## Boundary Conditions

### BND-001: CP container crash during agent start
- **Boundary**: TB-001 (CLI-CP)
- **Input from**: CLI calling EnsureControlPlane during container start
- **Validation required**: CP health probe before proceeding to firewall enable
- **Failure mode**: fail-closed (container start fails with "control plane unavailable" error)

### BND-002: PKCE slot expiry race → Branch 4
- Deferred to Branch 4 `/cspec`. Design intent: slot TTL check with monotonic clock, fail-closed on expiry.

### BND-003: Docker socket unavailable inside CP
- **Boundary**: TB-002 (CP-Docker)
- **Input from**: CP attempting Docker API calls
- **Validation required**: Docker ping on CP startup before serving RPCs
- **Failure mode**: fail-closed (CP refuses to start, removes ready file, host-side bootstrap retries)

### BND-004: Cert material missing on CLI cold start
- **Boundary**: TB-001 (CLI-CP)
- **Input from**: CLI calling cpClient() before CP has written certs
- **Validation required**: ensureCPClientReady() checks file existence
- **Failure mode**: fail-closed (error guides user to start CP first)

## Open Questions

Resolved OQs are deleted — decisions live in the branch spec that resolved them.

### OQ-001: Hostproxy daemon vs CP subprocess
- Does the hostproxy daemon become a subprocess managed by CP, or a gRPC service inside the CP container?
- Hostproxy must run on the host (bridges browser auth flows). CP can manage lifecycle but can't host it.
- Leaning: CP manages lifecycle via subprocess, similar to today but without PID files.

### OQ-002: Socketbridge architecture post-CP
- Do per-container socket bridges become CP-managed subprocesses, or does CP use a different forwarding mechanism?
- Current pattern spawns N processes (one per container). CP could multiplex.
- Leaning: Start with CP-managed subprocess pattern, optimize later.

### OQ-003: Monitor stack via Docker Compose or CP-direct?
- Does monitoring stay Docker Compose managed by CP, or does CP create containers directly?
- Leaning: Defer to Branch 6, start with Docker Compose managed by CP subprocess.

### OQ-004: Config generation location
- When CP owns Envoy/CoreDNS, does it generate configs internally or receive them via gRPC?
- Leaning: CP-side generation (CP owns rules store, should own config generation).

### OQ-005: Admin vs agent ports
- Separate gRPC listeners for CLI admin commands vs clawkerd agent commands?
- Decided during Branch 2 or 4 kickoff.

### OQ-006: FirewallManager interface fate
- Thin client wrapper, direct gRPC replacement, or something else?
- Might become thin gRPC client, might be replaced by direct gRPC methods, might split admin commands onto a separate port.
- Decided during Branch 2 kickoff.

## Reference Pointers

| What | Where |
|------|-------|
| Branch specs (created at kickoff) | `.correctless/specs/cp-initiative/branch-N-*.md` |
| Sub-initiative context | `.correctless/specs/cp-initiative/CLAUDE.md` |
| Architecture docs | `.claude/docs/ARCHITECTURE.md`, `.claude/docs/DESIGN.md` |
| Key concepts index | `.claude/docs/KEY-CONCEPTS.md` |
| CP brainstorm (reference artifact, not authority) | `.serena/memories/brainstorm_the-controlplane-and-clawkerd` |
| Outstanding features | `.serena/memories/outstanding-features` |
| Bug tracker | `.serena/memories/bug-tracker` |
| Per-package docs | `internal/*/CLAUDE.md` (51 files) |
| Firewall package docs | `internal/firewall/CLAUDE.md` |
| CP package docs | `internal/controlplane/CLAUDE.md` |
| Testing safety | `.serena/memories/correctless/testing-safety` |

## Maintenance

This is a living document. Updates happen at two defined points:

- **Branch kickoff** (`/cspec`): Scope finalized, open questions resolved (resolved OQs are deleted — decisions live in the branch spec), dependency graph amended if discoveries warrant it, deferred invariants promoted and potentially revised in the branch spec.
- **Branch merge**: Current state updated in `cp-initiative/CLAUDE.md` to reflect the latest completed branch.

The dependency graph, open questions, and scope may all change as branches surface unknowns. This is by design — the master spec captures current best understanding, not a contract.
