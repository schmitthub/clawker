# Spec: Branch 2 — CP Owns the Firewall (Ownership Reversal)

## Metadata
- **Created**: 2026-04-14
- **Status**: draft
- **Impacts**: control-plane-initiative, branch-1-cp-service
- **Branch**: feat/firewall-cp-migration
- **Research**: null
- **Recommended-intensity**: high
- **Intensity**: high
- **Intensity reason**: trust boundary (TB-002: CP-Docker DooD expanded, TB-001: CLI-CP surface scope-corrected — 13 firewall RPCs net after collapsing B1 Install into Enable and re-scoping Remove), auth keywords (mTLS, JWT, CA, certificate, TLS inspection), security-critical infrastructure, CP becomes authoritative owner of firewall state and lifecycle
- **Override**: none

## Context

Branch 1 shipped a proper control plane daemon with mTLS + Hydra OAuth2 + gRPC AdminService for eBPF operations. Branch 2 completes the ownership inversion: the CP becomes the authoritative owner of all firewall state, configuration, and container lifecycle.

Today `internal/firewall/` is a god package (~9,300 LoC across 20 .go files) with two shapes of state:

1. **`firewall.Manager`** (1,715 LoC, 34 methods) — monolithic Docker implementation of `FirewallManager`. Creates and runs Envoy + CoreDNS + CP containers; generates `envoy.yaml` + `Corefile`; owns `storage.Store[EgressRulesFile]`; manages MITM CA + per-domain certs; discovers network topology; dispatches eBPF operations as gRPC calls to the CP it bootstrapped.
2. **`firewall.Daemon`** (696 LoC) — PID-file background process. Health probe loop (5s, Envoy+CoreDNS+CP), container watcher loop (30s poll agent containers), graceful teardown on 0-agent exit, BPF link cleanup.

The manager bootstraps the CP as if the CP were a firewall dependency. The daemon holds lifecycle authority via a PID file. This inversion blocks stable release.

**The inversion flips three axes at once:**

