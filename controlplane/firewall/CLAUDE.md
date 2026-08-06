# Controlplane Firewall Subpackage

Firewall domain under the control plane. Owns the egress enforcement surface: Envoy + CoreDNS config generation, MITM CA + per-domain certs, egress rules store, eBPF manager (under `ebpf/`), CoreDNS binary embed, Docker network discovery, cgroup resolution, and the gRPC firewall-domain handler on `adminv1.AdminServiceServer`.

## Architecture

```
clawker CLI
    │  f.AdminClient(ctx) — mTLS + OAuth2 JWT
    ▼
internal/controlplane/adminServer  (embeds *firewall.Handler)
    │
    ▼
firewall.Handler (13 RPCs)
    │  pre-Submit work is PURE only (validate, proto convert);
    │  every store write and stack op runs inside a queued
    │  closure — Submit → wait on reply channel
    ▼
ActionQueue (single-goroutine FIFO worker; queue.go)
    │  the single-writer funnel: coalesces consecutive
    │  ActionReconcile submissions; Bringup / Teardown /
    │  RuleMutate / Read / Enable / Disable / Bypass never
    │  coalesce — they execute one-at-a-time.
    ▼
Closures (reconcileStackClosure + per-RPC bodies) call:
    ├── Stack         → Envoy + CoreDNS containers (on the clawker network)
    ├── ebpf.Manager  → pinned BPF maps + attached programs
    ├── EgressRulesStore → egress-rules.yaml (gofrs/flock, atomic rename)
    ├── Resolver      → Docker-backed (cid, cgroupPath, exists, err)
    ├── Certs (lazy)  → on-disk CA + per-domain certs
    └── EnrolledTopic → EBPFContainerEnrolled (drives netlogger LabelCache hydration)
```

- **No host-side daemon**: `internal/firewall/` is gone. Lifecycle authority is the `clawker-controlplane` container (see `../CLAUDE.md` for startup sequencing). CP bringup is owned by the explicit bootstrap verbs (`controlplane up`, `firewall up`, container start) via `Manager.Start`; when the `AgentWatcher` observes drain-to-zero + grace, the CP self-shuts-down (INV-B2-007).
- **Composite server**: `controlplane.adminServer` embeds `*firewall.Handler`; Go method promotion surfaces all 13 RPCs. Future domain handlers (monitor, hostproxy, clawkerd) embed alongside.
- **Per-container RPCs carry only `container_id`**: path resolution is hidden behind the injected `ContainerResolver`. The wiring in `cmd/clawkercp/main.go::containerResolverFromDocker` calls `DetectCgroupDriver` once at CP startup and captures the driver string in the resolver closure; every RPC call goes through the resolver, which invokes `ResolveContainerID` and then resolves the cgroup path (INV-B2-016 drift guard): the conventional rootful layout (`EBPFCgroupPath`) when it exists on disk, otherwise a one-time discovery walk of `/sys/fs/cgroup` for the target container's own directory (rootless daemons park scopes under the user slice at a uid-dependent depth) with the found parent cached per resolver. A cgroup that exists nowhere while Docker says the container is alive is a loud error, never a fabricated path. The Handler itself holds no cgroup driver state.

## Files

| File | Purpose |
|------|---------|
| `handler.go` | `Handler` + `HandlerDeps` + `ContainerResolver` + `StackLifecycle` — 13 RPCs, bypass timer management. Rule mutation itself lives on `EgressRulesStore` (`rules_store.go`); the Handler calls it and owns only the logging + RPC mapping around it. Wire↔config rule translation lives beside the proto bindings in `api/admin/v1` (`EgressRulesToProto`/`EgressRulesFromProto`), not here |
| `stack.go` | `Stack` — Envoy + CoreDNS container lifecycle via DooD; image build helpers (`drainPullStream`, `ensureEnvoyImage`, `ensureCorednsImage`); health probing; `EnsureRunning`/`Stop`/`Reload`/`WaitForHealthy`/`Status` + IP/CIDR accessors. Sibling drift gate: `driftLabels()` stamps three labels on both containers — `infra_certs_ready` (mTLS bind/env shape), `otel_infra_port` (create-time OTLP port), and `stack_build_sha` (the CP's embedded-binary hash via `consts.CPBinarySHA`, injected by host bootstrap as container env). `ensureContainer`/`reloadContainer` compare them against the running container and recreate on any mismatch (`event=firewall_container_spec_drift`). The build SHA covers every compiled-in staleness vector — pinned Envoy image const, embedded CoreDNS binary, config templates, containerSpec shape — so a CLI upgrade that replaces the CP also replaces the siblings instead of adopting stale ones. |
| `status.go` | `Status` struct returned by `Stack.Status` (per-container up state, IPs, rule count) |
| `cgroup.go` | `DetectCgroupDriver(ctx, *docker.Client)`, `EBPFCgroupPath(driver, cid)` (conventional rootful layout — fast path only), `cgroupPathResolver` (discovery fallback: walks the hierarchy for the container's own cgroup dir, caches the per-daemon parent — how rootless layouts resolve), `ResolveContainerID(ctx, *docker.Client, ref)`, `IsCanonicalContainerID` |
| `drift.go` | `resolveBypassCgroupID(entry, resolver, log)` — shared INV-B2-016 drift resolver used by direct Enable (`resolveForEnable`) and the bypass dead-man timer |
| `envoy_config.go` | Envoy YAML generation; per-domain filter chains; LOGICAL_DNS clusters; TCP/SSH listeners; access log builder (stdout JSON for `docker logs` triage, plus native `envoy.access_loggers.open_telemetry` OTLP/gRPC sink when mTLS material is wired). Rule routing by `proto:` (`https` → TLS-MITM HCM, `http` → plaintext HCM, `ssh`/`tcp`/other → opaque TCP listener). Per access-log record: OTel semconv fields for network/server/client/tls (`network.transport`, `network.protocol.name`, `network.protocol.version`, `tls.established`, `tls.protocol.version`, `tls.cipher`, `server.address` — SNI for TLS-MITM HCM + TCP/SSH; Host header override on plaintext HCM where SNI is unavailable, `client.address`, `network.peer.address`, `network.peer.port`) + clawker firewall verdict (`action`: `allowed`/`denied`) — TCP-level filter chains hardcode `action` (uniform verdict), HTTP HCMs substitute via `%METADATA(ROUTE:clawker:action)%` from per-route `clawkerActionMetadata()`. A path rule's `Path` becomes the route's `RouteMatch` path specifier (`envoy_http.go::pathSpecifier`): a literal path → open-ended `prefix`; a `~`-prefixed path → `safe_regex` (RE2, full-string match — `~` stripped, `google_re2` engine field omitted) so authors can anchor exactly and use alternation, closing the open-prefix bypass (`/repos/x` prefix also admitting `/repos/x-evil`). `ValidateRule` (`rules_store.go`) guards both forms before they reach generation — literal must start `/` and contain only RFC 3986 path characters (`literalPathChars`; rejects a regex written without the `~` marker), regex must compile (Go `regexp` is RE2, exact compile-compat) and anchor at the path root — failing the whole rule-update on any invalid path. A path rule's `Methods` add a `:method` `RouteMatch.headers` matcher (`exact` for one method, `safe_regex` alternation for many — `envoy_http.go::methodHeaderMatch`) narrowing that route to the listed HTTP verbs; non-matching verbs fall through to later routes / `path_default`. HTTP-family only — `methods`/`path_rules` on opaque protos (tcp/ssh/udp) are ignored at generation, surfaced as a `NormalizeAndDedup` warning (`pathRuleEnforcementWarning`). Every HCM merges in `httpConnectionManagerHardening()` (normalize_path / merge_slashes / path_with_escaped_slashes_action / headers_with_underscores_action / max_concurrent_streams) — load-bearing for path-rule enforcement against URL-encoded traversal. No timeouts or per-connection buffer caps: LLM workloads run for minutes with multi-MB bodies, Envoy defaults are correct. Centralized `firewallBlockedBody` constant for `direct_response: 403` bodies (non-fingerprinting). The `otel_collector_als` cluster dials the CP-only `otlp/infra` receiver on `OtelInfraPort` with an upstream TLS transport_socket (leaf+intermediate bind-mounted at `/etc/envoy/otel-tls/`, CLI root CA at `ca.pem` for server-cert verification). When `als.MTLS=false` the OTel sink AND cluster are both omitted at the sender (gated in `buildHTTPAccessLog` / `buildTCPAccessLog` / `buildClusters`) — Envoy keeps only the stdout JSON sink for triage. Infra services must never cross into the untrusted `otel-collector:4317` lane reserved for agent containers. `normalizeDomain` lives here — used by certs, coredns_config, rules_store, and by the IdentityAllocator's dst normalization |
| Per-svc OTel mTLS material | Provided by `*otelcerts.Service` — see `internal/controlplane/otelcerts/CLAUDE.md`. `Stack` holds an `OtelCertProvisioner` reference and dispatches one `EnsureClient` call per sibling (envoy, coredns) inside `ensureConfigs` so `Reload` rotates with the config refresh. No-op when the provisioner is nil — stdout-only degraded mode: Envoy emits no OTel access logs (sink + cluster dropped); CoreDNS otel plugin installs noopEmitter. Atomic write, pair-check, and 0o755/0o644 perms are owned by the provisioner. Note: netlogger's mTLS material is NOT provisioned by `firewall.Stack` — `cmd/clawkercp/main.go` mints its per-handshake leaf directly via `otelcerts.Service.LoadTLSConfig("netlogger")` and hands the resulting `*tls.Config` to `controlplane.NewOtelLoggerProvider`. |
| `coredns_config.go` | Corefile generation; wildcard rules → subtree-forward zones; exact-only rules → forward apex + NXDOMAIN-subdomain template (`fallthrough`); deny rules → dedicated NXDOMAIN zones (win via longest-zone match); `dnsbpf` plugin directive; catch-all NXDOMAIN |
| `certs.go` | CA keypair generation/loading; per-domain cert signing; wildcard SANs; `RotateCA` |
| `rules_store.go` | `EgressRulesFile` schema + the **`EgressRulesStore` interface** and its unexported impl (embeds `*storage.Store[EgressRulesFile]`) + the constructor pair `NewRulesStore(cfg)` (file-backed) / `NewRulesStoreFromString(yaml)` (in-memory seam), both returning the interface + rule helpers (`ValidateDst`, `NormalizeRule`, `RuleKey`, `NormalizeAndDedup`). Every rule read/write in the package goes through the interface — no consumer holds a `storage.Store`. Rule composition lives in `internal/bundler` (`bundler.EgressRules`) — firewall doesn't compose harness or project rules. `RoutesFromRules(rules, ports, idFor IdentityResolver) ([]ebpf.Route, []string)` is the pure projection behind `EgressRulesStore.Routes`; a resolver miss drops the route (fail closed) and is reported in the missed-dst return — `Handler.routesFromStore` logs partial misses as `event=identity_resolver_miss`. |
| `identity.go` | `IdentityAllocator` — sticky persisted route identities (typed `ebpf.RouteIdentity`, a named u32; cilium pattern). The **`RouteIdentityStore` interface** (`Entries`/`Cursor`/`SetTable`) and its unexported impl (embeds `*storage.Store[IdentityTableFile]`), the constructor pair `NewIdentityStore(cfg)` / `NewIdentityStoreFromString(yaml)` returning that interface, + `NewIdentityAllocator(store RouteIdentityStore)` (`ErrNilIdentityStore` on nil); `SyncDsts` (set-diff acquire/release), `IdentityFor`/`DomainFor`/`Snapshot`; allocatable band starts at `MinIdentity=256` (0 = none, 1–255 reserved), round-robin next-free so released IDs aren't reused prematurely; table persisted to `route-identities.yaml` in `FirewallDataSubdir`. `indexIdentityEntries` is the one table validator, run by both `NewIdentityAllocator` (load) and `SetTable` (write). Live dsts are never renumbered. `IdentityResolver` is the read-side func type consumed by `RoutesFromRules`/`GenerateCorefile`. |
| `network.go` | `NetworkInfo` + `DiscoverNetwork(ctx, *docker.Client, cfg)` + `ComputeStaticIP(gateway, lastOctet)` |
| `embed_coredns.go` | `//go:embed assets/coredns-clawker` — exported `CoreDNSClawkerBinary` |
| `errors.go` | Sentinels (`ErrEnvoyUnhealthy`, `ErrCoreDNSUnhealthy`, `ErrCPUnhealthy`) + `HealthTimeoutError` |
| `ebpf/` | eBPF subsystem — see `ebpf/CLAUDE.md` |
| `mocks/` | Moq-generated mocks (`EgressRulesStoreMock`, `RouteIdentityStoreMock`) for the two store-backed domain interfaces — regenerate with `go generate ./...`, never hand-edit. Black-box test files (`package firewall_test`) can import it; internal test files (`package firewall`, `*_internal_test.go`) cannot (import cycle). This package's own store tests use REAL stores by design — the store is the subject — regardless of which test package they sit in (see Test Patterns). |
| `testdata/` | Golden files (e.g., `corefile_basic.golden`) |
| `assets/` | `coredns-clawker` Linux binary (gitignored; built by `make coredns-binary`) |

## Handler RPCs (B2 scope-corrected surface — 13 methods)

Every RPC requires the uniform `"admin"` scope (INV-B2-009). Per-method scope diversification is intentionally not used.

| RPC | Scope | Purpose |
|-----|-------|---------|
| `FirewallInit` | global | Idempotent stack-up: `ensureConfigs` → ensure Envoy/CoreDNS images → ensure containers attached to the clawker network at static IPs → `WaitForHealthy`. Returns Envoy/CoreDNS IPs + network ID. BPF attach happens at CP startup, not here. Besides the RPC callers (`firewall up`, `controlplane up`, container-start bootstrap), the CP daemon itself invokes the handler method in-process as a pre-`SetReady` startup gate when `firewall.enable` (settings.yaml) is true (the settings-driven startup gate in `cmd/clawkercp/main.go`; failure fails CP startup, exit code 1) — the ActionQueue serializes the two paths. |
| `FirewallRemove` | global | Global teardown (queued, `ActionTeardown`): `CancelAllBypassTimers` → `Stack.Stop` → `ebpf.Manager.FlushAll` (wipe container_map + bypass_map + unpin links) → delete generated `envoy.yaml` + `Corefile`. **The egress rules store is preserved** so a subsequent `firewall remove <domain>` lands in the authoritative file and takes effect on next `firewall up` (trailing-mutation security invariant). |
| `FirewallEnable(container_id)` | per-container | Idempotent enroll. `resolveForEnable` → Docker lookup → fresh `cgroup_id` via `EBPFCgroupPath`. BPF `container_config` is built CP-side from `Stack.NetworkInfo` (Envoy/CoreDNS/gateway/CIDR) + `cfg.EnvoyEgressPort()` + `resolveHostProxy` (resolves `host.docker.internal` when the project has host proxy enabled). Writes `container_map` + attaches links via `ebpf.Manager.Install` + clears any bypass flag. Drift guard logs stored-vs-fresh cgroup_id delta. Returns `FailedPrecondition` if Docker says the container is gone. Note: the bypass dead-man timer does NOT re-run `Install` — it calls the cheap `ebpf.Manager.Enable` path (clears bypass flag only). Full re-enroll happens only on the explicit `FirewallEnable` RPC. **Side effect**: after the `container_map` write succeeds, publishes `ebpf.EBPFContainerEnrolled{CgroupID, ContainerID, OccurredAt}` on the typed `EnrolledTopic` (nil-tolerant — test wiring without a topic skips the publish). netlogger subscribes to this event to hydrate its label cache — but only for RPC-path `FirewallInit`/`FirewallEnable`: the settings-driven startup gate runs its `FirewallInit` before netlogger is constructed, so those enroll events are dropped and the LabelCache stays cold until the next RPC-path sweep (see the step-9 caveat in `internal/controlplane/CLAUDE.md`; telemetry enrichment only, enforcement unaffected). |
| `FirewallDisable(container_id)` | per-container | Set BPF bypass for the container. Falls back to stored `cgroup_id` when Docker reports the container gone; no-op for unknown containers (both paths reach `ebpf.Manager.Disable`). |
| `FirewallBypass(container_id, timeout)` | per-container | `FirewallDisable` + `time.AfterFunc` that calls drift-guarded `Enable` on expiry (`bypassTimerFired` → `resolveBypassCgroupID` → `ebpf.Manager.Enable`). Caps at `maxBypassTimeout = 1h`. Stores `storedCgroupID[cid]` so mid-bypass Disable on a now-gone container can still clear the orphan bypass_map entry. |
| `FirewallAddRules` | global | Pre-Submit: validation only (`ValidateRule` per rule — pure, no store). The mutation runs as a queued `ActionRuleMutate` closure: `EgressRulesStore.AddRules` (additive merge: caller wins on `Action`; caller wins on `PathDefault` only when non-empty (empty incoming preserves the stored value so a bare CLI add doesn't clobber a yaml-set default); `PathRules` union by `Path` with caller winning on path collision — see `MergeRule` in `rules_store.go`) + `store.Write`. Per-rule outcome reported on `FirewallAddRulesResult.statuses` (`statuses[i] ↔ req.rules[i]`, input order preserved): `ADDED` / `MODIFIED` / `UNCHANGED`. The `reflect.DeepEqual` gate makes identical re-seeds a true no-op — every entry comes back `UNCHANGED`, `store.Write` is skipped, no reconcile fires. When at least one rule is `ADDED` or `MODIFIED`, Submit `reconcileStackClosure` (`ActionReconcile`) — inside the closure, if the stack is running call `Stack.Reload` + `ebpf.Manager.SyncRoutes`; if down, no-op. Response carries `stack_restarted=false` for the stack-down path so the CLI can emit the "takes effect on next `firewall up`" note. |
| `FirewallRemoveRule` | global | Removal keyed by `(dst, proto, port)`; optional `path` field narrows the operation to a single `PathRule` entry (`EgressRulesStore.RemovePathRule`) while leaving the rule itself in place. The `all` field wipes every stored rule in one mutation + one reconcile (`EgressRulesStore.RemoveAll` — the `firewall prune` primitive); `all` alongside any of dst/proto/port/path is `InvalidArgument`, and an already-empty store reports NOT_FOUND like a single-rule miss. The lookup + removal run as a queued `ActionRuleMutate` closure, keyed by `RuleKey` (and by `Path` when set). Outcome on `FirewallRemoveRuleResult.status`: `REMOVED` (whole rule deleted), `PATH_REMOVED` (single PathRule entry deleted, rule remains), `NOT_FOUND` (key miss or — when `path` set — path miss). NOT_FOUND travels as a response status, NOT as a gRPC `codes.NotFound` error — genuine store-I/O failures still return as gRPC errors. On match: store write + shared `reconcileStackClosure`. No `ValidateDst` on this path — anything unmatched collapses into the same NOT_FOUND outcome. On the keyed/path forms the CLI exits non-zero on NOT_FOUND so a typo, wrong proto/port, or unknown path never silently succeeds; on the `all` form `firewall prune` treats an empty store as an informational no-op because its contract is the end state. |
| `FirewallListRules` | global | Read-only normalized rule dump from the store. |
| `FirewallStatus` | global | `Stack.Status` — per-container up state, Envoy/CoreDNS IPs, network ID, rule count. Network-discovery errors log at Warn and leave topology empty; per-container `isRunning` is authoritative for "stack down". |
| `FirewallReload` | global | Regenerate configs and restart the stack without rule mutation. |
| `FirewallRotateCA` | global | Regenerate MITM CA + per-domain certs and `Stack.Reload`. |
| `FirewallSyncRoutes` | global | Break-glass route re-sync. Routed through `reconcileStackClosure`, which rebuilds routes from the **current rules store** (not the caller-supplied proto rules — those are ignored so two coalesced SyncRoutes calls can't smuggle different inputs past the head-wins coalescer). The reported `applied` count is carried out on `StackReloadResult.RoutesApplied` from inside that closure, never re-read afterwards: a rule mutation queued behind the reconcile would land between the two and make the count describe a set this call never pushed. Zero when the stack was down (nothing synced). |
| `FirewallResolveHostname` | global | DNS lookup from CP netns (used by container enroll for `host.docker.internal` resolution). |

## Types

### `Handler` + `HandlerDeps`

```go
type HandlerDeps struct {
    EBPF          ebpf.EBPFManager    // required — every RPC hits it
    Stack         StackLifecycle      // optional — stack-up/down RPCs no-op if nil
    Store         EgressRulesStore    // optional — reconcile/route paths no-op if nil; ListRules/RotateCA/AddRules/RemoveRule fail loud instead of panicking
    Cfg           config.Config       // optional — read for rule defaults, CPIPLastOctet, etc.
    Resolver      ContainerResolver   // required — per-container RPCs
    Log           *logger.Logger      // optional — defaults to Nop
    Queue         *ActionQueue        // required — every RPC submits through it
    EnrolledTopic *pubsub.Topic[ebpf.EBPFContainerEnrolled] // optional — nil-tolerant; FirewallEnable skips publish when nil
    CertDirFn     func() (string, error) // optional — certs path for RotateCA
    ListAgents    func(ctx context.Context) ([]string, error) // optional — nil skips agent re-enrollment on FirewallInit
    Identity      *IdentityAllocator  // optional — nil degrades fail-closed (no routes/dnsbpf directives; event=identity_allocator_unset)
}

func NewHandler(deps HandlerDeps) (*Handler, error)  // ErrNilEBPFManager / ErrNilResolver / ErrNilQueue on missing required deps
```

The `Queue` is a single-goroutine FIFO worker (see `queue.go`) that
serializes all 13 firewall RPCs so rapid-fire rule mutations coalesce
into one stack restart instead of colliding mid-restart. Rule-CRUD,
Reload, RotateCA, and SyncRoutes submit `reconcileStackClosure`
(coalescing kind `ActionReconcile`); per-container RPCs submit their
own non-coalescing closures under `ActionEnable` / `ActionDisable` /
`ActionBypass`; reads run under `ActionRead`. Submit is close-safe:
post-`Close` submissions receive `ErrClosed` via a pre-closed reply
channel, which the Handler translates to `ErrQueueClosed` +
`codes.Unavailable` for CLI callers.

`ActionKind.Coalesces` is an exhaustive switch over every kind with no
default arm, so a kind added later cannot inherit a coalescing semantic by
omission — the linter makes the author state it. Inheriting the wrong one
silently drops a submitter's work.

### `Stack`

```go
type Stack struct { /* docker.Client, config.Config, logger, EgressRulesStore */ }

func NewStack(dc *docker.Client, cfg config.Config, log *logger.Logger, store EgressRulesStore, otelCerts OtelCertProvisioner, idFor IdentityResolver) *Stack  // nil idFor = fail-closed stub (no dnsbpf directives; event=identity_resolver_unset)
func (s *Stack) EnsureRunning(ctx) error
func (s *Stack) Stop(ctx) error
func (s *Stack) Reload(ctx) error
func (s *Stack) WaitForHealthy(ctx) error
func (s *Stack) Status(ctx) (*Status, error)
func (s *Stack) NetworkInfo(ctx) (*NetworkInfo, error)
func (s *Stack) EnvoyIP() string
func (s *Stack) CoreDNSIP() string
func (s *Stack) NetworkID() string
func (s *Stack) CIDR() string
```

`StackLifecycle` is the Handler-facing interface — `*Stack` satisfies it. It exposes `EnsureRunning`, `Stop`, `Reload`, `Status`, and `NetworkInfo`; `WaitForHealthy` is on `*Stack` directly but is not part of the interface. Keep Handler unit-testable by passing a Stack fake.

### `ContainerResolver`

```go
type ContainerResolver func(ctx context.Context, ref string) (id, cgroupPath string, exists bool, err error)
```

- `exists=false` + `err=nil` is the "container gone" signal — drives `FirewallEnable`'s `FailedPrecondition` and `FirewallDisable`'s stored-cgroup fallback.
- Production wiring: `cmd/clawkercp/main.go::containerResolverFromDocker` uses `*docker.Client` + `IsCanonicalContainerID` so short-ref NotFound doesn't silently drop enforcement state.

### `EgressRulesStore` — the rules-file domain facade

```go
type EgressRulesStore interface {
	Rules() ([]config.EgressRule, []string, error)                                  // canonical rules + normalization warnings
	Routes(ports EnvoyPorts, idFor IdentityResolver) ([]ebpf.Route, []string, error) // BPF route projection + missed (identity-less) dsts

	AddRules(incoming []config.EgressRule) ([]AddStatus, error)                     // merge-add; one status per input rule
	RemoveRule(target config.EgressRule) (matched bool, err error)                  // delete by RuleKey
	RemovePathRule(target config.EgressRule, path string) (matched bool, err error)  // delete one PathRule entry
	RemoveAll() (matched bool, err error)                                           // wipe the store in one write; false = already empty
	Canonicalize() (healed bool, err error)                                         // rewrite when the on-disk shape differs from canonical
}

func NewRulesStore(cfg config.Config) (EgressRulesStore, error)      // file-backed (filenames + default-filename guard + FirewallDataSubdir + flock)
func NewRulesStoreFromString(seed string) (EgressRulesStore, error)  // in-memory seam: no path options, no disk, writes error by design
```

**There is no whole-schema read.** Every read answers with the CANONICAL set
(`NormalizeAndDedup` applied) because that is the only shape the generators, the
cert pass, the route projection, and the rule count may act on. `Rules()`
surfaces a read failure rather than folding it to "no rules" — an empty set
wipes generated listeners and route_map entries, so an unreadable store must
never masquerade as one.

**Write serialization is the ActionQueue's job, not the store's.** The schema
has a single `rules` field, so every writer rewrites the whole list from its
own earlier read — two concurrent writers would be a lost-update race. The
store carries no lock for this: ALL writers run on the queue's single worker.
RPC mutations (`AddRules`/`RemoveRule`/`RemovePathRule`) execute inside queued
`ActionRuleMutate` closures (non-coalescing — coalescing would drop the
collapsed submitter's mutation) and the canonical heal (`Canonicalize`) runs
inside the bringup/reconcile closures via `Stack.ensureConfigs`. The engine
below provides per-operation thread safety, atomic temp+rename writes, and the
cross-process flock; same-path writes from other processes resolve to
last-writer-wins by design.

**`Canonicalize` compares the WHOLE canonical shape.** The heal fires whenever
the stored rules differ from `NormalizeAndDedup`'s output in any field it
touches — defaulted proto/action/port, a dropped duplicate, a carved opaque port
span, and a path rule's `methods` after uppercase/dedup/sort. A field-subset
comparison leaves a file that differs only in an uncompared field reporting
"already canonical" forever, so the on-disk shape never converges. It runs once
per bringup/reconcile; an empty file is never rewritten.

**Normalization warnings need a logger, and the store has none.** Every
canonical read returns them; the Handler is the layer that surfaces them
(`logNormalizeWarnings`). The mutating RPCs log them from inside their
`ActionRuleMutate` closure, and `reconcileStackClosure` logs them BEFORE it
branches on stack state — a path rule silently unenforced on an opaque proto
must reach the operator whether or not Envoy happens to be up.

### `RouteIdentityStore` — the identity-table domain facade

```go
type RouteIdentityStore interface {
	Entries() ([]IdentityEntry, error)                        // persisted allocations; unset table → none
	Cursor() (int64, error)                                   // round-robin cursor; unset → 0 (allocator applies MinIdentity)
	SetTable(entries []IdentityEntry, cursor int64) error      // entries + cursor persisted as one unit
}

func NewIdentityStore(cfg config.Config) (RouteIdentityStore, error)
func NewIdentityStoreFromString(seed string) (RouteIdentityStore, error)
```

Neither read folds a read FAILURE: an unreadable table presented as an empty one
would renumber every live identity on the next sync — the exact `dns_cache`
aliasing bug the sticky table exists to prevent. `SetTable` validates at the
write front door with the SAME check the load path runs
(`indexIdentityEntries`: every ID inside the allocatable band, dst and ID both
unique) and returns a wrapped `firewall: ...` error on a violation — a shape the
writer accepts but the loader refuses would surface as a CP that boots fine
today and refuses to boot tomorrow, with every enrolled agent already
fail-closed and no supervisor to explain why. `IdentityAllocator.mu`
serializes the table's read-modify-write — the in-memory `byDst`/`byID` maps
are the mutated state and the allocator is the table's only writer; the store
itself carries no lock (engine per-operation safety + flock below it).

### `EgressRulesFile` + rule helpers

`EgressRulesFile` is the on-disk schema (`egress-rules.yaml`) — it implements `storage.Schema` via `Fields()` so the store engine can read field metadata. Rule composition lives outside this package — `bundler.EgressRules(cfg, harness)` layers the harness's required egress floor over the project's `config.Config.ProjectEgressRules()` contribution (`security.firewall.rules` + `add_domains`); the firewall package owns store/stack/certs, not rule composition. `BootstrapServicesPreStart` (`internal/cmd/container/shared/container_start.go`) calls `bundler.EgressRules(cfg, harnessName)` and passes the result through `adminv1.EgressRulesToProto` to `FirewallAddRules`. The `clawker firewall refresh` CLI verb re-runs this exact `bundler.EgressRules` → `EgressRulesToProto` → `FirewallAddRules` sync on demand (no restart), so a `clawker.yaml` egress edit can be live-applied; it is add/update-only (no prune — removed domains are deleted via `firewall remove`).

Rule helpers are exported for reuse by `BootstrapServicesPostStart` and E2E tests:

- `ValidateDst(dst string) error` — domain syntax + wildcard rules + length
- `NormalizeRule(r)` — lowercase dst, trim leading `*.`, etc.
- `RuleKey(r) string` — dedup key (`dst:proto:port`)
- `MergeRule(existing, incoming) EgressRule` — same-RuleKey merge used by `EgressRulesStore.AddRules`. Caller wins on `Action`; caller wins on `PathDefault` only when non-empty (empty incoming preserves the stored value); `PathRules` union by `Path` (caller wins on same-`Path` collision). The single merge semantic used by both yaml-driven bootstrap reseeds and CLI `firewall add`.
- `NormalizeAndDedup(rules) ([]EgressRule, []string)` — canonical form + dropped-duplicate notes

Wire↔config rule translation (`EgressRulesToProto` / `EgressRulesFromProto`) is NOT here — it lives beside the generated bindings in `api/admin/v1/conversion.go` so the gRPC types stay confined to the transport edge and both server and CLI share one converter without importing this (embed-heavy) package.

## Invariants

- **INV-B2-007 drain ordering**: `ActionQueue.Close` → `grpcServer.GracefulStop` → `Handler.CancelAllBypassTimers` → `Stack.Stop` → `ebpf.Manager.FlushAll`. Closing the queue first makes in-flight RPCs observe `ErrClosed` on any pending Submit and return promptly, so `GracefulStop` unblocks quickly; `Stack.Stop` / `ebpf.FlushAll` run post-Close directly from `cmd/clawkercp/main.go` because the queue is gone. See `../CLAUDE.md` for the drain callback composition.
- **INV-B2-009 uniform scope**: every RPC in `AdminMethodScopes` maps to `"admin"`. `TestAdminMethodScopes_CoversAllRPCs` reflects over `AdminService_ServiceDesc` so a new RPC without a scope entry fails the build.
- **INV-B2-013 defensive startup cleanup**: `ebpf.Manager.CleanupStaleBypass` runs before `orchestrator.SetReady()`. Any error here fails startup (by design — a broken drain should not silently bless stale BPF state).
- **INV-B2-016 drift guard**: `FirewallEnable` always resolves `container_id → cgroup_path` via Docker, logs warning on stored-vs-fresh `cgroup_id` delta, returns `FailedPrecondition` if Docker says the container is gone. Bypass dead-man timer goes through the same `resolveBypassCgroupID` helper.
- **Route identities are allocated, never derived**: `IdentityAllocator` (`identity.go`) mints a sticky `ebpf.RouteIdentity` (named u32) per normalized destination (round-robin from `MinIdentity=256`; 0 = none — `ebpf.RouteIdentity.IsNone`; 1–255 reserved), persisted in `route-identities.yaml` under `FirewallDataSubdir` via `internal/storage`. Live destinations are NEVER renumbered across rule churn or CP restarts — the pinned `dns_cache` is populated asynchronously by CoreDNS and would alias other domains' routes if identities moved. `RoutesFromRules` and `GenerateCorefile` both take an `IdentityResolver`; a resolver miss fails closed (no route, no `dnsbpf` directive) and is reported in each function's missed-dst return — callers with a logger emit `event=identity_resolver_miss` for partial misses (`Handler.routesFromStore` for routes, `Stack.ensureConfigs` for the Corefile); the deliberate full degrades stay on `event=identity_allocator_unset` / `event=identity_resolver_unset`. dnsbpf receives each zone's identity as the `dnsbpf <identity>` Corefile directive argument, so all three writers share one allocation by construction.
- **Static IPs**: Envoy/CoreDNS/CP use `ComputeStaticIP(gateway, cfg.EnvoyIPLastOctet()/CoreDNSIPLastOctet()/CPIPLastOctet())`. Static-IP assignment cannot go through whail's `EnsureNetwork` helper — use `dc.EnsureNetwork` first, then explicit `NetworkingConfig.IPAMConfig.IPv4Address` in `ContainerCreate`.

## Imports

- **Uses**: `internal/config`, `internal/consts`, `internal/docker`, `internal/logger`, `internal/storage`, `internal/controlplane/firewall/ebpf`, `api/admin/v1`, `pkg/whail` (labels only), `github.com/moby/moby/api/types/*`.
- **Used by**: `internal/controlplane` (composite server embeds `*Handler`; startup wires `Stack`); `cmd/clawkercp/main.go` (Handler ctor + container resolver).
- **Not imported by**: CLI commands — those go through `f.AdminClient(ctx)` which speaks gRPC to the running CP. No direct Go calls into `firewall.Handler` from CLI code. Wire↔config rule translation (`adminv1.EgressRulesToProto`/`EgressRulesFromProto`/`EffectivePathDefault`) lives in `api/admin/v1`, so the container-start path (`BootstrapServicesPreStart`) and `firewall refresh` convert `bundler.EgressRules(cfg, harness)` output without importing this package.

## Test Patterns

- **Unit tests (`handler_test.go`, `stack_test.go`, `cgroup_test.go`)** — use `docker/mocks.FakeClient` + `controlplane/firewall/ebpf/mocks.EBPFManagerMock`. Handler fakes satisfy `StackLifecycle`; test-only `ContainerResolver` closures drive drift + not-found branches.
- **Store tests use REAL stores, never the mocks** — file-backed via `configmocks.NewIsolatedTestConfig(t)` + `NewRulesStore`/`NewIdentityStore` (isolated `FirewallDataSubdir`, real merge + atomic write), or in-memory via `NewRulesStoreFromString`/`NewIdentityStoreFromString` when a seeded rule set is all that's needed (`envoy_config_test.go`). `mocks/` exists for consumers of the interfaces — black-box `firewall_test` files can import it too; only internal test files (`package firewall`) can't (import cycle). The store tests avoid it by design, not by restriction: the store IS the subject.
- **Sibling drift tests (`stack_drift_internal_test.go`)** — `ensureContainer` recreate-vs-adopt on `stack_build_sha` drift (different value AND legacy missing label), drift-label provenance, and the production spec constructors carrying the drift label set. Test seam: `overrideCPBinarySHAForTest` swap-and-restores the package-init'd `consts.CPBinarySHA` (same approach as `overrideHostPathsForTest` in `container_spec_test.go`).
- **FakeClient managed-label jail**: `whail.ContainerInspect` re-invokes `ContainerInspectFn` inside `IsContainerManaged` — test fakes must return `Config.Labels[managedKey]=ManagedLabelValue` in inspect responses, otherwise real callers see `ErrContainerNotFound`.
- **Stop/Reload no-op tests** need affirmative assertions (`NotContains(fake.Calls, "ContainerStop")`, `FileExists(envoy.yaml)`) or they pass trivially without exercising the short-circuit.
- **Envoy-gen tests (`envoy_config_test.go`)** — ONE comprehensive golden, NOT one-per-feature. New coverage (any new proto/dst-type/path/ws/DFP/QUIC/cert/port-range permutation or interaction) is added by extending the `comprehensiveRules` const + re-blessing `comprehensive`/`comprehensive_mtls`, NOT by adding a new `*.envoy.golden` per feature. The only standalone cases allowed are generation-wide-fact-OFF shapes a mega-config can't express (`http_exact_only`/`https_exact_only` = DFP absent, `ssh` = no egress listener/deny floor) and fail-closed (`wantErrContains`) cases. Full rules: `.claude/rules/envoy.md` → Testing §.
- **Golden files**: `testdata/corefile_basic.golden` and `testdata/corefile_wildcard_deny.golden` are hand-edited to update (no `GOLDEN_UPDATE=1` hook). `testdata/envoy/*.envoy.golden` re-bless via `GOLDEN_UPDATE=1 go test ./internal/controlplane/firewall/ -run TestGenerateEnvoyConfig`.
- **E2E tests**: `test/e2e/firewall_test.go` (composite flows through the CLI — blocked domain, allowed domain, add/remove rules, status, path rules, bypass end-to-end including natural-expiry + gone-container error paths) and `test/e2e/controlplane_cli_test.go` (break-glass `controlplane up/status/down` verbs). E2E means through `harness.Run(...)` — no direct `Stack`/`Handler` construction belongs under `test/e2e/`.

## Gotchas

- `APIClient.ImagePull` / `ImageBuild` only return a top-level error on initial HTTP failure; auth/manifest/layer errors stream as JSON frames with an `error` field. Always drain via `drainPullStream`/`drainBuildStream` and surface `msg.Error`.
- `cerrdefs.IsNotFound` does NOT match whail's `*DockerError{Op: "network_find"}` wrapping. Substring-match on `"not found"` false-positives (`"image not found"`, `"endpoint not found"`). In Status, log network-discovery errors at Warn and leave topology fields empty — per-container `isRunning` distinguishes "stack down" from "Docker unreachable".
- `HandlerDeps.Store` being nil turns the reconcile/route paths into no-ops and makes `FirewallListRules` / `FirewallRotateCA` / `FirewallAddRules` / `FirewallRemoveRule` return a `codes.Internal` wiring-fault status naming the missing dep instead of nil-dereffing inside a queued closure (which comes back as a recovered-panic result that names nothing). Intentional for unit tests; `cmd/clawkercp` always wires a real store.
- A reconcile that fails AFTER a rule mutation committed returns an error saying the change WAS saved and takes effect on the next `firewall up` / `firewall reload`, wrapping the reconcile error. Reporting a bare failure for a durable write is the dangerous direction: a user who removed a deny rule would believe the block still stands.
- `HandlerDeps.Stack` being nil silently turns stack-up/down RPCs into no-ops. Intentional for unit tests, but a production wiring bug would hide here — `cmd/clawkercp/main.go` must always wire a real `*Stack`.

## See Also

- `../CLAUDE.md` — CP core (Ory auth, startup sequencing, container config, drain callback composition)
- `ebpf/CLAUDE.md` — eBPF subsystem details + pinned map contract
- `.claude/rules/envoy.md` — Envoy config rules + verification workflow
- `.claude/rules/firewall-uat.md` — runtime BEHAVIORAL UAT (in-container probe tools, allow/deny/upgrade/SSH-routing discriminators, live config spot-check, C2 harness). Golden+validate prove the config is valid; this proves it enforces.