- **State ownership**: CP becomes the sole writer of `egress-rules.yaml`, MITM CA files, and per-domain certs under `cfg.FirewallDataSubdir()`. CLI reads and mutations go through AdminService RPCs only. Paths stay put — only bind mount mode changes (RO → RW for CP; stays RO for Envoy/CoreDNS).
- **Container lifecycle**: CP owns Envoy + CoreDNS creation via Docker-outside-of-Docker (DooD) — the Docker socket is already mounted into CP from B1 (INV-005). CLI continues to own CP container creation and `clawker-net` creation (avoids a CP-can't-start-itself race).
- **Daemon dissolution**: The host-side PID-file firewall daemon is deleted. Its health probe loop is absorbed into the CP's existing aggregate `/healthz`. Its container watcher loop moves into the CP process (same 30s poll, same `missed_threshold=2`, same graceful exit — just relocated). PRH-003 (no PID-file daemons for new services) is honored.

**The package itself is deleted.** `internal/firewall/` does not survive the merge. Its files migrate to `internal/controlplane/` (most unchanged, some redesigned). The `FirewallManager` interface and its thin-client wrapper are not preserved. Commands call `f.AdminClient(ctx) (adminv1.AdminServiceClient, error)` directly — a new single lazy Factory noun that internally calls `controlplane.EnsureRunning` then `auth.DialCPAdmin`. This is the new pattern for domain packages (firewall today, monitoring in B6, clawkerd in B4+): gRPC clients are injected via the Factory, not wrapped.

**B2 reshapes the firewall admin surface with corrected per-container / global scope splits.** B1's proto conflated lifecycle boundaries (per-container `Install` overlapped with the per-container enrollment job `Enable` should own; per-container `Remove` was really a global teardown). B2 corrects this:

| RPC | Scope | Purpose | Replaces (B1) |
|-----|-------|---------|---------------|
| `FirewallInit` | global | Bring stack up (Envoy + CoreDNS) + attach BPF programs to host | *new* (was to be `FirewallEnsureStack`); absorbs the stack-up side of B1's per-container `Install` |
| `FirewallEnable(container_id)` | per-container | Idempotent enrollment: adds `container_id` to container_map routing. Drift-guarded (see INV-B2-016). | B1 `Install` (per-container attach) + B1 `Enable` (clear bypass flag) collapsed |
| `FirewallDisable(container_id)` | per-container | Remove from routing. Enrollment (BPF link) is maintained; only the container_map entry is removed so the BPF fast path exits to bypass. Re-enrollment via `FirewallEnable` is cheap. | B1 `Disable` (set bypass flag) — same outcome, cleaner mechanism |
| `FirewallBypass(container_id, timeout)` | per-container | Composite: `FirewallDisable` + CP dead-man timer that calls `FirewallEnable` on expiry. Inherits drift guard via Enable path. | B1 `Bypass` |
| `FirewallRemove` | global | Stack down (Envoy + CoreDNS) + detach all BPF programs + flush all rules, eBPF maps, and timers. Empty request. | B1 per-container `Remove` (re-scoped) + the `FirewallStopStack` RPC previously planned |
| `FirewallAddRules` | global | Add egress rules, regenerate configs, hot-reload stack. | *new* |
| `FirewallRemoveRules` | global | Remove egress rules, regenerate, hot-reload. | *new* |
| `FirewallListRules` | global | Read-only rule dump. | *new* |
| `FirewallReload` | global | Force regenerate + restart Envoy/CoreDNS from current rule state. | *new* |
| `FirewallStatus` | global | Health snapshot (stack running, envoy/coredns healthy, rule count, network state). | *new* |
| `FirewallRotateCA` | global | Regenerate MITM CA + per-domain certs. | *new* |
| `FirewallSyncRoutes` | global | Atomically replace global route_map. | B1 `SyncRoutes` (CP-internal now; rarely called externally) |
| `FirewallResolveHostname` | global | DNS lookup from CP netns. | B1 `ResolveHostname` |

Total: **13 RPCs**. All require the single `"admin"` scope. INV-009 (fail-closed on unmapped methods) preserved.

`clawker firewall up` → `FirewallInit`. `clawker firewall down` → `FirewallRemove`. CP lifecycle is separate: `clawker controlplane up/down/status` command group ships for CP break-glass control.

**AdminService's role in the CP's broader API design** (context for future branches, not new scope for B2): AdminService is the **CLI API** — the privileged, sensitive surface used exclusively by human operators via the `clawker` CLI. It is isolated and hardened: mTLS with CLI-CA signing, short-lived OAuth2 JWTs via `private_key_jwt`, localhost-only listener binding (`127.0.0.1:7443` with host port forward), single permanent `"admin"` scope. Branch 4 introduces a **completely separate** `AgentService` for clawkerd-in-managed-container callers, on its own TCP listener, with its own trust boundary (TB-003), per-agent CA, PKCE-bootstrapped mTLS, and `"agent:*"` scopes — primarily for agent → CP event reporting and CP → agent command dispatch via bidirectional streams. The single-`"admin"`-scope rule for AdminService is permanent precisely because AdminService has exactly one caller type (the human operator's CLI) and agents reach the CP through a completely different service. Fine-grained admin scopes ("admin:read" vs "admin:write") would not buy anything when the single caller already has root-level trust on the host. B2 does not implement AgentService; it locks the AdminService shape that B4+ builds alongside.

**Replacement, not paralleling.** B2 does not leave the old firewall daemon and manager running alongside their CP-side replacements. Every intermediate commit must still pass `make test` and produce a working `clawker run @`, but the old code paths do not linger into post-merge. Clean cutover landing as one PR per trunk-based development policy.

## Key Decisions (2026-04-14 session)

1. **Scope is narrow and concrete: dissolve the firewall daemon, flip the ownership axes above.** Network creation stays CLI-side via existing `whail.Engine.EnsureNetwork` primitives (already composed into `internal/docker.Client`). The CP uses the same primitive internally as a defensive guard before creating Envoy/CoreDNS. No new network helper is invented.
2. **`internal/firewall/` is deleted.** Not shrunk. Not kept as a semantic wrapper. Code moves CP-side; rules/cert/config-gen/network helpers migrate to `internal/controlplane/`. Commands call `f.AdminClient(ctx)` directly.
3. **One new Factory noun: `f.AdminClient(ctx) (adminv1.AdminServiceClient, error)`.** Lazy. First call invokes `controlplane.EnsureRunning`, then `auth.DialCPAdmin`. Subsequent calls return cached client (invalidated if `grpc.ClientConn` is not connected/connecting/idle). Tests override the closure. No `f.ControlPlane()` noun — bootstrap is a plain package function that takes a `*docker.Client` and config.
4. **CP watcher lives inside CP process.** Same polling algorithm as today's firewall daemon (30s poll, `missed_threshold=2`, 60s grace). CP exits cleanly on 0 agents. Docker marks container exited. Next `EnsureRunning` call creates a fresh CP container. Branch 3 will replace polling with Docker events subscription.
5. **Break-glass: `clawker controlplane up/down/status` ship in B2.** Explicit lifecycle control. `up` wraps `controlplane.EnsureRunning`. `down` stops CP container via `internal/docker.Client`. `status` queries CP health + reports Ory subsystem state.
6. **`clawker firewall up/down` are RPCs to the CP.** CP ≠ firewall. These bring Envoy/CoreDNS up/down and flush/clean eBPF routes in the stack (not the CP itself).
7. **CP uses `internal/docker.Client`** (composes whail with clawker label conventions + naming). Not raw moby. Firewall used raw moby to preserve leaf status — CP is orchestrator, not leaf. The exception list in `.claude/rules/docker-client.md` loses its `internal/firewall` entry; no exception is added for the CP.
8. **State paths stay at `cfg.FirewallDataSubdir()`.** CP bind-mounts RW (was RO in B1), Envoy/CoreDNS bind-mount RO (unchanged). Zero migration. `cfg.FirewallPIDFilePath` accessor deleted (no production caller left).
9. **Trunk stability by replacement, not paralleling.** B2 deletes the daemon and manager; does not keep them alongside. Every intermediate commit passes `make test` and produces a working `clawker run @`. The migration plan sequences phases so each leaves a viable tree.
10. **`cgroupDriver` detection moves CP-side into the firewall domain.** B1 put cgroup detection + `ebpfCgroupPath` host-side (inside `firewall.Manager`). Today's B1 wire contract carries the computed path in `InstallRequest.CgroupPath` — but the path is a firewall-internal detail; only the CP's eBPF code consumes it. B2 moves the computation into the firewall domain: `internal/controlplane/firewall/cgroup.go` contains `DetectCgroupDriver(ctx, docker)` and `EBPFCgroupPath(driver, containerID)`. `firewall.Handler` calls `DetectCgroupDriver` once at initialization (via DooD `docker.Client.SystemInfo`), caches the result, and resolves cgroup paths internally for every Install/Enable/Disable/Bypass RPC. The `cgroup_path` field is **removed** from `InstallRequest`, `EnableRequest`, `DisableRequest`, and `BypassRequest`. Alpha project, no B1 release shipped eBPF/CP → clean removal, no deprecation window. CLI sends only `container_id`.
11. **CP gets subpackage organization by domain, mirrored in the proto surface.** `internal/controlplane/` is the CP core (cross-cutting: bootstrap, auth, Ory subprocess, agent registry, watcher, gRPC server setup, CP container config, composite admin client). Domain subsystems live under `internal/controlplane/<domain>/`. B2 establishes the pattern with `internal/controlplane/firewall/`, which owns the **entire** firewall admin surface — both the B1 RPCs (`Install`, `Remove`, `Enable`, `Disable`, `Bypass`, `SyncRoutes`, `ResolveHostname`) and the 8 new B2 RPCs. Today's `internal/controlplane/admin_handler.go` (`AdminHandler`) is really a firewall handler under a generic name; it is **renamed and relocated** to `internal/controlplane/firewall/handler.go` as `firewall.Handler`, gaining 8 new methods for a total of 15. The existing `internal/controlplane/ebpf/` package moves to `internal/controlplane/firewall/ebpf/` — eBPF is strictly an egress-enforcement primitive and belongs with its domain. Envoy/CoreDNS config gen, certs, rules store, Stack, errors, status, coredns embed, and its binary asset are all firewall-domain and live in the subpackage. Future branches add `controlplane/monitor/`, `controlplane/hostproxy/`, `controlplane/clawkerd/` under the same pattern. Package naming: inside the `firewall` subpackage, the RPC server type is `firewall.Handler` (not `firewall.FirewallHandler` — package context disambiguates); the same applies to future sub-domains (`monitor.Handler`, etc.).

**Explicitly deferred (post-B2)**: CP hard-crash fallback. If the CP exits non-gracefully (SIGKILL, OOM, Docker daemon restart), running agents are left without egress enforcement until a new CP starts. Branch-2 does NOT ship the lockdown fallback. A later branch (after clawkerd exists in-container) will have clawkerd detect CP absence and install an iptables-based "no network" rule inside the managed container that holds until CP re-registers. B2 relies on INV-B2-013 to clean up any residual BPF state the next time CP starts, and on the alpha-project trust model (operator attention) to bridge the gap.

    The proto surface stays flat. **AdminService is the CLI admin API** — one flat surface for human operators doing privileged, sensitive operations. It is not "the firewall API": firewall is one of several domains the admin API covers, alongside future monitor, hostproxy, and clawkerd domains. Subpackage organization is a CP-side implementation detail invisible to clients. AdminService is hardened and isolated (mTLS via CLI-CA, `"admin"`-only scope, localhost-only listener) — strictly separated from the future `AgentService` (Branch 4) which serves clawkerd-in-managed-container callers on its own TCP listener with its own trust boundary (TB-003), per-agent CA, and distinct scope namespace. The separation is enforced at every layer: separate services, separate listeners, separate CAs, separate scope vocabularies. A compromised agent cannot invoke an AdminService method even with a valid agent mTLS cert because the admin mTLS listener will reject any cert not signed by the CLI CA. `api/admin/v1/admin.proto` defines a single `AdminService` with 13 firewall-domain methods today (scope-corrected from B1 — see table above); future branches extend with additional domain methods on the same service. RPC method names follow the `<Domain><Action>[<Object>]` convention to prevent cross-domain collisions: `FirewallInit`, `FirewallAddRules`, `FirewallEnable`, etc. B1's short-named methods (`Install`, `Remove`, `AddRules`) are **renamed AND scope-corrected** — alpha project, clean break, no deprecation. Message types may be split into per-domain files for readability (`admin.proto` for the service definition, `firewall_messages.proto` / future `monitor_messages.proto` for message types) and follow the same naming (`FirewallInitRequest`, `FirewallAddRulesRequest`, etc.), but the service stays single. Host-side, `f.AdminClient(ctx)` returns `adminv1.AdminServiceClient` directly; commands name the local binding `adminClient` (future branches add `f.AgentClient` with local name `agentClient`). Commands call flat, prefixed methods: `adminClient.FirewallAddRules(ctx, req)`, `adminClient.FirewallEnable(ctx, req)`, `adminClient.FirewallInit(ctx, req)`. No `.Firewall.` namespace at the caller — the domain split lives in the method name and in CP code layout, not in a nested client structure. CP side uses Go embedding to compose the service impl: `internal/controlplane/server.go` defines an `adminServer` struct embedding `*firewall.Handler` (and future `*monitor.Handler`, `*hostproxy.Handler`, etc.) — each domain handler contributes its prefixed methods to the full `AdminServiceServer` interface via method promotion.

## Current State Inventory

### `internal/firewall/` package (to be deleted)

| File | LoC | Role | Migration |
|------|-----|------|-----------|
| `firewall.go` | 103 | `FirewallManager` interface (15 methods — host-side domain contract, superseded by gRPC AdminService in B2), `FirewallStatus`, `HealthTimeoutError`, sentinels (`ErrEnvoyUnhealthy`, `ErrCoreDNSUnhealthy`, `ErrCPUnhealthy`) | **DELETE** the interface. Sentinels + `HealthTimeoutError` + status struct move to `internal/controlplane/firewall/errors.go` + `status.go` (CP-side internal types) |
| `types.go` | 17 | `EgressRulesFile` storage schema | **MOVE** to `internal/controlplane/firewall/rules_store.go` |
| `manager.go` | 1715 | `Manager` struct + 34 methods (entire Docker implementation) | **SPLIT** — see breakdown below. Split targets: CP-core (`bootstrap.go`, `cp_container.go`) + firewall subpkg (`handler.go`, `stack.go`, `cgroup.go`, `rules_store.go`). |
| `daemon.go` | 696 | `Daemon`, PID file, health loop (5s), watcher loop (30s), `EnsureDaemon`, `StopDaemon`, `IsDaemonRunning`, `WaitForDaemonExit`, BPF cleanup | **REPLACE** — health absorbed into CP `/healthz`; watcher → `internal/controlplane/watcher.go` (CP core — agent-count logic is cross-domain, not firewall-specific); PID file deleted |
| `envoy.go` | 944 | `GenerateEnvoyConfig`, `EnvoyPorts`, per-domain filter chain generation, LOGICAL_DNS cluster assembly | **MOVE** to `internal/controlplane/firewall/envoy_config.go` (unchanged logic, package rename) |
| `coredns.go` | 128 | `GenerateCorefile`, per-domain forward zones, catch-all NXDOMAIN | **MOVE** to `internal/controlplane/firewall/coredns_config.go` |
| `certs.go` | 337 | `EnsureCA`, `GenerateDomainCert`, `RegenerateDomainCerts`, `RotateCA` | **MOVE** to `internal/controlplane/firewall/certs.go` (MITM certs — firewall domain) |
| `rules.go` | 161 | `NewRulesStore`, `ValidateDst`, `normalizeRule`, `normalizeAndDedup`, `ruleKey`, `normalizeDomain`, `isIPOrCIDR` | **MOVE** to `internal/controlplane/firewall/rules_store.go` + `ProjectRules(cfg)` from `manager.go` |
| `network.go` | 95 | `NetworkInfo`, `discoverNetwork`, `ensureNetwork`, `computeStaticIP` | **MOVE** to `internal/controlplane/firewall/network.go` (swap raw moby for `*docker.Client`; drop `ensureNetwork` — whail's `EnsureNetwork` container option handles it) |
| `cp_embed.go` | 22 | `//go:embed assets/clawker-cp` | **MOVE** to `internal/controlplane/embed_cp.go` + `assets/clawker-cp` binary (CP's own binary — CP core) |
| `ebpf_embed.go` | 14 | `//go:embed assets/ebpf-manager` | **MOVE** to `internal/controlplane/embed_ebpf.go` + binary (break-glass eBPF CLI bundled in CP image — CP core) |
| `coredns_embed.go` | 15 | `//go:embed assets/coredns-clawker` | **MOVE** to `internal/controlplane/firewall/embed_coredns.go` + binary (CoreDNS is firewall-domain) |
| `mocks/manager_mock.go` | — | moq-generated `FirewallManagerMock` | **DELETE** (no interface to mock; tests use `AdminServiceClient` mock + `firewall.Handler` direct instantiation) |
| `testdata/corefile_basic.golden` | — | Corefile golden test fixture | **MOVE** to `internal/controlplane/firewall/testdata/` |
| `assets/{clawker-cp,ebpf-manager}` | — | Linux binaries (gitignored) | **MOVE** to `internal/controlplane/assets/` |
| `assets/coredns-clawker` | — | Linux binary | **MOVE** to `internal/controlplane/firewall/assets/` |
| `*_test.go` (8 files, ~3000 LoC) | — | Unit tests | **SPLIT** — manager/daemon tests replaced by handler/Stack/watcher tests (all in firewall subpkg); envoy/coredns/certs/rules/network tests move with their sources |

#### `manager.go` method-level breakdown (1715 LoC → distributed)

| Method group | Approx LoC | Methods | Destination |
|--------------|-----------|---------|-------------|
| Stack lifecycle | 400 | `EnsureRunning`, `Stop`, `IsRunning`, `WaitForHealthy`, `ensureContainer`, `runContainer`, `ensureImage`, `ensureCPImage`, `ensureCorednsImage`, `ensureEmbeddedImage`, `embeddedBuildContext`, `stopAndRemove`, `isContainerRunning`, `isContainerRunningE`, `restartContainer`, `ensureConfigs`, `regenerateAndRestart` | **SPLIT**: host-side `controlplane.EnsureRunning` handles CP container lifecycle (CP core). CP-side `firewall.Stack` handles Envoy + CoreDNS lifecycle. `firewall.Handler` calls `Stack` methods from RPCs. `ensureCPImage` + embedded image spec for CP stays host-side (CP can't build its own image). `ensureCorednsImage` + embedded coredns spec move CP-side into `firewall.Stack`. |
| Rule management | 250 | `AddRules`, `RemoveRules`, `Reload`, `List`, `addRulesToStore`, `removeRulesFromStore`, `ProjectRules` | **MOVE** to `firewall.Handler` (RPC methods). `ProjectRules(cfg)` helper moves to `internal/controlplane/firewall/rules_store.go` and is called CLI-side in `BootstrapServicesPostStart` to compose the initial rule set, then sent via `AddRules` RPC. |
| Per-container gRPC dispatch | 300 | `Enable`, `Disable`, `Bypass`, `InstallFirewall`, `resolveContainerID`, `isAllHex`, `touchSignalFile`, `touchSignalFileImpl` | **DELETE host-side**. CP-side `firewall.Handler` already implements `Install/Enable/Disable/Bypass` (B1 — moved from `admin_handler.go` in B2). `cgroupDriver` detection + `ebpfCgroupPath` + `resolveContainerID` + `isAllHex` move to `internal/controlplane/firewall/cgroup.go` (CP-side); `firewall.Handler` resolves cgroup paths internally for each RPC. `touchSignalFile` moves to `firewall.Handler` (CP has Docker exec via DooD; no reason to split this helper host-side when the caller is CP-side). |
| Status / introspection | 100 | `Status`, `EnvoyIP`, `CoreDNSIP`, `NetCIDR`, `envoyPorts` | **MOVE** to `firewall.Handler.FirewallStatus` RPC |
| Container specs | 200 | `envoyContainerConfig`, `corednsContainerConfig`, `cpContainerConfig`, `cpStaticIP`, `computeGateway`, `containerSpec` struct, `embeddedImageSpec`, `embeddedBinary` | **SPLIT**: `cpContainerConfig` + `cpStaticIP` stay host-side (CP core — used by `controlplane.EnsureRunning` + `BuildCPContainerConfig`). `envoyContainerConfig`/`corednsContainerConfig`/`containerSpec`/`computeGateway` move CP-side into `firewall.Stack`. CP's own `embeddedImageSpec` stays host-side; CoreDNS's moves CP-side. |
| CP gRPC client | 150 | `cpClient`, `Close`, `waitForCPReady`, `waitForCPReadyImpl`, `syncRoutes` | **DELETE** — `f.AdminClient` closure replaces `cpClient` (the existing B1 method on `firewall.Manager`). `waitForCPReady` moves into `controlplane.EnsureRunning` (CP core bootstrap). `syncRoutes` becomes internal to `firewall.Handler` (called on rule-change RPCs). |
| Test seams | 30 | `cgroupDriverFn`, `touchSignalFileFn`, `waitForCPReadyFn`, `adminClientFn` | **DELETE** all four. Seams collapse: `cgroupDriver` → pure function in `firewall/cgroup.go` (testable without injection); `touchSignalFile` → method on `firewall.Handler` with `docker.Client` already injected via constructor; `waitForCPReady` → internal to `controlplane.EnsureRunning`; `adminClientFn` → `f.AdminClient` mock injection via Factory. |
| Helpers | 100 | `normalizeRule`, `normalizeAndDedup`, `ruleKey`, `normalizeDomain`, `isIPOrCIDR`, `ValidateDst` | Already in `rules.go` — moves with it to `internal/controlplane/firewall/rules_store.go` |

### `internal/cmd/firewall/` commands (to be rewired)

All 12 subcommands preserved with identical UX. Only transport changes (gRPC under the hood).

| File | LoC | Command | Uses `f.Firewall`? | Uses `f.Config`? | After B2 |
|------|-----|---------|---|---|---------|
| `firewall.go` | 51 | parent | — | — | Keep. Drop `serve` from subcommand list. |
| `up.go` | 138 | `up`, `serve` (hidden) | No | Yes (daemon bootstrap) | `up` → `adminClient.FirewallInit`. **DELETE** `serve` subcommand entirely (was hidden daemon entrypoint). |
| `down.go` | 109 | `down` | Yes | Yes (PID path) | `down` → `adminClient.FirewallRemove` (global teardown: Envoy + CoreDNS + eBPF flush). Drop PID file handling. |
| `status.go` | 126 | `status` | Yes | — | → `adminClient.FirewallStatus` |
| `list.go` | 139 | `list`, `ls` | Yes | — | → `adminClient.FirewallListRules` |
| `add.go` | 82 | `add` | Yes | — | → `adminClient.FirewallAddRules` |
| `remove.go` | 119 | `remove` | Yes | — | → `adminClient.FirewallRemoveRules`. Tab-completion helper `domainCompletions` calls `adminClient.FirewallListRules`. |
| `reload.go` | 60 | `reload` | Yes | — | → `adminClient.FirewallReload` |
| `enable.go` | 85 | `enable` | Yes | — | → `adminClient.FirewallEnable` — B2 semantic: idempotent per-container enroll into `container_map` with drift guard (INV-B2-016). Absorbs B1 per-container `Install` + `Enable`. |
| `disable.go` | 85 | `disable` | Yes | — | → `adminClient.FirewallDisable` — B2 semantic: remove container_id from routing (delete container_map entry); BPF links preserved. |
| `bypass.go` | 214 | `bypass` | Yes | — | → `adminClient.FirewallBypass` — B2 semantic: timed Disable + CP dead-man timer that calls Enable on expiry. Drift guard inherited from Enable. |
| `bypass_dash.go` | 122 | bypass helper | Yes | — | → `adminClient.FirewallBypass` (B1 RPC renamed) |
| `rotate_ca.go` | 82 | `rotate-ca` | Yes | Yes (cert dir) | → `adminClient.FirewallRotateCA`. Drop direct `fwpkg.RotateCA` + cert dir access. |

### Factory + wiring (`internal/cmdutil`, `internal/cmd/factory`)

| File | Current | B2 delta |
|------|---------|----------|
| `internal/cmdutil/factory.go` | Field `Firewall func(context.Context) (firewall.FirewallManager, error)` | **Delete** `Firewall` field. **Add** `AdminClient func(context.Context) (adminv1.AdminServiceClient, error)`. |
| `internal/cmd/factory/default.go` | `firewallFunc(f)` builds raw moby client + `firewall.NewManager`; wires `f.Firewall` | **Delete** `firewallFunc`. **Add** `adminClientFunc(f)`: lazy closure that calls `controlplane.EnsureRunning` then `auth.DialCPAdmin`, caches client, re-dials on stale `grpc.ClientConn`. **Drop** `mobyclient` import (exception for firewall goes away). |
| `internal/cmd/factory/CLAUDE.md` | Documents `firewallFunc` + its moby exception | Update to describe `adminClientFunc`. Remove firewall exception prose. |

### `BootstrapServicesPostStart` flow

**Current** (`internal/cmd/container/shared/container_start.go:90`):
```
if settings.Firewall.FirewallEnabled():
  fwMgr := cmdOpts.Firewall(ctx)
  fwMgr.AddRules(ctx, ProjectRules(cfg))
  firewall.EnsureDaemon(cfg, log)              ← spawn PID-file daemon
  fwMgr.WaitForHealthy(ctx)                    ← 90s timeout
  fwMgr.InstallFirewall(ctx, containerID)      ← gRPC (B1)
```

**After B2**:
```
if settings.Firewall.FirewallEnabled():
  adminClient := cmdOpts.AdminClient(ctx)             ← triggers EnsureRunning internally
  adminClient.FirewallInit(ctx, &FirewallInitRequest{})  ← idempotent: stack up + BPF attached
  adminClient.FirewallAddRules(ctx, &FirewallAddRulesRequest{Rules: ProjectRules(cfg)})  ← sync; hot-reloads stack
  adminClient.FirewallEnable(ctx, &FirewallEnableRequest{ContainerId: container, Config: cfg})
                                                       ← drift-guarded per-container enroll (INV-B2-016)
```

Four calls become one lazy noun fetch + three RPCs. No PID file. No 90s external wait (CP-internal readiness gating handles it; `AddRules` returns only when stack is healthy post-restart). `FirewallInit` is cheap if the stack is already up — Handler performs idempotent presence check.

## Blast Radius

**39 files import `internal/firewall/` or reference firewall symbols.** Every one requires a touch.

| File | Symbol(s) used | B2 change |
|------|---------------|-----------|
| `internal/cmdutil/factory.go` | `firewall.FirewallManager` | Delete field, add `AdminClient` |
| `internal/cmd/factory/default.go` | `firewall.NewManager`, `firewall.FirewallManager`, raw moby import | Delete `firewallFunc`, add `adminClientFunc` |
| `internal/cmd/firewall/*.go` (all 13 command files) | `fw.FirewallManager`, `fwpkg.RotateCA`, `fw.EnsureDaemon`, `fw.IsDaemonRunning`, `fw.StopDaemon`, `fw.WaitForDaemonExit`, `fw.NewDaemon` | Rewire per commands table above |
| `internal/cmd/container/shared/container_start.go` | `firewall.ProjectRules`, `firewall.EnsureDaemon`, `firewall.FirewallManager` | Replace with AdminClient calls (see flow above); `ProjectRules` becomes `controlplane.ProjectRules` |
| `internal/cmd/container/start/start.go`, `stop/stop.go`, `run/run.go`, `restart/restart.go`, `remove/remove.go` | `f.Firewall` reference | Swap to `f.AdminClient` where firewall ops needed; drop entirely where only present for struct completeness |
| `internal/cmd/container/stop/stop_test.go`, `remove/remove_test.go` | `firewall.FirewallManager` mock | Swap for `AdminServiceClient` mock |
| `internal/cmd/loop/shared/lifecycle.go`, `loop/tasks/tasks.go`, `loop/iterate/iterate.go` | `f.Firewall` | Swap for `f.AdminClient` |
| `internal/config/consts.go` | `FirewallPIDFilePath` accessor | **Delete** (no production caller) |
| `internal/config/config.go` | `FirewallDataSubdir`, `FirewallCertSubdir`, `FirewallLogFilePath`, `EgressRulesFileName` accessors | Keep data/cert/rules accessors. Delete `FirewallPIDFilePath` + (per OQ-B2-004) probably `FirewallLogFilePath`. |
| `internal/config/mocks/config_mock.go`, `mocks/stubs.go` | `FirewallPIDFilePath` mock | Regenerate moq after interface change |
| `internal/hostproxy/daemon.go` | Confirm: likely imports a config accessor, not firewall package | Audit import; if `internal/firewall` import exists, swap to `internal/controlplane/firewall` or remove |
| `internal/controlplane/ebpf/types.go` | Grep hit — confirm whether real import or false positive | Moves with package to `internal/controlplane/firewall/ebpf/`; audit internal firewall-package references and drop if present |
| `internal/dnsbpf/` | Imports `internal/controlplane/ebpf` for dnsbpf plugin integration (BPF types, map schemas) | Import path changes to `internal/controlplane/firewall/ebpf` |
| `cmd/clawker-cp/main.go` | Instantiates `ebpf.Manager`, wires handler, starts Ory + watcher | Import path update for ebpf → `controlplane/firewall/ebpf`. Handler construction uses `firewall.NewHandler` (was `NewAdminHandler`). Add `firewall.NewStack` + `controlplane.NewAgentWatcher` wiring. |
| `cmd/coredns-clawker/main.go` | Standalone CoreDNS binary (no firewall-package import expected) | Audit grep hit; if imports exist they were likely ebpf types for the dnsbpf plugin — repoint to `controlplane/firewall/ebpf` |
| `test/e2e/firewall_test.go` | E2E against `FirewallManager` interface | Rewrite to exercise gRPC path via `AdminClient` |
| `test/e2e/harness/factory.go` | `f.Firewall` wiring | Replace with `f.AdminClient` test wiring |
| `test/e2e/preset_builds_test.go` | Firewall references (container assertions) | Update wiring, keep assertions |
| All `internal/firewall/*_test.go` | — | Tests migrate with their source files. Manager/daemon tests are rewritten as handler/Stack/watcher tests. |

**CLAUDE.md / rules files affected**:
- `internal/firewall/CLAUDE.md` — **delete**
- `internal/cmd/firewall/CLAUDE.md` — update subcommand table, drop `serve`, note gRPC transport
- `internal/cmd/factory/CLAUDE.md` — replace `firewallFunc` prose with `adminClientFunc`
- `internal/cmdutil/CLAUDE.md` — update Factory field list
- `internal/controlplane/CLAUDE.md` — update core section to describe subpackage organization, `AgentWatcher`, `bootstrap`, removal of `admin_handler.go` (now in firewall subpkg). Remove firewall-specific prose (moved to new subpkg CLAUDE.md).
- `internal/controlplane/firewall/CLAUDE.md` — **NEW**: `Handler` (15 RPCs), `Stack`, `cgroup` helpers, `rules_store`, `certs`, Envoy/CoreDNS config generators, `ebpf/` sub-subpackage, mount-mode invariants, testing patterns
- `.claude/rules/docker-client.md` — remove `internal/firewall` from exception list
- `.claude/rules/envoy.md` — update paths from `internal/firewall/envoy.go` → `internal/controlplane/envoy_config.go`
- `.claude/rules/dependency-placement.md` — remove `internal/firewall` row from Current Package Layout table
- `.claude/docs/ARCHITECTURE.md` — update firewall subsystem section (now CP-owned)
- `.claude/docs/KEY-CONCEPTS.md` — remove `FirewallManager`; add `firewall.Handler`, `firewall.Stack`, `firewall.EBPFCgroupPath`, `controlplane.AgentWatcher`, `f.AdminClient`
- `.correctless/specs/cp-initiative/CLAUDE.md` — update Current State (firewall gone, ownership inverted)
- `docs/threat-model.mdx` — expand TB-002; update DooD language; MITM CA now CP-owned
- `docs/cli-reference/` — regenerate via `make`; new `controlplane up/down/status` commands present

## Target Structure

### `internal/controlplane/` (CP core) after B2

Cross-cutting CP concerns. Domain-agnostic. No firewall-specific code.

```
internal/controlplane/
├── authz.go                   # B1: gRPC AuthInterceptor — cross-domain (validates scope for all RPCs)
├── authz_test.go              # B1: enumerate-all-methods test (now covers 15 firewall RPCs + future cross-domain)
├── bootstrap.go               # NEW: host-side EnsureRunning, Stop (CP container lifecycle)
├── bootstrap_test.go          # NEW
├── cp_container.go            # B1: BuildCPContainerConfig — MODIFY (RW mount FirewallDataSubdir)
├── container_config_test.go   # B1 (+ RW-mount assertion)
├── embed_cp.go                # MOVED from firewall/cp_embed.go (CP's own binary)
├── embed_ebpf.go              # MOVED from firewall/ebpf_embed.go (break-glass ebpf-manager binary bundled in CP image)
├── assets/
│   ├── clawker-cp             # MOVED (gitignored, built by make)
│   └── ebpf-manager           # MOVED
├── hydra_client.go            # B1: RegisterCLIClient + registered-methods set (all AdminService methods → "admin" scope uniformly)
├── hydra_client_test.go       # B1
├── ory_configs.go             # B1: Hydra/Kratos/Oathkeeper YAML generation (cross-domain auth infra)
├── registry.go                # B1: agent registry (future clawkerd home — cross-domain for now)
├── registry_test.go           # B1
├── server.go                  # B1: gRPC server setup — MODIFY to register firewall.Handler
├── startup.go                 # B1: CPStartupOrchestrator — MODIFY (init Stack + firewall.Handler + Watcher)
├── subprocess.go              # B1: Ory subprocess lifecycle
├── subprocess_test.go         # B1
├── lifecycle_test.go          # B1 (+ watcher-triggered exit test)
├── grpc_mtls_test.go          # B1
├── grpc_test_helpers_test.go  # B1
├── watcher.go                 # NEW: AgentWatcher (30s poll, missed_threshold=2). Cross-domain: "no agents" signals CP shutdown regardless of which subsystems are active.
├── watcher_test.go            # NEW
├── firewall/                  # NEW SUBPACKAGE — see next tree
├── mocks/                     # B1 + regenerated for new types
├── CLAUDE.md                  # UPDATED
```

### `internal/controlplane/firewall/` (firewall domain subpackage) after B2

All firewall-specific code: RPC handler (13 scope-corrected methods — see Context table), Envoy/CoreDNS configs, CA, certs, Stack, rules store, cgroup helpers, eBPF primitives.

```
internal/controlplane/firewall/
├── handler.go                 # RENAMED+MOVED from controlplane/admin_handler.go. Type: firewall.Handler. Implements the 13 scope-corrected firewall-domain AdminService RPCs.
├── handler_test.go            # MOVED+EXPANDED from admin_handler_test.go
├── stack.go                   # NEW: Envoy + CoreDNS lifecycle via *docker.Client + DooD
├── stack_test.go              # NEW
├── cgroup.go                  # NEW: DetectCgroupDriver (via DooD SystemInfo), EBPFCgroupPath, ResolveContainerID (moved from firewall/manager.go; now CP-side)
├── cgroup_test.go             # NEW
├── certs.go                   # MOVED from firewall/certs.go (MITM CA + domain certs)
├── certs_test.go              # MOVED
├── coredns_config.go          # MOVED from firewall/coredns.go
├── coredns_config_test.go     # MOVED
├── embed_coredns.go           # MOVED from firewall/coredns_embed.go
├── assets/
│   └── coredns-clawker        # MOVED
├── envoy_config.go            # MOVED from firewall/envoy.go
├── envoy_config_test.go       # MOVED
├── errors.go                  # NEW: ErrEnvoyUnhealthy, ErrCoreDNSUnhealthy, ErrCPUnhealthy, HealthTimeoutError (moved from firewall/firewall.go)
├── network.go                 # MOVED from firewall/network.go (uses *docker.Client, drop ensureNetwork)
├── network_test.go            # MOVED
├── rules_store.go             # MOVED from firewall/rules.go + types.go (+ ProjectRules from manager.go)
├── rules_store_test.go        # MOVED
├── status.go                  # NEW: internal Status struct (feeds FirewallStatus RPC response)
├── testdata/
│   └── corefile_basic.golden  # MOVED
├── ebpf/                      # MOVED from internal/controlplane/ebpf/
│   ├── manager.go             # Same — Load, Install, Remove, Enable, Disable, SyncRoutes
│   ├── types.go               # PinPath, ContainerConfig, DomainHash, CgroupID, etc.
│   ├── cmd/                   # (if exists today — break-glass CLI)
│   ├── mocks/                 # EBPFManagerMock (moq)
│   └── ...
├── mocks/                     # moq-generated for firewall.Handler + any firewall-scoped interfaces
└── CLAUDE.md                  # NEW
```

**Importers updated for `ebpf/` relocation**: `internal/dnsbpf/` (imports ebpf types for dnsbpf plugin integration — changes import path), `cmd/clawker-cp/main.go` (ebpf.Manager instantiation), `internal/controlplane/firewall/handler.go` (calls ebpf.Manager methods). No cycles: `controlplane` → `controlplane/firewall` → `controlplane/firewall/ebpf`.

### `internal/cmd/controlplane/` (NEW)

```
internal/cmd/controlplane/
├── controlplane.go    # NewCmdControlPlane(f) — parent, registers up/down/status
├── up.go              # NewCmdUp — wraps controlplane.EnsureRunning
├── up_test.go
├── down.go            # NewCmdDown — stops CP container via f.Client + warning
├── down_test.go
├── status.go          # NewCmdStatus — /healthz probe + best-effort FirewallStatus
├── status_test.go
└── CLAUDE.md
```

Registered in root command's `AddCommand` list.

### `api/admin/v1/` after B2

Existing `admin.proto` gains 8 RPCs + supporting messages (full listing in Design § 8).

## Design

### 1. New Factory field

```go
// internal/cmdutil/factory.go
type Factory struct {
    // ... eager fields unchanged ...

    // Lazy nouns
    Client         func(context.Context) (*docker.Client, error)
    Config         func() (config.Config, error)
    Logger         func() (*logger.Logger, error)
    ProjectManager func() (project.ProjectManager, error)
    GitManager     func() (*git.GitManager, error)
    HostProxy      func() hostproxy.HostProxyService
    SocketBridge   func() socketbridge.SocketBridgeManager
    Prompter       func() *prompter.Prompter

    // AdminClient is the gRPC client to the control plane's AdminService
    // — the umbrella admin API covering all CP administrative domains
    // (firewall today; monitor, hostproxy, clawkerd in future branches).
    // Lazy: first call invokes controlplane.EnsureRunning (ensures CP is
    // running and /healthz is green), then auth.DialCPAdmin (mTLS +
    // OAuth2 handshake). Subsequent calls return the cached client.
    // Cache is invalidated if the underlying grpc.ClientConn is no
    // longer Ready/Connecting/Idle. Tests inject a stub by overriding
    // this closure.
    //
    // Callers see a flat API surface with domain-prefixed method names:
    // adminClient.FirewallAddRules(ctx, req), adminClient.FirewallEnable(ctx, req),
    // future adminClient.MonitorListMetrics(ctx, req), etc. No nested client
    // structure. The firewall subpackage organization is CP-internal only.
    AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
}
```

`Firewall` field deleted.

### 2. `adminClientFunc` wiring

```go
// internal/cmd/factory/default.go
//
// ensureRunning is a package-level seam so that tests can stub the
// bootstrap call (TA-7 / command tests for `clawker run @` etc. should
// not spin up a real CP). Production wiring assigns the real function;
// tests that wire the Factory directly assign a no-op.
var ensureRunning = controlplane.EnsureRunning

func adminClientFunc(f *cmdutil.Factory) func(context.Context) (adminv1.AdminServiceClient, error) {
    var (
        mu     sync.Mutex
        conn   *grpc.ClientConn
        client adminv1.AdminServiceClient
    )
    return func(ctx context.Context) (adminv1.AdminServiceClient, error) {
        mu.Lock()
        defer mu.Unlock()

        // Return cached client if connection is healthy.
        // grpc.ClientConn has its own reconnect-with-backoff on transient
        // failures — for long-running callers (loop, monitor, bypass
        // dashboard) the cached client auto-heals. We only rebuild when
        // state is TransientFailure or Shutdown (permanent).
        if conn != nil {
            s := conn.GetState()
            if s == connectivity.Ready || s == connectivity.Connecting || s == connectivity.Idle {
                return client, nil
            }
            _ = conn.Close()
            conn = nil
            client = nil
        }

        cfg, err := f.Config()
        if err != nil { return nil, fmt.Errorf("admin client: config: %w", err) }
        log, err := f.Logger()
        if err != nil { return nil, fmt.Errorf("admin client: logger: %w", err) }
        dc, err := f.Client(ctx)
        if err != nil { return nil, fmt.Errorf("admin client: docker: %w", err) }

        // Bootstrap happens exactly once per cached-connection lifetime.
        // After the first successful Dial, subsequent f.AdminClient(ctx)
        // calls skip this branch entirely. Long-running CLI processes
        // (loop, monitor) benefit — no repeated Docker pings.
        if err := ensureRunning(ctx, dc, cfg, log); err != nil {
            return nil, fmt.Errorf("admin client: ensure control plane: %w", err)
        }

        // Dial with gRPC keepalive so stale NAT/firewall paths are
        // detected before the next RPC. Values match CP server-side
        // (configured in server.go B1).
        newConn, err := auth.DialCPAdmin(ctx, cfg,
            grpc.WithKeepaliveParams(keepalive.ClientParameters{
                Time:                30 * time.Second,
                Timeout:             10 * time.Second,
                PermitWithoutStream: false,
            }),
        )
        if err != nil {
            return nil, fmt.Errorf("admin client: dial: %w", err)
        }
        conn = newConn
        client = adminv1.NewAdminServiceClient(conn)
        return client, nil
    }
}
```

**Test strategy for command tests (TA-7)**: `f.AdminClient` is a closure on the Factory — command tests assign a stub directly (`f.AdminClient = func(ctx context.Context) (adminv1.AdminServiceClient, error) { return mockAdminClient, nil }`) and never touch `adminClientFunc`. For tests that need to exercise the real closure (factory integration tests), swap the package-level `ensureRunning` var via a `t.Cleanup`-protected assignment. This mirrors the existing pattern in `internal/cmd/factory/default_test.go`.

### 3. `controlplane.EnsureRunning` signature + steps

```go
// internal/controlplane/bootstrap.go

// EnsureRunning is the host-side entry point for bringing up the CP.
// Idempotent and concurrency-safe. Returns nil if the CP is running
// and /healthz is green.
//
// Steps (in order):
//   1. Ensure auth material (CLI CA, signing key, server cert).
//   2. Ensure CP image present (build from embedded binaries if missing).
//   3. Create CP container via docker.Client.ContainerCreate with
//      ContainerCreateOptions.EnsureNetwork set so whail materializes
//      clawker-net on demand (or reuses existing).
//   4. Start CP container.
//   5. Poll HTTP /healthz on 127.0.0.1:<HealthPort> until 200 or timeout.
//
// On partial failure (e.g., container created but /healthz timed out),
// the next call observes the stopped/unhealthy container and reconciles.
func EnsureRunning(ctx context.Context, docker *docker.Client, cfg config.Config, log *logger.Logger) error

// Stop removes the CP container. Used by `clawker controlplane down`.
// Does NOT stop Envoy or CoreDNS — callers who want those torn down
// must call FirewallHandler.StopFirewallStack via gRPC first.
func Stop(ctx context.Context, docker *docker.Client, cfg config.Config, log *logger.Logger) error
```

### 4. `Stack` type (firewall subpackage)

```go
// internal/controlplane/firewall/stack.go
package firewall

// Stack manages the Envoy + CoreDNS container pair via DooD from
// inside the CP. Owned by firewall.Handler; not exposed externally.
type Stack struct {
    docker *docker.Client
    cfg    config.Config
    log    *logger.Logger
    store  *storage.Store[EgressRulesFile]
}

func NewStack(docker *docker.Client, cfg config.Config, log *logger.Logger, store *storage.Store[EgressRulesFile]) *Stack

// EnsureRunning starts Envoy + CoreDNS if not running. Idempotent.
// Generates envoy.yaml + Corefile from current rules store before start.
// Calls docker.EnsureNetwork internally as defensive guard.
func (s *Stack) EnsureRunning(ctx context.Context) error

// Stop removes Envoy + CoreDNS. Leaves eBPF state intact.
func (s *Stack) Stop(ctx context.Context) error

// Reload regenerates configs, restarts Envoy + CoreDNS, waits for healthy.
func (s *Stack) Reload(ctx context.Context) error

// WaitForHealthy polls Envoy + CoreDNS health via their internal IPs
// on clawker-net (CP shares the network). Returns HealthTimeoutError
// on deadline.
func (s *Stack) WaitForHealthy(ctx context.Context) error

// Status returns aggregated health + network state.
func (s *Stack) Status(ctx context.Context) (*Status, error)  // Status type is firewall.Status (internal)

func (s *Stack) EnvoyIP() string
func (s *Stack) CoreDNSIP() string
func (s *Stack) NetworkID() string
func (s *Stack) CIDR() string
```

### 5. `firewall.Handler` (domain handler — contributes 13 scope-corrected methods to AdminService via embedding)

B1 had `AdminHandler` in `internal/controlplane/admin_handler.go` implementing 7 RPCs (per-container `Install/Remove/Enable/Disable/Bypass` + global `SyncRoutes/ResolveHostname`). B2 moves + renames it to `internal/controlplane/firewall/handler.go` as `firewall.Handler`, scope-corrects the surface, and adds the rule-CRUD + lifecycle RPCs — 13 methods total (see Context table). The handler does NOT directly register as an `AdminServiceServer` — instead, CP core's `adminServer` struct embeds `*firewall.Handler` and Go method promotion surfaces all 13 methods on the composite. This makes the single-AdminService + domain-subpackage split work cleanly: handlers own their domain methods; composition stitches them together at the gRPC surface.

```go
// internal/controlplane/firewall/handler.go
package firewall

type Handler struct {
    adminv1.UnimplementedAdminServiceServer
    docker       *docker.Client   // for DooD ops + SystemInfo (cgroup driver)
    ebpf         ebpf.Manager
    stack        *Stack
    store        *storage.Store[EgressRulesFile]
    cfg          config.Config
    log          *logger.Logger

    cgroupDriver string          // cached from DetectCgroupDriver at init

    mu sync.Mutex // serializes AddRules/RemoveRules/Reload/RotateCA (see BND-B2-005)
}

func NewHandler(
    ctx context.Context,
    docker *docker.Client,
    ebpfMgr ebpf.Manager,
    stack *Stack,
    store *storage.Store[EgressRulesFile],
    cfg config.Config,
    log *logger.Logger,
) (*Handler, error) {
    driver, err := DetectCgroupDriver(ctx, docker)
    if err != nil {
        return nil, fmt.Errorf("detecting cgroup driver: %w", err)
    }
    return &Handler{docker: docker, ebpf: ebpfMgr, stack: stack, store: store, cfg: cfg, log: log, cgroupDriver: driver}, nil
}

// Global lifecycle
func (h *Handler) FirewallInit(ctx context.Context, req *adminv1.FirewallInitRequest) (*adminv1.FirewallInitResponse, error)
func (h *Handler) FirewallRemove(ctx context.Context, req *adminv1.FirewallRemoveRequest) (*adminv1.FirewallRemoveResponse, error)

// Per-container enrollment (drift-guarded — see INV-B2-016)
func (h *Handler) FirewallEnable(ctx context.Context, req *adminv1.FirewallEnableRequest) (*adminv1.FirewallEnableResponse, error)
func (h *Handler) FirewallDisable(ctx context.Context, req *adminv1.FirewallDisableRequest) (*adminv1.FirewallDisableResponse, error)
func (h *Handler) FirewallBypass(ctx context.Context, req *adminv1.FirewallBypassRequest) (*adminv1.FirewallBypassResponse, error)

// Rule CRUD + ops (global)
func (h *Handler) FirewallAddRules(ctx context.Context, req *adminv1.FirewallAddRulesRequest) (*adminv1.FirewallAddRulesResponse, error)
func (h *Handler) FirewallRemoveRules(ctx context.Context, req *adminv1.FirewallRemoveRulesRequest) (*adminv1.FirewallRemoveRulesResponse, error)
func (h *Handler) FirewallListRules(ctx context.Context, req *adminv1.FirewallListRulesRequest) (*adminv1.FirewallListRulesResponse, error)
func (h *Handler) FirewallReload(ctx context.Context, req *adminv1.FirewallReloadRequest) (*adminv1.FirewallReloadResponse, error)
func (h *Handler) FirewallStatus(ctx context.Context, req *adminv1.FirewallStatusRequest) (*adminv1.FirewallStatusResponse, error)
func (h *Handler) FirewallRotateCA(ctx context.Context, req *adminv1.FirewallRotateCARequest) (*adminv1.FirewallRotateCAResponse, error)

// Utilities (global)
func (h *Handler) FirewallSyncRoutes(ctx context.Context, req *adminv1.FirewallSyncRoutesRequest) (*adminv1.FirewallSyncRoutesResponse, error)
func (h *Handler) FirewallResolveHostname(ctx context.Context, req *adminv1.FirewallResolveHostnameRequest) (*adminv1.FirewallResolveHostnameResponse, error)
```

**Semantic corrections from B1**: `FirewallEnable` absorbs B1 `Install`'s per-container enroll role; there is no separate `FirewallInstall` RPC. `FirewallRemove` is global (was per-container in B1 proto). `FirewallDisable` removes the container_map entry rather than toggling a separate `bypass_map` flag — the BPF fast path already exits to bypass when container_map lookup misses, so the two mechanisms converge on identical outcomes and the simpler one wins. `FirewallBypass` composes `Disable` + timed `Enable`; the existing CP-side dead-man timer + Docker-backed drift resolution (admin_handler.go:267–311 `resolveBypassCgroupID`) is preserved and extended to direct `FirewallEnable` calls (new guard — B1 had this only on the bypass timer fire path).

In `FirewallEnable/Disable/Bypass`, the Handler resolves the cgroup path from `container_id` via Docker API (drift-guarded — INV-B2-016), not via a CLI-supplied path. No `cgroup_path` field on any request — CLI sends only `container_id`. `FirewallInit` and `FirewallRemove` take no container_id (global).

Registered in `server.go` via composite:

```go
// internal/controlplane/server.go
package controlplane

type adminServer struct {
    adminv1.UnimplementedAdminServiceServer
    *firewall.Handler  // promotes all 15 firewall methods to the composite
    // future: *monitor.Handler, *hostproxy.Handler, *clawkerd.Handler
}

var _ adminv1.AdminServiceServer = (*adminServer)(nil)  // compile-time check

// Registration:
srv := &adminServer{Handler: fwHandler}
adminv1.RegisterAdminServiceServer(grpcServer, srv)
```

Callers invoke the admin API with flat, domain-prefixed method names: `adminClient.FirewallAddRules(...)`, `adminClient.FirewallEnable(...)`, future `adminClient.MonitorListMetrics(...)`. CP internals stay cleanly split by domain via the embedded handlers. Method-name collisions across domains cannot occur because every method carries its domain prefix (`Firewall*`, future `Monitor*`, `Hostproxy*`, `Clawkerd*`).

### 6. `AgentWatcher` (CP core — cross-domain)

The watcher lives in CP core, not firewall subpkg. Its trigger condition — "no clawker agent containers are running" — is not firewall-specific; it's the signal that the CP has no work to do regardless of which subsystems (firewall today, monitor/hostproxy/clawkerd in later branches) are active. The drain callback is supplied by the caller, so the watcher has no compile-time dependency on firewall.

```go
// internal/controlplane/watcher.go
package controlplane

type AgentWatcher struct {
    docker          *docker.Client
    cfg             config.Config
    log             *logger.Logger
    pollInterval    time.Duration   // 30s (override for tests)
    missedThreshold int             // 2
    gracePeriod     time.Duration   // 60s

    // onDrainToZero is invoked when the watcher decides to exit.
    // cmd/clawker-cp/main.go wires this to a shutdown function that
    // stops the firewall Stack, cleans eBPF state, drains the gRPC
    // server, and triggers os.Exit(0). Keeping the callback caller-
    // supplied lets the watcher stay domain-agnostic.
    onDrainToZero func(ctx context.Context) error

    // Test seam: returns count of agent containers.
    // Production wire: docker.Client.ListContainersByProject filtered to
    // purpose=agent, managed=true.
    listAgentsFn func(ctx context.Context) (int, error)
}

func NewAgentWatcher(
    docker *docker.Client, cfg config.Config, log *logger.Logger,
    onDrainToZero func(context.Context) error,
) *AgentWatcher

// Run blocks until ctx cancels or drain-to-zero fires.
// On drain-to-zero: calls onDrainToZero, returns nil.
// On ctx cancel: returns ctx.Err().
func (w *AgentWatcher) Run(ctx context.Context) error
```

Wired in `cmd/clawker-cp/main.go` after `CPStartupOrchestrator.SetReady()`. The drain callback composes firewall-domain shutdown (`firewall.Stack.Stop`, `firewall/ebpf.Manager.CleanupAllLinks`) with process-level shutdown (`grpcServer.GracefulStop`, `os.Exit(0)`).

### 7. Modified CP container spec (bind mount mode change + restart policy)

```go
// internal/controlplane/cp_container.go — BuildCPContainerConfig()

// B1: FirewallDataSubdir mounted RO
// B2: FirewallDataSubdir mounted RW
mounts := []mount.Mount{
    {
        Type:     mount.TypeBind,
        Source:   firewallDataSubdir,
        Target:   "/var/lib/clawker/firewall",
        ReadOnly: false, // was true
    },
    // ... other mounts unchanged ...
}

// Restart policy: on-failure (NOT unless-stopped/always).
// Rationale: CP self-exits cleanly (exit 0) via AgentWatcher drain-to-zero
// (INV-B2-007). unless-stopped/always would restart on that zero-exit and
// defeat the self-shutdown design. on-failure restarts only on non-zero
// exit (crash) — bounded MaximumRetryCount so the CP doesn't thrash.
// No auto crash-recovery beyond that bound: the user reads logs and runs
// `clawker controlplane up`. See INV-B2-007.
hostConfig.RestartPolicy = container.RestartPolicy{
    Name:              container.RestartPolicyOnFailure,
    MaximumRetryCount: 3,
}
```

Envoy + CoreDNS specs keep RO mounts (INV-B2-011).

### 8. Proto changes (`api/admin/v1/`)

Three-part change: (1) domain-prefix rename of B1's 7 methods, (2) scope correction — Install collapses into Enable; Remove re-scoped from per-container to global; Disable semantic shifts from `bypass_map` flag toggle to container_map entry removal (same BPF outcome, cleaner), (3) 6 new global RPCs for rules/lifecycle/status/CA. Net: 13 methods. Message types may be split into `firewall_messages.proto` (all firewall-domain messages) + `admin.proto` (service definition + future cross-domain messages) for file hygiene; single `clawker.admin.v1` package either way.

**Shared firewall messages:**

```proto
syntax = "proto3";
package clawker.admin.v1;

message EgressRule {
  string dst = 1;
  string proto = 2;              // "tls" | "tcp" | "http" | "ssh" | "ip" | "cidr"
  uint32 port = 3;
  string action = 4;             // "allow" | "deny"
  repeated PathRule path_rules = 5;
  string path_default = 6;
}

message PathRule {
  string prefix = 1;
  string action = 2;
}

// Global lifecycle.
// FirewallInit brings the stack up (Envoy + CoreDNS) AND attaches BPF
// programs to host cgroups. Idempotent. Replaces both B1 per-container
// Install's global-setup side and the earlier-proposed FirewallEnsureStack.
message FirewallInitRequest  {}
message FirewallInitResponse {
  string envoy_ip = 1;
  string coredns_ip = 2;
  string network_id = 3;
}

// FirewallRemove is global teardown — no container_id. Stops Envoy +
// CoreDNS, detaches all BPF programs, and flushes all eBPF state
// (container_map, bypass_map, bypass timers, pinned links) plus all
// rules from the store.
message FirewallRemoveRequest  {}
message FirewallRemoveResponse {}

// Per-container enrollment — CLI sends only container_id. CP resolves
// cgroup path internally via Docker API + firewall.EBPFCgroupPath with
// drift guard (INV-B2-016).
//
// FirewallEnable is idempotent: adds container_id to container_map
// routing, attaching BPF links if not already present. Absorbs B1
// `Install`'s per-container role.
message FirewallEnableRequest  { string container_id = 1; ContainerConfig config = 2; }
message FirewallEnableResponse {}

// FirewallDisable removes container_id from routing (clears its
// container_map entry). BPF links remain attached so re-Enable is
// cheap. BPF fast path exits to bypass when container_map lookup
// misses, so enforcement is effectively skipped.
message FirewallDisableRequest  { string container_id = 1; }
message FirewallDisableResponse {}

// FirewallBypass = timed Disable + CP-side dead-man timer that calls
// Enable on expiry. Enable's drift guard covers the restore path.
message FirewallBypassRequest  { string container_id = 1; uint32 timeout_seconds = 2; }
message FirewallBypassResponse {}

// Utilities — unchanged from B1 surface.
message FirewallSyncRoutesRequest  { repeated Route routes = 1; }
message FirewallSyncRoutesResponse {}

message FirewallResolveHostnameRequest  { string hostname = 1; }
message FirewallResolveHostnameResponse { repeated string addresses = 1; }

message FirewallAddRulesRequest  { repeated EgressRule rules = 1; }
message FirewallAddRulesResponse { int32 added_count = 1; bool stack_restarted = 2; }

message FirewallRemoveRulesRequest  { repeated EgressRule rules = 1; }
message FirewallRemoveRulesResponse { int32 removed_count = 1; bool stack_restarted = 2; }

message FirewallListRulesRequest  {}
message FirewallListRulesResponse { repeated EgressRule rules = 1; }

message FirewallReloadRequest  {}
message FirewallReloadResponse { bool stack_restarted = 1; }

message FirewallStatusRequest  {}
message FirewallStatusResponse {
  bool running = 1;
  bool envoy_health = 2;
  bool coredns_health = 3;
  int32 rule_count = 4;
  string envoy_ip = 5;
  string coredns_ip = 6;
  string network_id = 7;
  string cidr = 8;
}

message FirewallRotateCARequest  {}
message FirewallRotateCAResponse {}
```

**Service** (flat, single-service; domain prefixes on method names):

```proto
service AdminService {
  // Firewall domain (13 methods — scope-corrected from B1)
  rpc FirewallInit(FirewallInitRequest) returns (FirewallInitResponse);
  rpc FirewallRemove(FirewallRemoveRequest) returns (FirewallRemoveResponse);
  rpc FirewallEnable(FirewallEnableRequest) returns (FirewallEnableResponse);
  rpc FirewallDisable(FirewallDisableRequest) returns (FirewallDisableResponse);
  rpc FirewallBypass(FirewallBypassRequest) returns (FirewallBypassResponse);
  rpc FirewallAddRules(FirewallAddRulesRequest) returns (FirewallAddRulesResponse);
  rpc FirewallRemoveRules(FirewallRemoveRulesRequest) returns (FirewallRemoveRulesResponse);
  rpc FirewallListRules(FirewallListRulesRequest) returns (FirewallListRulesResponse);
  rpc FirewallReload(FirewallReloadRequest) returns (FirewallReloadResponse);
  rpc FirewallStatus(FirewallStatusRequest) returns (FirewallStatusResponse);
  rpc FirewallRotateCA(FirewallRotateCARequest) returns (FirewallRotateCAResponse);
  rpc FirewallSyncRoutes(FirewallSyncRoutesRequest) returns (FirewallSyncRoutesResponse);
  rpc FirewallResolveHostname(FirewallResolveHostnameRequest) returns (FirewallResolveHostnameResponse);

  // Future cross-domain methods will follow the same `<Domain><Action>[<Object>]`
  // convention: MonitorListMetrics, HostproxyEnsureRunning, ClawkerdRegister, etc.
}
```

All 13 AdminService methods are registered for uniform `"admin"` scope enforcement. INV-B2-009 enumerates via reflection; single scope, no per-method diversification. The `cgroup_path` field removal is a breaking change to B1; alpha project, clean break.

### 9. `cgroupDriver` + cgroup path resolution (CP-side)

B1 had these helpers host-side inside `firewall.Manager`. B2 moves them CP-side into the firewall subpackage — they are firewall-internal primitives (only used by eBPF operations) and belong with their domain.

```go
// internal/controlplane/firewall/cgroup.go
package firewall

// DetectCgroupDriver returns the Docker cgroup driver ("systemd" or "cgroupfs")
// via docker.Client.SystemInfo. Called once at Handler init; cached on Handler.cgroupDriver.
func DetectCgroupDriver(ctx context.Context, docker *docker.Client) (string, error)

// EBPFCgroupPath returns the BPF-attachable cgroup path for a container.
// For systemd driver: /sys/fs/cgroup/system.slice/docker-<id>.scope
// For cgroupfs driver: /sys/fs/cgroup/docker/<id>
func EBPFCgroupPath(cgroupDriver, containerID string) string

// ResolveContainerID looks up a container by name, short ID, or long ID
// and returns its canonical long ID. Fast-path on 64-char hex inputs to
// avoid Docker API round-trip.
func ResolveContainerID(ctx context.Context, docker *docker.Client, ref string) (string, error)
```

**Docker Desktop note (AA-2)**: On macOS/Windows, the Docker daemon runs inside a Linux VM (LinuxKit / WSL2). `docker.Client.SystemInfo` returns the VM's cgroup driver — which is what matters because the CP container, BPF programs, and all managed agent containers live inside that VM. The host OS's cgroup configuration is irrelevant; no code path reads host `/sys/fs/cgroup` from macOS. The cgroup path returned by `EBPFCgroupPath` is valid inside the VM where the CP mounts `/sys/fs/cgroup` (RO) — tested under both Docker Desktop for macOS (cgroupfs in VM) and native Linux (systemd driver typical on modern distros). Integration tests in `test/e2e/firewall_test.go` exercise the Docker-Desktop VM path; unit tests cover both driver strings via a `DetectCgroupDriver` test seam.

`firewall.Handler.Install/Enable/Disable/Bypass` flow:
```go
func (h *Handler) Install(ctx context.Context, req *adminv1.InstallRequest) (*adminv1.InstallResponse, error) {
    cid, err := ResolveContainerID(ctx, h.docker, req.ContainerId)
    if err != nil { return nil, status.Errorf(codes.InvalidArgument, "resolve: %v", err) }
    cgroupPath := EBPFCgroupPath(h.cgroupDriver, cid)
    return h.ebpf.Install(ctx, cgroupPath, req.Config)
}
```

No proto `cgroup_path` field. CLI side (`internal/cmd/firewall/{enable,disable,bypass}.go`) sends only `container_id`.

### 10. `ProjectRules` helper migration

```go
// internal/controlplane/firewall/rules_store.go (was internal/firewall/manager.go)
func ProjectRules(cfg config.Config) []config.EgressRule
```

Called CLI-side in `BootstrapServicesPostStart` to compose the project rule set from `config.Config` (project file + required rules). Sent via `AddRules` RPC. The CP has access to `config.Config` inside the container but the CLI caller is the authoritative project-cwd owner, so composition stays CLI-side.

## Migration Plan

One PR. Internal commits sequenced so each leaves `make test` green and `clawker run @` functional. No commit leaves the tree with both old and new paths active for the same lifecycle event.

**Per-phase acceptance gates (TA-15)** — every phase ends with all of the following passing, enforced locally before the next phase starts and via pre-push hook on the CI branch:

| Gate | Command | Must pass |
|------|---------|-----------|
| Build | `go build ./...` | No compile errors across all packages. |
| Vet | `go vet ./...` | No flagged issues (catches dangling imports post-move). |
| Unit tests | `make test` | All existing + phase-added tests green. |
| E2E smoke | `go test ./test/e2e/firewall_test.go -run TestFirewallEnforcement_E2E -timeout 5m` | Real-Docker baseline: egress enforcement works end-to-end. Skipped on non-Docker hosts. |
| Manual: agent bring-up | `./bin/clawker run @ --detach && ./bin/clawker firewall status && ./bin/clawker container stop @` | Demonstrates container start+firewall enroll+stop still works after the phase. |

Phase boundaries below specify what each phase must additionally prove. A phase that passes build/vet/test but regresses the E2E smoke or manual bring-up is a failed phase — revert and redo. No phase may be "landed" with a deferred test failure ("I'll fix it in Phase N+1"); the migration plan depends on each phase leaving a viable tree.

### Phase 1 — Proto + CP-side code move and rename (no CLI changes)

**Prerequisite (Phase 1.0)**: `pkg/whail` exposes `SystemInfo(ctx) (system.Info, error)`, propagated onto `internal/docker.Client`. Today's firewall manager calls `moby.Client.Info` directly (leaf-status exception). CP is not a leaf — it uses `*docker.Client`. Without this step, `DetectCgroupDriver(ctx, *docker.Client)` in §9 cannot be implemented. Add the method to the whail interface, wire through the decorator, regenerate `internal/docker/mocks/FakeClient` so tests can stub it. Remove `internal/firewall` exception from `.claude/rules/docker-client.md` in the same step only if the legacy `firewall.Manager.Info` call site has already been deleted; otherwise defer the exception removal to Phase 7.

1. Create `internal/controlplane/firewall/` subpackage skeleton with `CLAUDE.md`.
2. **Move and rename** B1's `internal/controlplane/admin_handler.go` + `admin_handler_test.go` to `internal/controlplane/firewall/handler.go` + `handler_test.go`. Rename `AdminHandler` → `Handler`. Update all references.
3. **Move** `internal/controlplane/ebpf/` to `internal/controlplane/firewall/ebpf/`. Update imports in `internal/dnsbpf`, `cmd/clawker-cp/main.go`, and the handler.
4. **Move** firewall package files to `internal/controlplane/firewall/`:
   - `envoy.go` → `envoy_config.go` (rename package to `firewall`)
   - `coredns.go` → `coredns_config.go`
   - `certs.go` → `certs.go`
   - `rules.go` + `types.go` → `rules_store.go`
   - `network.go` → `network.go` (swap raw moby for `*docker.Client`; drop `ensureNetwork`)
   - Sentinels + `HealthTimeoutError` → `errors.go`
   - `coredns_embed.go` → `embed_coredns.go` + move `coredns-clawker` binary to `firewall/assets/`
   - Golden files → `firewall/testdata/`
   - `_test.go` siblings move with their sources
5. **Move** CP-own embed files + binaries to `internal/controlplane/` (core): `cp_embed.go` → `embed_cp.go`, `ebpf_embed.go` → `embed_ebpf.go`, `assets/{clawker-cp,ebpf-manager}` to `controlplane/assets/`.
6. Add `firewall.Stack` in `stack.go` — wraps moved Envoy/CoreDNS lifecycle (previously methods on `firewall.Manager`).
7. Add `firewall.cgroup.go` with `DetectCgroupDriver`, `EBPFCgroupPath`, `ResolveContainerID`.
8. Rewrite `api/admin/v1/admin.proto` to the 13-method scope-corrected surface (see Context table and Design §8): rename B1's 7 RPCs with `Firewall` prefix, collapse `Install` into `Enable`, re-scope `Remove` to global (empty request), add `FirewallInit` + 6 rule/lifecycle/status RPCs. **Remove** `cgroup_path` field from all per-container requests. Run `make proto`.
9. Update `firewall.Handler` to use `DetectCgroupDriver` at construction and resolve cgroup paths internally for `Install/Enable/Disable/Bypass`. Implement the 8 new methods (`EnsureFirewallStack`, `StopFirewallStack`, `AddRules`, `RemoveRules`, `ListRules`, `ReloadFirewall`, `FirewallStatus`, `RotateCA`) on the same type.
10. Update the registered-methods set in `authz.go` to include all 13 AdminService methods. Scope is uniformly `"admin"` — no per-method scope diversification. Update enumerate-all-methods test to reflect over `AdminServiceServer` and assert every method is registered.
11. Update `server.go` to register `firewall.Handler` as the sole AdminService implementation.
12. `CPStartupOrchestrator` initializes `firewall.Stack` + `firewall.Handler` during startup.

**End of Phase 1**: CP compiles. All 15 RPCs work end-to-end inside the CP. `internal/firewall/` still exists but most files have moved; `manager.go` + `daemon.go` remain (used by CLI). `make test` passes. CLI still uses `firewall.Manager` (wired through `f.Firewall` with host-side cgroup-path plumbing temporarily disabled — see note below). User-visible behavior unchanged.

**Note on Phase 1 intermediate state**: Removing `cgroup_path` from proto breaks B1's CLI-side `firewall.Manager` which populates that field. Option A: keep `cgroup_path` in proto during Phase 1–4 as a no-op server-side, then delete in Phase 7 along with the manager. Option B: bundle proto removal with the CLI migration (Phases 4–7 land as one coherent push). **Choose B** to avoid a deprecated field ever existing in a merged commit. Phases 1–3 keep B1 proto unchanged; Phase 4+ ships the proto break with the CLI changes that match.

### Phase 2 — CP watcher + self-shutdown
1. Add `AgentWatcher` in `watcher.go`. Uses `docker.Client.ListContainersByProject` for agent count; wired to drain-to-zero callback.
2. Wire into `cmd/clawker-cp/main.go`: after `/healthz` ready, start watcher goroutine. On drain-to-zero: stop Stack, clean eBPF links, exit process 0.
3. Integration test (E2E): CP + Envoy + CoreDNS + 0 agents → CP exits after `(30s × 2) + grace`.
4. Lifecycle test: CP restart after self-exit recreates stack cleanly.

**End of Phase 2**: CP has watcher capability; host-side firewall daemon *also* has a watcher. This is the only phase with intentional dual-watcher behavior — both run, first to notice wins. Acceptable because Phase 3 immediately flips ownership and Phase 7 deletes the host daemon.

### Phase 3 — Host-side bootstrap extraction
1. Add `internal/controlplane/bootstrap.go` with `EnsureRunning(ctx, docker, cfg, log) error` and `Stop(...)`.
2. Logic is moved (not copied) from `firewall.Manager.EnsureRunning` steps 4a–5 (auth material → CP image → CP container → wait `/healthz`). Uses `*docker.Client`; whail's `EnsureNetwork` option materializes `clawker-net`.
3. `firewall.Manager.EnsureRunning` retains only the Envoy/CoreDNS startup portion (temporary — deleted in Phase 7).
4. Unit tests with `docker/mocks.FakeClient`.

**End of Phase 3**: Two callers of "ensure CP running" exist: (a) new `controlplane.EnsureRunning` (unused by production code), (b) remaining `firewall.Manager.EnsureRunning` partial impl. No user-visible change.

### Phase 4 — Factory noun `f.AdminClient` + proto `cgroup_path` removal
1. Modify `internal/cmdutil/factory.go`: delete `Firewall`, add `AdminClient`.
2. Modify `internal/cmd/factory/default.go`: delete `firewallFunc` + its `mobyclient` import; add `adminClientFunc`. Wire `f.AdminClient = adminClientFunc(f)` in `New()`.
3. Remove `cgroup_path` field from `InstallRequest/EnableRequest/DisableRequest/BypassRequest` in `api/admin/v1/admin.proto`. Run `make proto`. CP side is already ignoring the field from Phase 1's handler changes, so CP compiles. CLI side now has no way to send it.
4. Add test double: extend `internal/controlplane/mocks/` (or new adminclient mock package) with `AdminServiceClient` mock usable by command tests.
5. **Every caller of `f.Firewall` must compile after this phase.** Phases 4–7 are one working-tree-coherent change; intermediate state with `f.Firewall` removed but callers not updated does not compile. Commit granularity inside the PR is fine, but no push between these phases.

### Phase 5 — Command migration (host-side gRPC wire-up)
1. Rewire all 13 firewall subcommands per the Current State Inventory table.
2. Delete `NewCmdServe` (hidden daemon entrypoint).
3. Update command tests: replace `FirewallManagerMock` with `AdminServiceClientMock`.
4. `BootstrapServicesPostStart`: drop `firewall.EnsureDaemon`; call `adminClient.FirewallInit` (idempotent global stack + BPF bring-up), `adminClient.FirewallAddRules` (sync, returns healthy), then `adminClient.FirewallEnable(container_id, config)` (drift-guarded per-container enroll, absorbs B1 `InstallFirewall`).
5. Container commands (`start`, `stop`, `run`, `restart`, `remove`) + loop commands (`iterate`, `tasks`, `lifecycle`): swap `f.Firewall` references for `f.AdminClient` where firewall operations exist; drop entirely where the field was held but unused.

### Phase 6 — Break-glass `controlplane` CLI
1. Create `internal/cmd/controlplane/` with `up.go`, `down.go`, `status.go`, parent `controlplane.go`, CLAUDE.md.
2. `up`: calls `controlplane.EnsureRunning`.
3. `down`: stops CP container via `f.Client`; prints warning about orphan Envoy/CoreDNS (INV-B2-008).
4. `status`: probes `/healthz`; best-effort `adminClient.FirewallStatus` RPC (tolerates stopped CP).
5. Register parent in root command.
6. Regenerate `docs/cli-reference/`.

### Phase 7 — Delete `internal/firewall/` + config accessor cleanup
1. Delete `internal/firewall/` directory entirely (code, tests, mocks, CLAUDE.md, embed stubs).
2. Delete `cfg.FirewallPIDFilePath` accessor. Regenerate `internal/config/mocks/config_mock.go`.
3. Audit `cfg.FirewallLogFilePath` — if no caller, delete (per OQ-B2-004).
4. Audit and update: `internal/hostproxy/daemon.go`, `internal/controlplane/ebpf/types.go`, `cmd/clawker-cp/main.go`, `cmd/coredns-clawker/main.go` — swap any firewall imports to controlplane or remove.
5. Remove `internal/firewall` from `.claude/rules/docker-client.md` exception list.
6. Update `.claude/rules/envoy.md`, `.claude/rules/dependency-placement.md` path references.
7. Update all CLAUDE.md files per Blast Radius section.
8. Update `docs/threat-model.mdx` (expanded TB-002).
9. Regenerate `docs/cli-reference/`.
10. Run `go build ./... && go vet ./... && make test && make test-all` — all green.

**End of Phase 7**: `internal/firewall/` does not exist. `go vet ./...` shows zero imports of the deleted package. `clawker run @` works end-to-end.

### Rollback plan

If a critical bug is discovered post-merge, revert the branch. No data migration occurred (paths stay put), so revert restores the old daemon path and users' state files remain valid. MITM CA and `egress-rules.yaml` format unchanged — a reverted tree reads them successfully.

## Complexity Budget

- **Estimated LOC delta**: approximately **–1,400 net**. Deletions (`daemon.go` 696, `manager.go` 1,715, mocks, removed seams): ~–2,700 LoC. Additions (`FirewallHandler`, `Stack`, `AgentWatcher`, `bootstrap`, controlplane subcommands, generated proto code + handlers + tests): ~+1,300 LoC. Most firewall package code (envoy_config, coredns_config, certs, rules, network, ~2,400 LoC) moves rather than grows.
- **Files touched**: ~50 (20 deleted from `internal/firewall/`, ~15 new/moved in `internal/controlplane/`, 13 command files rewired, 5 new controlplane command files, factory + cmdutil + container start, 8 CLAUDE.md + 3 rules files + threat model + auto-generated docs).
- **New abstractions**: 3 (`firewall.Stack`, `firewall.Handler` — renamed + expanded from B1's `AdminHandler`, `controlplane.AgentWatcher`). One new Factory noun (`AdminClient`). One new subpackage (`internal/controlplane/firewall/`) plus sub-subpackage `firewall/ebpf/` relocated from `controlplane/ebpf/`.
- **Trust boundaries touched**: 2 (TB-001: CLI-CP extended with 8 RPCs; TB-002: CP-Docker DooD expanded to own firewall container lifecycle + firewall state files).
- **Risk surface delta**: medium. CP's scope expands but primitives are already trusted from B1 (DooD, mTLS, OAuth2). The ownership flip concentrates authority — a compromised CP now has firewall rule write access in addition to eBPF and Docker socket. This is the design goal; the existing auth model (mTLS + JWT + admin scope) is the mitigation.

## Invariants

### INV-B2-001: CP is the single writer of firewall state files
- **Type**: must
- **Category**: data-integrity
- **Statement**: After this branch lands, only the CP process writes to `egress-rules.yaml`, `firewall-ca.{crt,key}`, and per-domain cert files under `cfg.FirewallDataSubdir()`. Host-side code does not open these files for writing. CLI reads go through AdminService RPCs only.
- **Boundary**: TB-002
- **Violated when**: Any file in `cfg.FirewallDataSubdir()` gains a host-side writer, or any CLI code calls `storage.Store[EgressRulesFile].Set/Write` for firewall state.
- **Guards against**: dual-writer races, provenance drift, flock contention.
- **Test approach**: AST — `go/packages` load of the full module; walk ASTs for call expressions targeting `os.Create`, `os.OpenFile`, `os.WriteFile`, `storage.Store[...].Write`, `storage.Store[...].Set`; cross-reference call sites against the string literal / accessor `FirewallDataSubdir()`. Fail the test if any such call appears outside `internal/controlplane/firewall/...`, `cmd/clawker-cp/...`, or test files. Grep is insufficient because it matches comments, string literals in docs, and unrelated substrings. The AST pass lives in a standalone `boundary_test.go` at module root (same pattern as today's `TestNoMobyImportOutsideWhail`).
- **Risk**: high

### INV-B2-002: CP is the single owner of Envoy + CoreDNS container lifecycle
- **Type**: must
- **Category**: resource-lifecycle
- **Statement**: Only the CP calls `ContainerCreate/Start/Stop/Restart/Remove` for containers with `dev.clawker.purpose=firewall` (Envoy) or `dev.clawker.purpose=coredns`. Host-side code does not make Docker API calls for these containers.
- **Boundary**: TB-002
- **Violated when**: Any host-side code calls Docker container API methods for `purpose=firewall|coredns` containers.
- **Guards against**: dual-owner races, orphan containers, state divergence.
- **Test approach**: AST — `go/packages` load; walk call expressions targeting `docker.Client.ContainerCreate/Start/Stop/Restart/Remove` and the moby equivalents; correlate each call with the container-name literal or label constant passed in. Fail if any such call targets `envoyContainer`/`corednsContainer` constants (or the hardcoded names) from outside `internal/controlplane/firewall/...`, `cmd/clawker-cp/...`, or test files. Same `boundary_test.go` as INV-B2-001.
- **Risk**: high

### INV-B2-003: Firewall daemon is dissolved, not paralleled
- **Type**: must
- **Category**: resource-lifecycle
- **Statement**: `internal/firewall/daemon.go` is deleted. No PID file is created for firewall lifecycle. `firewall.EnsureDaemon`, `firewall.Daemon`, `firewall.IsDaemonRunning`, `firewall.StopDaemon`, `firewall.WaitForDaemonExit` do not exist in the merged tree. The daemon's health loop and container watcher are replaced by in-CP equivalents (aggregate `/healthz`, `AgentWatcher`).
- **Boundary**: N/A (cleanup invariant)
- **Violated when**: Merged tree contains any PID-file spawning path for firewall, or `internal/firewall/daemon.go` exists.
- **Guards against**: violates PRH-003, half-finished migration.
- **Test approach**: unit (CI assertion: `internal/firewall/daemon.go` does not exist; `writePIDFile.*firewall` zero matches).
- **Risk**: medium

### INV-B2-004: `internal/firewall/` package does not exist post-merge
- **Type**: must
- **Category**: functional
- **Statement**: The `internal/firewall/` directory is deleted. No Go file at `internal/firewall/*`. All firewall imports are replaced by `internal/controlplane/` or `api/admin/v1`.
- **Boundary**: N/A (cleanup invariant)
- **Violated when**: `internal/firewall/` exists, or any Go file imports `"github.com/schmitthub/clawker/internal/firewall"`.
- **Guards against**: orphan code, import drift, ambiguous "thin wrapper" survivor.
- **Test approach**: unit (CI: directory absent; `go vet ./...` catches dangling imports).
- **Risk**: medium

### INV-B2-005: AdminClient is lazy, bootstraps CP on first use, cached per-process, keepalive-probed
- **Type**: must
- **Category**: functional
- **Statement**: `f.AdminClient(ctx)` is a lazy Factory closure. First invocation calls `controlplane.EnsureRunning` (auth material, CP image, CP container, `/healthz` ready) before returning the `adminv1.AdminServiceClient`. Subsequent invocations return the cached client without re-bootstrapping, unless the cached `grpc.ClientConn` has transitioned to `TransientFailure` or `Shutdown` — in which case the closure tears down and rebuilds. `Ready`, `Connecting`, and `Idle` are all treated as cacheable (the gRPC layer handles its own reconnect-with-backoff for those). Dial options include `grpc.WithKeepaliveParams` so long-running CLI processes (loop, monitor, bypass dashboard) detect dead paths within ~40s rather than hanging on the next RPC.
  **Test seam**: the bootstrap call is routed through a package-level `var ensureRunning = controlplane.EnsureRunning` inside `default.go` so command tests can stub it without triggering real CP creation (see TA-7 disposition in Design §2). Tests that assemble a Factory directly (`&cmdutil.Factory{AdminClient: …}`) bypass `adminClientFunc` entirely — the idiomatic pattern for subcommand tests.
- **Boundary**: TB-001
- **Violated when**: `f.AdminClient` dials before ensuring CP on first call, rebuilds on every call, returns a closed connection, or keeps a cached client in `TransientFailure`/`Shutdown`. Also violated if the dial omits keepalive params (long-running callers would silently hang).
- **Guards against**: calling gRPC before CP is ready (fail-closed), wasteful bootstrap calls, stale connections, keepalive gaps causing long-running CLI hangs, tests accidentally spawning real CP containers.
- **Test approach**: unit — (1) mock `ensureRunning` counter; assert first call triggers once, subsequent return cached; (2) simulate `connectivity.Shutdown` / `TransientFailure` via `grpc/test/bufconn` — assert rebuild; (3) assert dial options include keepalive via a stubbed `auth.DialCPAdmin` that captures options; (4) assert `Ready`/`Connecting`/`Idle` are all treated as cacheable (no rebuild).
- **Risk**: medium

### INV-B2-006: `controlplane.EnsureRunning` is idempotent, fail-closed, concurrency-safe
- **Type**: must
- **Category**: functional
- **Statement**: `controlplane.EnsureRunning(ctx, docker, cfg, log) error` is safe under concurrent calls and safe when CP is already running. Returns `nil` if CP is running and healthy; non-nil on any step failure. `clawker-net` network is ensured via existing `whail.Engine.EnsureNetwork` (as the `ContainerCreateOptions.EnsureNetwork` side-effect) — not as a separate call site. An existing CP container is adopted as-is: if stopped, started; if running, verified via `/healthz`. Mount spec is NOT reconciled — CP mounts are a function of install-level XDG env vars + compile-time constants, so legitimate divergence cannot occur, and a host-level attacker who can mutate the container spec already has privileges that trivially bypass any mount-inspection guard. Operators who genuinely need a recreated CP run `clawker controlplane down && up` explicitly.
- **Boundary**: TB-002
- **Violated when**: Concurrent bootstraps create duplicate CP containers, partial failure leaves orphan resources, or a healthy CP is unnecessarily restarted.
- **Guards against**: race conditions, orphan resources from crashed bootstraps, wasteful restarts.
- **Test approach**: unit (concurrent goroutines with fake `*docker.Client` — assert single container create; fake returns existing running container — assert no stop/recreate; fake returns existing stopped container — assert ContainerStart without recreate); integration (real Docker idempotency).
- **Risk**: high
- **History**: the earlier statement mandated mount-spec reconciliation for the B1→B2 RO→RW upgrade path. That migration is landed; the reconciliation guard was retired because (a) it never legitimately fires in B2+, and (b) Docker Desktop on macOS rewrites the `/var/run/docker.sock` bind source to its vsock-proxy path (`/run/host-services/docker.proxy.sock`), which the guard mis-detected as divergence and permanently false-positived, killing a live CP under any cross-process `EnsureRunning` call (concurrent `container run` + `firewall add`, cleanup's `firewall down`, etc.). The threat model only protects what's outside the containers; host-level mount-spec tampering is out of scope.

### INV-B2-007: CP self-shutdown on zero agent containers
- **Type**: must
- **Category**: resource-lifecycle
- **Statement**: CP runs `AgentWatcher` polling agent container count every 30s via `docker.Client.ListContainersByProject` filtered to `purpose=agent, managed=true`. After `missed_threshold=2` consecutive polls with zero agents past the 60s grace period, CP performs graceful shutdown in strict order: (1) `Stack.Stop` removes Envoy + CoreDNS containers; (2) eBPF state is fully flushed — all `container_map` entries, any active bypass timers persisted in BPF maps, and all pinned links are removed; (3) gRPC server `GracefulStop`; (4) `os.Exit(0)`. The CP container's Docker restart policy is `on-failure` (NOT `unless-stopped` or `always`) — graceful zero-code exit does not trigger restart; only a non-zero exit (crash) would. Next CLI invocation that needs the CP triggers `controlplane.EnsureRunning`, which observes the exited container and creates a fresh one. No state (bypass timers, enforcement entries) persists across lifecycle — the CP startup path (INV-B2-013) re-establishes a clean slate.
  **No auto crash-recovery.** If CP exits non-zero, `on-failure` will retry a bounded number of times (policy tuning detail — see §7). Once retries are exhausted, the CP stays down until the user intervenes via `clawker controlplane up` after reading logs. Clawkerd's later-branch failsafe is a separate concern: it detects "firewall stack absent while managed agent container still running" and locks down the agent's networking via iptables to prevent a silently-open firewall — it is NOT a CP auto-restarter. Alpha-project trust model: the user is the recovery mechanism.
- **Boundary**: TB-002
- **Violated when**: CP keeps running with 0 agents past grace, exits before grace expires, restart policy is set to `unless-stopped`/`always` (would restart CP after the graceful drain-to-zero exit, defeating the design), skips eBPF cleanup on exit, or leaves bypass timer state pinned in BPF maps across lifecycle.
- **Guards against**: resource leaks, stale enforcement state, bypass-timer carryover across agent lifecycles, indefinite Docker resource consumption, Docker restart-policy loops, drain-to-zero being defeated by aggressive restart policy.
- **Test approach**: unit (watcher goroutine with `listAgentsFn` returning 0 for N polls; assert `onDrainToZero` fires in the exact order above; assert bypass timers are cleared alongside container_map); container-spec unit test (assert `HostConfig.RestartPolicy.Name == "on-failure"` in `BuildCPContainerConfig`); integration (real CP + 0 agents → exits cleanly, container shows `exited (0)` and is NOT restarted).
- **Risk**: medium

### INV-B2-008: Break-glass `controlplane down` does not leak firewall state
- **Type**: must
- **Category**: resource-lifecycle
- **Statement**: `clawker controlplane down` stops and removes only the CP container. It does NOT automatically teardown Envoy + CoreDNS — if running, they remain, orphaned until next `EnsureRunning` sweeps them or the user calls `clawker firewall down` first. The command prints a visible warning. `clawker controlplane up` is a thin wrapper over `controlplane.EnsureRunning`; strictly idempotent.
- **Boundary**: N/A (operator UX)
- **Violated when**: `down` unexpectedly stops Envoy/CoreDNS without warning, or `up` creates duplicate CP containers.
- **Guards against**: user confusion from coupled lifecycle, surprise teardown.
- **Test approach**: unit (command tests with `FakeClient` — assert only CP affected; warning printed to stderr when Envoy/CoreDNS running).
- **Risk**: low

### INV-B2-009: Every AdminService method requires the `admin` scope; unmapped methods fail closed
- **Type**: must
- **Category**: security
- **Statement**: Every registered `AdminService` method — the 13 scope-corrected firewall RPCs and any future cross-domain methods — requires the single scope `"admin"`. The CP admin API has exactly one scope, permanently. The authz interceptor enforces two gates: (1) the method must appear in the registered-methods set (unmapped methods return `PermissionDenied` — preserves master INV-009 fail-closed default); (2) the caller's JWT must carry `"admin"` in its scope claim. No per-method scope diversification is planned or permitted; callers that need finer-grained access control are out of scope for the CP admin API.
- **Boundary**: TB-001
- **Violated when**: A registered RPC is missing from the registered-methods set; or the authz interceptor accepts any scope other than `"admin"`; or any non-uniform per-method scope logic is introduced.
- **Guards against**: authz bypass from an unregistered method; scope-creep in the authorization model; inconsistent privilege enforcement across RPCs.
- **Test approach**: unit — reflection over the registered `AdminServiceServer` interface enumerates all methods; assert each appears in the registered-methods set; assert interceptor rejects missing-scope and wrong-scope tokens for a sampled method; assert interceptor rejects a method that exists on `AdminServiceServer` but has no registered-methods entry.
- **Risk**: critical

### INV-B2-010: Rules store RPC is the only external read path (one carve-out)
- **Type**: must
- **Category**: data-integrity
- **Statement**: `clawker firewall list` and every other external rule reader issues a `ListRules` gRPC call. No external package parses `egress-rules.yaml` directly. CP-internal code reads the store via `storage.Store`; external packages may not — with one carve-out: `internal/hostproxy` reads `egress-rules.yaml` directly via `os.ReadFile` + `yaml.Unmarshal` with mirror types for its `/open/url` browser-auth egress check. Hostproxy must stay a leaf package (no `internal/controlplane` or `internal/storage` imports) to avoid a dependency cycle; the CP calls into hostproxy transitively via the factory, so hostproxy cannot call into CP. Flock-protected reads on every request mitigate torn-read risk; `yaml.Unmarshal` tolerates unknown fields, so additive rule-schema changes do not break hostproxy.
- **Boundary**: TB-001
- **Violated when**: Any package outside `internal/controlplane/`, `cmd/clawker-cp/`, or `internal/hostproxy/` reads `egress-rules.yaml` directly; or hostproxy acquires an import on `internal/controlplane` or `internal/storage`.
- **Guards against**: dual-reader races, source-of-truth drift, uncontrolled proliferation of direct readers.
- **Test approach**: AST — `go/packages` load; collect every identifier reference resolving to type `EgressRulesFile` or the `EgressRulesFileName` accessor. Assert every reference comes from `internal/controlplane/...`, `internal/config/...` (path accessors only), `internal/hostproxy/...` (carve-out), or test files. The AST pass distinguishes true imports from string-literal mentions — grep would false-positive on comments and CLAUDE.md snippets embedded in Go docstrings.
- **Risk**: medium

### INV-B2-012: Cgroup resolution is CP-side; proto carries no `cgroup_path`
- **Type**: must
- **Category**: data-integrity
- **Statement**: After B2, `firewall.Handler` resolves cgroup paths internally from `container_id` using a cached `cgroupDriver` (detected once at `NewHandler` via `docker.Client.SystemInfo`). The CLI does not compute cgroup paths. The `cgroup_path` field is removed entirely from `InstallRequest`, `EnableRequest`, `DisableRequest`, `BypassRequest` — alpha project, clean break, no deprecation.
- **Boundary**: TB-001
- **Violated when**: Any request message in `api/admin/v1/` still carries a `cgroup_path` field, or any CLI code computes a cgroup path, or `firewall.Handler` trusts a client-supplied path.
- **Guards against**: cgroup path injection from an authenticated but malicious caller; scattered SystemInfo calls; CLI-CP coupling on a purely internal firewall detail.
- **Test approach**: unit (proto-reflection test asserts no `cgroup_path` field exists on the four request messages; handler test asserts `EBPFCgroupPath(h.cgroupDriver, cid)` is the sole source of path values).
- **Risk**: medium

### INV-B2-011: Envoy + CoreDNS bind mounts to firewall data are RO
- **Type**: must
- **Category**: security
- **Statement**: Envoy and CoreDNS container configs mount `cfg.FirewallDataSubdir()` (or relevant subpaths: certs, envoy.yaml, Corefile) as read-only. Only the CP has an RW bind mount. This prevents a compromised Envoy/CoreDNS from rewriting rules or certs.
- **Boundary**: TB-002
- **Violated when**: Envoy or CoreDNS container config mounts any path RW from `cfg.FirewallDataSubdir()`.
- **Guards against**: privilege escalation via compromised proxy, defense-in-depth erosion.
- **Test approach**: unit (`container_config_test.go`: assert `mount.ReadOnly == true` on Envoy + CoreDNS specs for firewall data paths).
- **Risk**: high

### INV-B2-013: CP startup performs defensive eBPF clean-slate
- **Type**: must
- **Category**: resource-lifecycle
- **Statement**: Every CP process startup, before `CPStartupOrchestrator.SetReady()` and before any RPC can be served, runs a defensive eBPF cleanup pass: flush `container_map`, cancel and remove any stale bypass timer state persisted in BPF maps, remove orphaned pinned links under the CP's pin directory. This is the crash-recovery path — it handles cases where a prior CP instance did NOT reach the graceful shutdown sequence of INV-B2-007 (SIGKILL, OOM, Docker daemon restart, host reboot, process panic). Per-agent re-enrollment then happens normally via `FirewallEnable` RPC keyed on container ID (INV-B2-012, INV-B2-016). Cleanup is idempotent and safe against empty maps.
- **Boundary**: TB-002
- **Violated when**: CP starts serving RPCs before the cleanup pass completes; startup skips cleanup on the "happy path" assumption; cleanup panics on empty BPF maps.
- **Guards against**: stale enforcement entries across a CP crash masquerading as live enforcement; bypass timer state outliving the agent it was granted to; pinned-link leaks accumulating across restart cycles.
- **Test approach**: unit (startup orchestrator test: assert cleanup runs before `SetReady`; fake eBPF manager records the cleanup call and pre-populates stale state to verify idempotency and effect); integration (pre-seed BPF maps, start CP, assert maps empty before first RPC).
- **Risk**: medium

### INV-B2-016: `FirewallEnable` verifies container_id → cgroup_id against Docker before acting
- **Type**: must
- **Category**: data-integrity
- **Statement**: Every `FirewallEnable(container_id)` call resolves the container's CURRENT cgroup path via Docker API (same resolver used by `resolveBypassCgroupID` in admin_handler.go:267–311), computes the fresh cgroup_id from that path, and uses that value when writing to `container_map`. If the CP holds a stored mapping for this `container_id` from a prior Enable/Install and the fresh cgroup_id differs, the CP logs a warning ("cgroup_id drift detected") and proceeds with the fresh value. A container may legitimately get a new cgroup_id after a crash + restart; without this guard, a fast-path Enable would re-populate container_map under a stale cgroup_id that now belongs to some other container — routing to the wrong BPF subject. The same resolution path covers the dead-man-timer restore in `FirewallBypass` (B1 already has this there; B2 extends it to direct Enable). If Docker says the container is gone (`!exists`), Enable returns `FailedPrecondition` rather than writing stale state.
- **Boundary**: TB-002
- **Violated when**: Direct `FirewallEnable` trusts any CLI-supplied or cached cgroup path without re-resolving through Docker; or silently proceeds on drift without logging; or succeeds when Docker reports the container is gone.
- **Guards against**: routing reassignment after cgroup_id reuse (Linux cgroup inodes are reused eventually); silent enforcement misdirection to a sibling container; stale bypass-map entries landing on the wrong cgroup.
- **Test approach**: unit (fake Docker resolver returning a path whose cgroup_id differs from stored — assert warning log + fresh ID written to container_map; fake resolver returning `!exists` — assert `FailedPrecondition`); regression (extract the existing `resolveBypassCgroupID` drift logic into a shared helper used by both Enable and the bypass timer).
- **Risk**: high

### INV-B2-014: CP container is attached to `clawker-net`
- **Type**: must
- **Category**: functional
- **Statement**: The CP container is attached to the `clawker-net` Docker network (in addition to AdminPort port-forwarding on `127.0.0.1`). This is required for CP to reach Envoy and CoreDNS by their clawker-net IPs for health probing, config-reload signaling, and DooD-driven lifecycle ops. `BuildCPContainerConfig` sets the network explicitly; `controlplane.EnsureRunning` uses `ContainerCreateOptions.EnsureNetwork` so `clawker-net` is materialized before container creation if not already present. The CP's IP on clawker-net is either static (`cpStaticIP`) or whail-assigned — determined by the same policy used for Envoy/CoreDNS today, carried over unchanged.
- **Boundary**: TB-002
- **Violated when**: CP container is created without a `clawker-net` endpoint; `EnsureRunning` does not ensure `clawker-net` exists; or CP attempts to probe Envoy/CoreDNS via a network it is not attached to.
- **Guards against**: CP `Stack.WaitForHealthy` hanging forever because CP has no route to Envoy/CoreDNS; silent breakage when `clawker-net` is deleted externally.
- **Test approach**: unit (`container_config_test.go`: assert `clawker-net` in CP endpoint config; `ContainerCreateOptions.EnsureNetwork` set); integration (delete `clawker-net` externally, run `controlplane up`, assert network recreated and CP attached).
- **Risk**: high

## Prohibitions

### PRH-B2-001: No `internal/firewall` imports after merge
- **Statement**: No Go file imports `"github.com/schmitthub/clawker/internal/firewall"`. The package does not exist.
- **Detection**: AST — `go/packages` load of all packages in the module, walk `Imports` maps, assert no package has `github.com/schmitthub/clawker/internal/firewall` in its import set. Reliable across comments, vendored copies, and build-tagged files.
- **Consequence**: Half-finished migration, orphan code, import drift.

### PRH-B2-002: No host-side PID file for firewall
- **Statement**: Host-side code does not create, check, or remove any PID file for firewall purposes. `cfg.FirewallPIDFilePath` accessor is deleted.
- **Detection**: AST — identifier/selector reference search for `FirewallPIDFilePath` on the `config.Config` interface across the loaded module. Zero references. Accessor is removed from the interface so the compiler also enforces this once the change lands — the AST test is belt-and-suspenders against accidental reintroduction.
- **Consequence**: Violates PRH-003, daemon sunset incomplete.

### PRH-B2-003: No host-side Docker API calls for Envoy or CoreDNS containers
- **Statement**: No CLI code calls `ContainerCreate|Start|Stop|Restart|Remove` for containers with `purpose=firewall` or `purpose=coredns`. All lifecycle goes through CP RPCs.
- **Detection**: grep for those method names targeting firewall/coredns labels outside `internal/controlplane/` — zero matches.
- **Consequence**: Dual-owner race, state divergence, violates INV-001 from master.

### PRH-B2-004: No direct file reads of `egress-rules.yaml` outside CP (one carve-out)
- **Statement**: Only `internal/controlplane/`, `cmd/clawker-cp/`, and `internal/hostproxy/` may read `egress-rules.yaml`. External readers use `ListRules` RPC. The `internal/hostproxy/` carve-out exists because hostproxy must remain a leaf package (no `internal/controlplane` or `internal/storage` imports) — the CP reaches hostproxy through the factory, so hostproxy cannot dial the CP without a dependency cycle. Hostproxy uses `os.ReadFile` + `yaml.Unmarshal` with flock, mirror types, and unknown-field tolerance.
- **Detection**: grep for `egress-rules.yaml` or `EgressRulesFile` outside allowed packages (`internal/controlplane/**`, `cmd/clawker-cp/**`, `internal/hostproxy/**`) — zero matches.
- **Consequence**: Source-of-truth drift, mid-write reads, interface bypass.

### PRH-B2-005: No synchronous daemon pattern in new code
- **Statement**: No new code uses detached subprocess + PID file + `Setsid: true`. Daemons live as PID 1 in containers.
- **Detection**: AST — walk composite literal `syscall.SysProcAttr{Setsid: true}` constructions and function calls named `writePIDFile` across the module; assert zero occurrences in any new file (files absent from the pre-B2 baseline file list).
- **Consequence**: Fragmentation, undoes consolidation goal.

### PRH-B2-007: No `cgroup_path` on proto; no CLI-side cgroup computation
- **Statement**: The `cgroup_path` field does not exist on `InstallRequest/EnableRequest/DisableRequest/BypassRequest`. No CLI code calls `DetectCgroupDriver` or `EBPFCgroupPath`. Those helpers live only in `internal/controlplane/firewall/cgroup.go` and are called only by `firewall.Handler`.
- **Detection**: hybrid — (a) `grep -R "cgroup_path" api/admin/v1/*.proto` is zero (proto is non-Go, grep is the authoritative tool here); (b) AST — assert the generated Go message types in `api/admin/v1/*.pb.go` have no `CgroupPath` field on `FirewallEnableRequest/FirewallDisableRequest/FirewallBypassRequest` via reflect over the registered proto descriptors; (c) AST — `EBPFCgroupPath` and `DetectCgroupDriver` identifier references appear only in `internal/controlplane/firewall/...` or test files.
- **Consequence**: INV-B2-012 violation; CLI-CP coupling on a firewall-internal detail.

### PRH-B2-006: No cross-reference between auth CA and MITM CA
- **Statement**: The CLI↔CP auth CA and the Envoy MITM CA never sign each other's material, share keys, or appear in the same code path. The CP manages both with strictly separate handlers and file paths.
- **Detection**: grep for cross-mentions in the same function; assert separate handler types, separate rotation entry points, separate file paths.
- **Consequence**: Compromise cascades; violates INV-008 (master).

## Boundary Conditions

### BND-B2-001: CP crash mid-bootstrap leaves orphan resources
- **Boundary**: TB-002
- **Input**: CLI calling `controlplane.EnsureRunning` during container start
- **Validation required**: Detect partial state (CP image present but container missing; CP container present but unhealthy). Clean up or recover idempotently on next call.
- **Failure mode**: Fail-closed. On unrecoverable partial state: return error with actionable message ("control plane in inconsistent state — run `clawker controlplane down` then retry").

### BND-B2-002: CP watcher sees 0 agents during grace period
- **Boundary**: Internal CP lifecycle
- **Input**: Docker API at watcher startup
- **Validation required**: 60s grace period before counting "missed" polls.
- **Failure mode**: Fail-soft. Grace timer runs; watcher does not count missed polls until grace expires.

### BND-B2-003: RPC received before Ory stack fully healthy
- **Boundary**: TB-001
- **Input**: CLI gRPC call arriving before `/healthz` returns 200
- **Validation required**: `f.AdminClient(ctx)` blocks on `/healthz` polling via `controlplane.EnsureRunning` before returning client. Inside CP, `CPStartupOrchestrator.SetReady()` gates the auth interceptor.
- **Failure mode**: Fail-closed. CP returns `codes.Unavailable` if called before ready; CLI retries through `EnsureRunning` wait loop.

### BND-B2-004: Docker socket unavailable inside CP (post-bootstrap)
- **Boundary**: TB-002
- **Input**: CP attempting DooD call with broken socket
- **Validation required**: CP pings Docker on startup (B1 BND-003). If socket breaks at runtime, the specific RPC fails clearly; CP does not crash (watcher + health loops continue).
- **Failure mode**: Fail-closed for affected RPC; log at Error; `/healthz` degrades if persistent.

### BND-B2-005: Rules store contention during concurrent AddRules
- **Boundary**: Internal CP state
- **Input**: Two concurrent `AddRules` RPCs
- **Validation required**: `storage.Store[EgressRulesFile]` uses flock + COW inside CP. In-process mutex on `FirewallHandler` serializes `AddRules/RemoveRules/Reload/RotateCA` to prevent intra-process reordering while `Stack.Reload` is in flight.
- **Failure mode**: Serialized; both succeed eventually. No lost updates.

### BND-B2-006: `StopFirewallStack` called while agents still enrolled
- **Boundary**: TB-001
- **Input**: User runs `clawker firewall down` with running agents
- **Validation required**: Document that `StopFirewallStack` tears down Envoy + CoreDNS and leaves enrolled agents without egress enforcement. Does NOT silently flush `container_map` entries — agents remain enrolled, ready for next `EnsureFirewallStack`.
- **Failure mode**: Permissive (intentional; user-requested destructive op). Log clearly. Do not refuse.

### BND-B2-007: `RotateCA` during active Envoy connections
- **Boundary**: TB-002
- **Input**: `clawker firewall rotate-ca`
- **Validation required**: Envoy restart after cert regen. Active connections drop as with any restart.
- **Failure mode**: Brief egress unavailability during Envoy restart (~2s). Log start/end.

## STRIDE Analysis

### TB-002: CP-Docker DooD (expanded from B1 eBPF-only to full firewall)

| Category | Threat | Mitigation |
|----------|--------|------------|
| **Spoofing** | Malicious host process spoofs gRPC to AdminService | mTLS client cert + OAuth2 JWT (unchanged from B1). Attacker needs CLI-CA-signed cert + valid access token. |
| **Tampering** | Attacker modifies `egress-rules.yaml` out-of-band to add allow rules | CP is sole writer (INV-B2-001). Host-side root can still tamper — documented existing trust assumption (host root out-of-scope for threat model). |
| **Tampering** | Compromised Envoy writes to MITM CA private key | Envoy mounts `FirewallDataSubdir` RO (INV-B2-011). Envoy RCE cannot rotate CA. |
| **Tampering** | Compromised Envoy writes `envoy.yaml` to reconfigure itself | Same RO mount. |
| **Repudiation** | RPC calls not logged | CP logs every AdminService call with caller subject (JWT `sub`) to its zerolog file-backed logger. |
| **Info Disclosure** | `ListRules` leaks internal URLs via rule destinations | Rules contain domains, not content. Destinations already visible to any host process running `clawker firewall list`. No new disclosure. |
| **DoS** | Attacker floods `AddRules` / `ReloadFirewall` causing Envoy restart loop | `admin` scope required. Localhost-only binding. Rate limiting deferred — documented gap. |
| **DoS** | Attacker triggers `RotateCA` repeatedly | Same. |
| **Elevation of Privilege** | RCE in CP grants root-equivalent Docker access | Fundamental DooD trust. Pinned base images (SHA-256 digests). Ory binaries embedded and hash-verified at build. Not new to B2 (B1 established). |
| **Elevation of Privilege** | `EnsureFirewallStack` creates arbitrary containers | RPC parameter-free. Handler creates Envoy + CoreDNS from hardcoded pinned specs. No user-controlled image or command. |
| **Elevation of Privilege** | `AddRules` rule payload injects path traversal / shell metacharacters via `dst` | `ValidateDst` enforces RFC 1035/1123 label rules; rejects uppercase, rejects path metacharacters. Same validation as today. |
| **Elevation of Privilege** | Client-supplied cgroup path traversal | Removed in B2. `cgroup_path` field is deleted from proto; `firewall.Handler` resolves the path internally via `EBPFCgroupPath(h.cgroupDriver, containerID)`. No client input reaches eBPF path construction. |

### TB-001: CLI-CP AdminService (B1 auth stack unchanged; surface scope-corrected to 13 firewall RPCs)

**Surface framing**: AdminService is the CLI API — privileged, sensitive, operated exclusively by human users via the `clawker` binary. It is isolated from the future AgentService (TB-003, B4) by:
- **Separate listeners**: AdminService binds `127.0.0.1:7443` (localhost-only, host port-forwarded). AgentService binds `0.0.0.0:<agent-port>` on clawker-net. A compromised agent on clawker-net cannot reach the admin listener at all; a compromised host process cannot reach the agent listener without joining clawker-net.
- **Separate CAs**: AdminService's mTLS trust anchor is the CLI CA. AgentService's trust anchor is the agent CA (future B4). Cross-signing is prohibited by master INV-008 and the B2 spec's PRH-B2-006.
- **Separate scope vocabularies**: AdminService has exactly one scope (`"admin"`) permanently. AgentService will use `"agent:*"` scopes. No token carrying only `"admin"` can authorize on AgentService and vice versa.

All 13 AdminService methods inherit B1's auth stack (mTLS with CLI CA + OAuth2 JWT via `private_key_jwt` + uniform `"admin"` scope). STRIDE analysis from B1 applies. INV-B2-009 enforces scope registration + fail-closed default for any unmapped method.

## Environment Assumptions

### EA-B2-001: Docker socket available to CP container
- **Assumption**: `/var/run/docker.sock` bind-mounted into CP (B1 INV-005). B2 adds Envoy + CoreDNS lifecycle via this socket.
- **Consequence if wrong**: `EnsureFirewallStack` fails at CP startup Docker ping (B1 BND-003). CP refuses to mark ready; host-side `controlplane.EnsureRunning` times out with clear error.

### EA-B2-002: `cfg.FirewallDataSubdir()` writable by CP
- **Assumption**: Bind mount RW on host; in-container process has write perms.
- **Consequence if wrong**: CP fails at first write (AddRules, CA ensure). Logged at Error, surfaced as RPC error. User sees `permission denied` with path.

### EA-B2-003: CP process can exit cleanly
- **Assumption**: Container runtime propagates `os.Exit(0)` and allows orderly shutdown (defers run, gRPC `server.Stop` completes, Ory subprocesses receive SIGTERM).
- **Consequence if wrong**: Partial shutdown could leave Envoy + CoreDNS orphaned. Next `EnsureRunning` sweeps orphans (INV-B2-006 idempotency).

### EA-B2-004: cgroup v2 support
- **Assumption**: Modern cgroup driver detection works for CP's eBPF path resolution. Same as B1.
- **Consequence if wrong**: CP logs detection failure, refuses `Install`.

### EA-B2-005: AdminPort (7443) accessible on `127.0.0.1`
- **Assumption**: CP binds AdminPort on 127.0.0.1 inside container; port-forwarded to host 127.0.0.1. No other process holds this port.
- **Consequence if wrong**: `controlplane.EnsureRunning` fails at container start with port conflict. Error surfaces to CLI.

### EA-B2-006: Docker Events API availability
- **Assumption (B2 defers)**: B2 does NOT require Docker Events — watcher uses polling. B3 swaps in event subscription.

## Open Questions

### OQ-B2-001: `AddRules` async-restart vs sync-wait-healthy?

`AddRules` today (host-side) regenerates configs and restarts Envoy + CoreDNS synchronously.

1. RPC returns immediately; separate `WaitForHealthy` RPC for polling.
2. RPC blocks until stack is healthy post-restart.

**Leaning**: Option 2 — matches today's semantics, no new RPC. `FirewallStatus` remains the explicit probe. Resolve during `/ctdd`.

### OQ-B2-002: `controlplane.EnsureRunning` internal retry?

Transient Docker failures (socket busy, image pull glitch) could cause intermittent bootstrap failures.

1. Internal bounded-backoff retry.
2. Return immediately; let caller decide retry policy.

**Leaning**: Option 2 — explicit, predictable, easier to test. Resolve during `/ctdd`.

### OQ-B2-003: `clawker controlplane down` behavior when firewall stack is up

INV-B2-008 says `down` stops only CP. Open: refuse with message, warn-and-proceed, or silent orphan?

**Leaning**: Warn and proceed. Print `"Envoy and CoreDNS will continue running until next `controlplane up`"`. Break-glass commands should not block. Resolve during `/ctdd`.

### OQ-B2-004: `cfg.FirewallLogFilePath` accessor fate

Used today only by firewall daemon (`firewall.log`). After B2, daemon is gone. Roll firewall log into CP's main log, or keep dedicated `firewall.log`?

**Leaning**: Roll into CP log. Delete accessor. CP's file logging already writes to its own log file — one fewer log for users to know about.

### OQ-B2-005: ~~Handler composition~~ — **RESOLVED**

Resolved by Key Decision #11 and Design §5: B1's `AdminHandler` is renamed and relocated to `firewall.Handler` in the firewall subpackage, then scope-corrected. One `firewall.Handler` implements all 13 firewall-domain AdminService RPCs. `server.go` registers it via embedding in `adminServer`. No open question remaining.

## Packages Affected

`is_monorepo: false`. Listed for orientation:

- **Deleted**: `internal/firewall/`, `internal/firewall/mocks/`, `internal/firewall/testdata/`, `internal/firewall/assets/`
- **Grown**: `internal/controlplane/` core (+4 new files: `bootstrap.go`, `watcher.go`, `embed_cp.go`, `embed_ebpf.go` — the latter two are moves; +2 tests), `internal/controlplane/firewall/` NEW SUBPACKAGE (absorbs ~15 firewall .go files + `handler.go` renamed from `admin_handler.go` + 3 new files: `stack.go`, `cgroup.go`, `status.go`; +`errors.go` + `embed_coredns.go` + `testdata/` + `assets/coredns-clawker` + `ebpf/` subpackage moved from `controlplane/ebpf/`), `internal/cmd/controlplane/` (new, 5 files), `api/admin/v1/` (+8 RPCs + messages + generated Go; **breaking**: `cgroup_path` field removed from 4 B1 request messages)
- **Modified**: `internal/cmdutil/factory.go` (–`Firewall`, +`AdminClient`), `internal/cmd/factory/default.go` (–`firewallFunc`, +`adminClientFunc`, –moby import), `internal/cmd/firewall/*.go` (all 13 files rewired; `NewCmdServe` deleted), `internal/cmd/container/shared/container_start.go`, `internal/cmd/container/{start,stop,run,restart,remove}/*.go` (swap `f.Firewall`→`f.AdminClient`), `internal/cmd/loop/{iterate,tasks,shared}/*.go` (same), `internal/config/config.go` + `consts.go` + mocks (drop `FirewallPIDFilePath`, possibly `FirewallLogFilePath`), `cmd/clawker-cp/main.go` (wire AgentWatcher + FirewallHandler), `test/e2e/{firewall_test.go,harness/factory.go,preset_builds_test.go}` (E2E rewritten to gRPC path)
- **Docs**: `internal/firewall/CLAUDE.md` (deleted), `internal/cmd/firewall/CLAUDE.md` (updated), `internal/cmd/factory/CLAUDE.md`, `internal/cmdutil/CLAUDE.md`, `internal/controlplane/CLAUDE.md` (rescoped to core only), `internal/controlplane/firewall/CLAUDE.md` (new), `.claude/rules/{docker-client.md,envoy.md,dependency-placement.md}`, `.claude/docs/{ARCHITECTURE.md,KEY-CONCEPTS.md}`, `.correctless/specs/cp-initiative/CLAUDE.md`, `docs/threat-model.mdx`, `docs/cli-reference/` (regenerated)
