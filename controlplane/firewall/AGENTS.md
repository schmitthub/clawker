# Controlplane Firewall Subpackage

Firewall domain under the control plane. Owns the egress enforcement surface: Envoy + CoreDNS config generation, MITM CA + per-domain certs, egress rules store, eBPF manager (under `ebpf/`), CoreDNS binary embed, Docker network discovery, cgroup resolution, and the gRPC firewall-domain handler on `adminv1.AdminServiceServer`.

The egress rules and route identity stores follow the Store-backed package contract in `internal/storage/AGENTS.md`. Read it before changing either store.

## Architecture

```
clawker CLI
    │  f.AdminClient(ctx) — mTLS + OAuth2 JWT
    ▼
internal/controlplane/adminServer  (embeds *firewall.Handler)
    │
    ▼
firewall.Handler (13 RPCs)
    │  pre-Submit validation; CA rotation holds the CAStore lock;
    │  rule-store writes and stack operations run inside a queued
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
    ├── CAStore       → shared CA owner; rotation also runs before Submit
    └── EnrolledTopic → EBPFContainerEnrolled (drives netlogger LabelCache hydration)
```

- **No host-side daemon**: `internal/firewall/` is gone. Lifecycle authority is the `clawker-controlplane` container (see `../CLAUDE.md` for startup sequencing). CP bringup is owned by the explicit bootstrap verbs (`controlplane up`, `firewall up`, container start) via `Manager.Start`; when the `AgentWatcher` observes drain-to-zero + grace, the CP self-shuts-down (INV-B2-007).
- **Composite server**: `controlplane.adminServer` embeds `*firewall.Handler`; Go method promotion surfaces all 13 RPCs. Future domain handlers (monitor, hostproxy, clawkerd) embed alongside.
- **Per-container RPCs carry only `container_id`**: path resolution is hidden behind the injected `ContainerResolver`. The wiring in `cmd/clawkercp/main.go::containerResolverFromDocker` calls `DetectCgroupDriver` once at CP startup and captures the driver string in the resolver closure; every RPC call goes through the resolver, which invokes `ResolveContainerID` and then resolves the cgroup path (INV-B2-016 drift guard): the conventional rootful layout (`EBPFCgroupPath`) when it exists on disk, otherwise a one-time discovery walk of `/sys/fs/cgroup` for the target container's own directory (rootless daemons park scopes under the user slice at a uid-dependent depth) with the found parent cached per resolver. A cgroup that exists nowhere while Docker says the container is alive is a loud error, never a fabricated path. The Handler itself holds no cgroup driver state.

## Files

| File | Purpose |
|------|---------|
| `handler.go` | `Handler` + `HandlerDeps` + `ContainerResolver` + `StackLifecycle` — 13 RPCs, bypass timer management. Rule mutation itself lives on `EgressRulesStore` (`rules_store.go`); the Handler calls it and owns only the logging + RPC mapping around it. Wire↔config rule translation lives beside the proto bindings in `api/admin/v1` (`EgressRulesToProto`/`EgressRulesFromProto`), not here |
| `stack.go` | `Stack` — Envoy + CoreDNS container lifecycle via DooD; image build helpers (`drainPullStream`, `ensureEnvoyImage`, `ensureCorednsImage`); health probing; `EnsureRunning`/`Stop`/`Reload`/`WaitForHealthy`/`Status` + IP/CIDR accessors. Sibling drift gate: `driftLabels()` stamps six labels on both containers — `labelInfraCertsReady` (telemetry certificate readiness), `labelSDSCertsReady` (SDS certificate readiness), `labelOtelInfraPort` (OTLP port), `labelSDSPort` (`ControlPlaneSettings.SDSPort`, read by Envoy at startup), `labelStackBuildSHA` (`consts.CPBinarySHA`), and `labelBPFFSSource` (`consts.HostBPFFSSource`). `ensureContainer`/`reloadContainer` compare them against the running container and recreate on any mismatch (`event=firewall_container_spec_drift`). The build SHA covers every compiled-in staleness vector — pinned Envoy image const, embedded CoreDNS binary, config templates, containerSpec shape — so a CLI upgrade that replaces the CP also replaces the siblings instead of adopting stale ones. |
| `status.go` | `Status` struct returned by `Stack.Status` (per-container up state, IPs, rule count) |
| `cgroup.go` | `DetectCgroupDriver(ctx, *docker.Client)`, `EBPFCgroupPath(driver, cid)` (conventional rootful layout — fast path only), `cgroupPathResolver` (discovery fallback: walks the hierarchy for the container's own cgroup dir, caches the per-daemon parent — how rootless layouts resolve), `ResolveContainerID(ctx, *docker.Client, ref)`, `IsCanonicalContainerID` |
| `drift.go` | `resolveBypassCgroupID(entry, resolver, log)` — shared INV-B2-016 drift resolver used by direct Enable (`resolveForEnable`) and the bypass dead-man timer |
| `envoy_config.go` | Envoy YAML generation; per-domain filter chains; LOGICAL_DNS clusters; TCP/SSH listeners; access log builder (stdout JSON for `docker logs` triage, plus native `envoy.access_loggers.open_telemetry` OTLP/gRPC sink when mTLS material is wired). Rule routing by `proto:` (`https` → TLS-MITM HCM, `http` → plaintext HCM, `ssh`/`tcp`/other → opaque TCP listener). Per access-log record: OTel semconv fields for network/server/client/tls (`network.transport`, `network.protocol.name`, `network.protocol.version`, `tls.established`, `tls.protocol.version`, `tls.cipher`, `server.address` — SNI for TLS-MITM HCM + TCP/SSH; Host header override on plaintext HCM where SNI is unavailable, `client.address`, `network.peer.address`, `network.peer.port`) + clawker firewall verdict (`action`: `allowed`/`denied`) — TCP-level filter chains hardcode `action` (uniform verdict), HTTP HCMs substitute via `%METADATA(ROUTE:clawker:action)%` from per-route `clawkerActionMetadata()`. A path rule's `Path` becomes the route's `RouteMatch` path specifier (`envoy_http.go::pathSpecifier`): a literal path → open-ended `prefix`; a `~`-prefixed path → `safe_regex` (RE2, full-string match — `~` stripped, `google_re2` engine field omitted) so authors can anchor exactly and use alternation, closing the open-prefix bypass (`/repos/x` prefix also admitting `/repos/x-evil`). `ValidateRule` (`rules_store.go`) guards both forms before they reach generation — literal must start `/` and contain only RFC 3986 path characters (`literalPathChars`; rejects a regex written without the `~` marker), regex must compile (Go `regexp` is RE2, exact compile-compat) and anchor at the path root — failing the whole rule-update on any invalid path. A path rule's `Methods` add a `:method` `RouteMatch.headers` matcher (`exact` for one method, `safe_regex` alternation for many — `envoy_http.go::methodHeaderMatch`) narrowing that route to the listed HTTP verbs; non-matching verbs fall through to later routes / `path_default`. HTTP-family only — `methods`/`path_rules` on opaque protos (tcp/ssh/udp) are ignored at generation, surfaced as a `NormalizeAndDedup` warning (`pathRuleEnforcementWarning`). Every HCM merges in `httpConnectionManagerHardening()` (normalize_path / merge_slashes / path_with_escaped_slashes_action / headers_with_underscores_action / max_concurrent_streams) — load-bearing for path-rule enforcement against URL-encoded traversal. No timeouts or per-connection buffer caps: LLM workloads run for minutes with multi-MB bodies, Envoy defaults are correct. Centralized `firewallBlockedBody` constant for `direct_response: 403` bodies (non-fingerprinting). The `otel_collector_als` cluster dials the CP-only `otlp/infra` receiver on `OtelInfraPort` with an upstream TLS transport_socket (leaf+intermediate bind-mounted at `/etc/envoy/otel-tls/`, CLI root CA at `ca.pem` for server-cert verification). When `als.MTLS=false` the OTel sink AND cluster are both omitted at the sender (gated in `buildHTTPAccessLog` / `buildTCPAccessLog` / `buildClusters`) — Envoy keeps only the stdout JSON sink for triage. Infra services must never cross into the untrusted `otel-collector:4317` lane reserved for agent containers. `normalizeDomain` lives here — used by certs, coredns_config, rules_store, and by the IdentityAllocator's dst normalization |
| Per-svc OTel mTLS material | Provided by `*otelcerts.Service` — see `internal/controlplane/otelcerts/AGENTS.md`. `Stack` holds an `OtelCertProvisioner` reference and dispatches one `EnsureClient` call per sibling (envoy, coredns) inside `ensureConfigs` so `Reload` rotates with the config refresh. No-op when the provisioner is nil — stdout-only degraded mode: Envoy emits no OTel access logs (sink + cluster dropped); CoreDNS otel plugin installs noopEmitter. Atomic write, pair-check, and 0o755/0o644 perms are owned by the provisioner. Note: netlogger's mTLS material is NOT provisioned by `firewall.Stack` — `cmd/clawkercp/main.go` mints its per-handshake leaf directly via `otelcerts.Service.LoadTLSConfig("netlogger")` and hands the resulting `*tls.Config` to `controlplane.NewOtelLoggerProvider`. |
| `coredns_config.go` | Corefile generation; wildcard rules → subtree-forward zones; exact-only rules → forward apex + NXDOMAIN-subdomain template (`fallthrough`); deny rules → dedicated NXDOMAIN zones (win via longest-zone match); `dnsbpf` plugin directive; catch-all NXDOMAIN |
| `castore.go` | `CAStore` is the shared MITM CA owner for `Handler`, `Stack`, and `SDSServer`. `Load` holds a read lock and never generates a CA. `Ensure` and `Rotate` hold a write lock. `NewCAStore` rejects a nil directory function with `ErrNilCACertDirFn` and takes the domain-cert reader identity (`sdscerts.FileOwner`, the Envoy process UID/GID); `Reader` returns it. `RegenerateDomainCerts` and `RotateCA` chown each domain pair to that reader (mode `0600`) before publishing it and make the certs directory group-traversable (`0750`) — the Envoy sibling is not root, and Linux bind mounts preserve host ownership. The CA pair stays with CP root. |
| `certs.go` | `LoadCA` reads an existing CA pair and returns `ErrNoCA` if either file is absent. `EnsureCA` creates absent pairs and repairs pairs that do not match. CA and domain PEM writes use temporary files and rename; the key is written before the certificate. `GenerateDomainCert` and `GenerateSNICert` sign static and per-SNI leaves. |
| `sds_server.go` | `SDSServer` + `SDSServerDeps` + `NewSDSServer` — the Envoy secret-discovery service (delta xDS) behind the on-demand downstream certificate selector on wildcard https/wss MITM chains. Envoy pauses the handshake at ClientHello, requests a secret named by the SNI, and resumes with the returned per-SNI leaf minted against the firewall CA — so a wildcard rule covers hosts at EVERY label depth (an RFC 6125 wildcard matches one label; the static `[apex, *.apex]` pair cannot cover `a.b.zone` — issue #500). Fail closed: a name not admitted by a stored wildcard https/wss allow rule (longest matching zone wins) is answered with a resource removal, which fails the handshake. The server reads the shared `CAStore` with `Load`. Its mint cache is keyed by SNI and invalidated by CA serial. Listener wiring lives in `internal/controlplane/cmd.go::startSDSServer` (mTLS: CP server leaf ↔ infra-intermediate-anchored client certs, narrowed by a `VerifyConnection` SAN pin to the DEDICATED `consts.EnvoySDSClientName` leaf provisioned by `controlplane/sdscerts` — the telemetry lane's `envoy-otel-client` leaf is refused; clawker-net only, port `ControlPlaneSettings.SDSPort`, degrade `event=sds_unavailable`). Envoy dials with the `/etc/envoy/sds-tls` material, gated on the stack's `sdsCertsReady` flag (independent of the telemetry lane's `infraCertsReady`). |
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
| `FirewallEnable(container_id)` | per-container | Idempotent enroll. `resolveForEnable` → Docker lookup → fresh `cgroup_id` via `EBPFCgroupPath`. BPF `container_config` is built CP-side from `Stack.NetworkInfo` (Envoy/CoreDNS/gateway/CIDR) + `cfg.EnvoyEgressPort()` + `resolveHostProxy` (resolves `host.docker.internal` when the project has host proxy enabled). Writes `container_map` + attaches links via `ebpf.Manager.Install` + clears any bypass flag. Drift guard logs stored-vs-fresh cgroup_id delta. Returns `FailedPrecondition` if Docker says the container is gone. Note: the bypass dead-man timer does NOT re-run `Install` — it calls the cheap `ebpf.Manager.Enable` path (clears bypass flag only). Full re-enroll happens only on the explicit `FirewallEnable` RPC. **Side effect**: after the `container_map` write succeeds, publishes `ebpf.EBPFContainerEnrolled{CgroupID, ContainerID, OccurredAt}` on the typed `EnrolledTopic` (nil-tolerant — test wiring without a topic skips the publish). netlogger subscribes to this event to hydrate its label cache — but only for RPC-path `FirewallInit`/`FirewallEnable`: the settings-driven startup gate runs its `FirewallInit` before netlogger is constructed, so those enroll events are dropped and the LabelCache stays cold until the next RPC-path sweep (see the step-9 caveat in `internal/controlplane/AGENTS.md`; telemetry enrichment only, enforcement unaffected). |
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
    CA            *CAStore           // shared with Stack and SDSServer; required by FirewallRotateCA
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

### `CAStore`

`buildEnforcement` constructs one store with `consts.FirewallCertSubdir` and
passes it to the handler, stack, and SDS server. SDS calls `Load`; it cannot
create a second CA during rotation. `FirewallRotateCA` calls `Rotate` before
queue submission, under the same store lock used by `Stack.ensureConfigs`
for `Ensure`. Returned certificates and keys are independent of later rotations.

```go
func NewCAStore(certDirFn func() (string, error)) (*CAStore, error)
func (s *CAStore) Load() (*x509.Certificate, *ecdsa.PrivateKey, error)
func (s *CAStore) Ensure() (*x509.Certificate, *ecdsa.PrivateKey, error)
func (s *CAStore) Rotate(rules []config.EgressRule) error
```

### `Stack`

```go
type Stack struct { /* docker.Client, config.Config, logger, EgressRulesStore, CAStore */ }

func NewStack(dc *docker.Client, cfg config.Config, log *logger.Logger, store EgressRulesStore, otelCerts OtelCertProvisioner, sdsCerts SDSCertProvisioner, idFor IdentityResolver, ca *CAStore) (*Stack, error)  // ErrNilCAStore on nil ca; nil otelCerts/sdsCerts = per-lane degraded mode; nil idFor = fail-closed stub (no dnsbpf directives; event=identity_resolver_unset)
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
- **Envoy-gen tests (`envoy_config_test.go`)** — ONE comprehensive golden, NOT one-per-feature. New coverage (any new proto/dst-type/path/ws/DFP/QUIC/cert/port-range permutation or interaction) is added by extending the `comprehensiveRules` const + re-blessing `comprehensive`/`comprehensive_mtls`, NOT by adding a new `*.envoy.golden` per feature. The only standalone cases allowed are generation-wide-fact-OFF shapes a mega-config can't express (`http_exact_only`/`https_exact_only` = DFP absent, `ssh` = no egress listener/deny floor) and fail-closed (`wantErrContains`) cases. Full rules: Envoy egress generation → Testing, below.
- **Golden files**: `testdata/corefile_basic.golden` and `testdata/corefile_wildcard_deny.golden` are hand-edited to update (no `GOLDEN_UPDATE=1` hook). `testdata/envoy/*.envoy.golden` re-bless via `GOLDEN_UPDATE=1 go test ./internal/controlplane/firewall/ -run TestGenerateEnvoyConfig`.
- **E2E tests**: `test/e2e/firewall_test.go` (composite flows through the CLI — blocked domain, allowed domain, add/remove rules, status, path rules, bypass end-to-end including natural-expiry + gone-container error paths) and `test/e2e/controlplane_cli_test.go` (break-glass `controlplane up/status/down` verbs). E2E means through `harness.Run(...)` — no direct `Stack`/`Handler` construction belongs under `test/e2e/`.

## Gotchas

- `APIClient.ImagePull` / `ImageBuild` only return a top-level error on initial HTTP failure; auth/manifest/layer errors stream as JSON frames with an `error` field. Always drain via `drainPullStream`/`drainBuildStream` and surface `msg.Error`.
- `cerrdefs.IsNotFound` does NOT match whail's `*DockerError{Op: "network_find"}` wrapping. Substring-match on `"not found"` false-positives (`"image not found"`, `"endpoint not found"`). In Status, log network-discovery errors at Warn and leave topology fields empty — per-container `isRunning` distinguishes "stack down" from "Docker unreachable".
- `HandlerDeps.Store` being nil turns the reconcile/route paths into no-ops and makes `FirewallListRules` / `FirewallRotateCA` / `FirewallAddRules` / `FirewallRemoveRule` return a `codes.Internal` wiring-fault status naming the missing dep instead of nil-dereffing inside a queued closure (which comes back as a recovered-panic result that names nothing). Intentional for unit tests; `cmd/clawkercp` always wires a real store.
- A reconcile that fails AFTER a rule mutation committed returns an error saying the change WAS saved and takes effect on the next `firewall up` / `firewall reload`, wrapping the reconcile error. Reporting a bare failure for a durable write is the dangerous direction: a user who removed a deny rule would believe the block still stands.
- `HandlerDeps.Stack` being nil silently turns stack-up/down RPCs into no-ops. Intentional for unit tests, but a production wiring bug would hide here — `cmd/clawkercp/main.go` must always wire a real `*Stack`.

## Envoy egress generation

<critical>
### Envoy is NOT an HTTP proxy. It is NOT a TLS proxy. Stop building it that way.

From Envoy's own architecture docs (https://www.envoyproxy.io/docs/envoy/latest/intro/what_is_envoy and intro/arch_overview/intro/threading_model + intro/arch_overview/listeners/network_filters):

> "At its core, Envoy is an **L3/L4 network proxy**. A pluggable filter chain mechanism allows filters to be written to perform different TCP/UDP proxy tasks... **HTTP is such a critical component of modern application architectures that Envoy supports an additional HTTP L7 filter layer. The HTTP connection manager is itself a network filter.**"

Internalize that last sentence. **HTTP (the HCM) is ONE L3/L4 network filter among many.** TLS is a *transport socket* decoration. SNI is a *match* condition. These are leaves, not the trunk. The trunk is: a **listener** (TCP or UDP) → a **filter chain** selected by a **match** → **network filters** → an **upstream cluster**. Everything clawker does is an arrangement of those generic primitives.

When you catch yourself reaching for "the TLS chain" or "the HTTP path" as the organizing idea, STOP — you have the bias this entire document exists to kill. The organizing idea is the **L4 transport** (TCP/UDP); crypto (TLS/QUIC/mTLS/SNI) and L7 (HTTP/WS/...) are optional decorations layered on top.

clawker's egress firewall must support **every permutation of the network stack**, not just https:
- **L4:** raw TCP, raw UDP.
- **Crypto/L5-6:** plaintext, TLS-terminate (MITM), QUIC-terminate (MITM), SNI gating, mTLS.
- **L7 / app protocols:** HTTP, HTTPS, WS, WSS, HTTP/3, SSH, FTP, and any future protocol — including opaque (no L7 inspection) flows.

If a change only handles HTTP and HTTPS, it is incomplete by definition.
</critical>

<critical>
### Verify against source. Never guess Envoy behavior.

LLM training data on Envoy is weak and frequently wrong (this session alone: `require_sni` is unimplemented; `server_names` wildcards are asterisk-form not leading-dot; `auto_host_sni` doesn't work for dynamic-forward-proxy hosts — all contradicted "common knowledge"). The firewall is security-critical; a wrong config silently breaks egress enforcement.

**Every claim about Envoy behavior MUST cite a fetched source before you write code:**
1. Official example: `gh api "repos/envoyproxy/examples/contents/<path>" --jq '.content' | base64 -d`
2. Proto / docs: `WebFetch` the api-v3 proto or arch_overview page (use `raw.githubusercontent.com/envoyproxy/envoy/main/api/...` for exact proto field names + `[#not-implemented-hide:]` markers).
3. Reference impls (Contour, Istio) for how production control planes assemble the same blocks.
4. `envoyproxy/envoy` issues for known limits.

Do not name a field, default, or behavior from memory. If it isn't in fetched context, it isn't established.
</critical>

### The proto "token" model (the core abstraction)

An egress rule is `{host, port, paths, proto}`. **`proto` is a TOKEN: an abstract, polymorphic, orthogonal statement of user intent — NOT a config shape.** It names *what the user wants to reach*, and the generator derives the *full network-stack permutation* of Envoy config for that `host:port:proto(:paths)`.

| token | user intent | network stack the generator must derive |
|-------|-------------|------------------------------------------|
| `tcp` | raw TCP to host:port | TCP listener → opaque `tcp_proxy` → pinned cluster (no crypto, no L7) |
| `ssh` | SSH over TCP | TCP listener → opaque `tcp_proxy` → pinned cluster (L7=ssh is opaque to us; gate = pin) |
| `ftp` | FTP over TCP | TCP listener → opaque `tcp_proxy` (+ control/data port handling) → pinned cluster |
| `http` | cleartext HTTP | TCP listener (raw_buffer) → HCM (L7) → plaintext upstream |
| `https` | HTTP over TLS | TCP listener (TLS-terminate/MITM, SNI gate) → HCM → **reencrypt** upstream **+** sibling UDP/QUIC (h3) listener → HCM → reencrypt upstream |
| `ws` | WebSocket over HTTP | `http` + per-route `upgrade_configs` |
| `wss` | WebSocket over TLS | `https` + per-route `upgrade_configs` (+ upstream h1.1 pin) |
| `udp` | raw UDP datagrams | UDP listener → `udp_proxy` → pinned cluster (no crypto, no L7) |

This table is illustrative, not exhaustive — new tokens (gRPC, QUIC-raw, DNS, SMTP, ...) slot in by composing the same blocks. **A token is never special-cased with a bespoke code path.** It selects an ordered list of building blocks.

### Architecture: orthogonal building blocks, one forward pass

The generator (`envoy_*.go` in the firewall package) is a **protocol-agnostic orchestrator + a deriver + self-contained layer blocks**:

- **Orchestrator** (`GenerateEnvoyConfig`): dumb generic loop. For each permutation it chains an ordered list of `layer` methods through one mutable `genCtx`, then commits into the `EnvoyConfig` accumulator (upsert by key, fail-closed). It never names a protocol.
- **Deriver** (`derive(rules, ports)`): the ONLY proto-aware step. Calls `deriveGenFacts` once for generation-wide facts, then for each rule: skips non-opaque deny rules (enforced by absence), handles ws/wss absorb/promote to http/https, then delegates to `layersFor(r, gen)` — the per-rule mapper that turns the token (+ wildcard-ness, port, paths) into `[][]layer`. One token may yield multiple permutations (e.g. `https` → a TCP chain AND a QUIC chain).
- **Layer blocks** — three ORTHOGONAL kinds, each a `func(*genCtx) error` that reads/writes only `genCtx`, never the token:
  1. **Transport block** — binds the listener and decides L4 + downstream crypto. *This is the trunk.* Organized by L4: TCP transports (raw_buffer cleartext; TLS-terminate MITM) live with TCP; UDP transports (QUIC-terminate MITM; raw `udp_proxy`) live with UDP. A transport block is self-deciding; the deriver picks exactly one per permutation. **QUIC is a UDP transport — it never belongs in a "tls" file.**
  2. **Upstream block** — the cluster: "where do these bytes go, how is the host resolved." **Generic and L4-agnostic.** A cluster is a cluster (LOGICAL_DNS pin, dynamic_forward_proxy, ORIGINAL_DST, STATIC) with optional decorations (reencrypt `UpstreamTlsContext`, `HttpProtocolOptions`). ssh/tcp/udp pinned clusters are peers of the http/https ones — do NOT frame the upstream layer as "HTTP upstreams."
  3. **App block** — L7 inspection, i.e. the HCM. ONLY for HTTP-family tokens (http/https/ws/wss/h3). Opaque tokens (tcp/ssh/ftp/raw-udp) have **no app block**. The same HCM app block is reused verbatim across http/https/ws/wss/h3 — it inspects cleartext regardless of whether bytes arrived plaintext, TLS-decrypted, or QUIC-decrypted.

**Decoupling is mandatory.** Transport / upstream / app are independent. The app block must not know how bytes were decrypted; the upstream block must not know the L7; the transport block must not know the cluster. A block that reaches across this boundary (e.g. an L7 HTTP filter living in the upstream/cluster file, or QUIC config in a TLS file) is a bug to fix, not a pattern to extend.

**Single forward pass.** `genCtx` is threaded mutably transport → upstream → app. A block decides its facts BEFORE the next runs; nothing patches a committed block retroactively. Generation-wide facts that no single permutation can decide in isolation are computed once up front in the deriver (`genFacts`).

### Redundancy is REQUIRED — Envoy is its own island

Defense in depth here is NOT "layers that together cover the threat." It is **mandatory redundancy: every hop independently re-checks everything, as if it were the only defense.** No component of the firewall stack is load-bearing for the group. A regression, bug, missing feature, or outage in any one layer must change the firewall's security posture by ZERO.

**Envoy generates as if eBPF and CoreDNS do not exist.** They can fail, regress, be misconfigured, or be bypassed (a hardcoded IP skips CoreDNS; an eBPF gap skips the redirect). So Envoy's config must, entirely on its own:
- gate the host (SNI `server_names` / Host vhost + deny default) — even though CoreDNS also NXDOMAINs disallowed names;
- resolve the upstream IP itself, never the client's (see confused-deputy gotcha) — even though eBPF also redirects;
- pin or deny every flow — even though eBPF also filters at the cgroup.

Never reason "eBPF will catch it" / "CoreDNS NXDOMAINs it, so the vhost can fail open" / "eBPF doesn't redirect UDP anyway, so the UDP listener doesn't matter." Every one of those makes a sibling layer load-bearing — forbidden.

**This extends to supported CAPABILITIES, not just runtime liveness.** It does not matter whether eBPF (or anything else) currently supports UDP, a particular app protocol, IPv6, or any future stack feature. Envoy emits the COMPLETE, self-secure config for every permutation the egress rules express, regardless of what the rest of the stack can carry today. Envoy assumes nothing about, and depends on nothing from, eBPF or CoreDNS — it is in its own void. If a permutation is currently unreachable because another layer hasn't caught up, that is the other layer's gap to close; Envoy's atom is still correct and still self-secure. (This is also why "is eBPF UDP-ready?" is never a precondition for emitting a UDP/QUIC listener.)

**Redundancy applies INSIDE Envoy too.** Each listener, filter chain, and cluster runs its own checks; a later stage never trusts that an earlier one already validated. In the generated config:
- `udp_proxy`/`tcp_proxy` forwards ONLY to its pinned cluster — opaque flows are gated by the pin alone.
- a TLS/QUIC listener gates by per-SNI `server_names` + a deny `default_filter_chain`, AND each per-SNI chain carries only its own vhost so Host is re-gated at L7 (two independent checks, not one).
- a port-range pins per in-range port; never `ORIGINAL_DST`.
- the upstream cluster re-validates identity (`auto_sni` / `auto_san_validation` against the system CA) even though SNI already selected the chain.

See the `defense-in-depth-no-vacuum-excuse` and `transport-first-not-tls-centric` memories.

### Deny: an EXPLICIT chain, NOT a fall-through (two different mechanisms)

There are TWO deny mechanisms. They are not interchangeable, and the floor is not an escape hatch for skipping the first.

**1. Explicit deny chain — a real `action: deny` on a SUPPORTED proto.** The operator said deny, so the generator builds a real chain, the same way it builds an allow chain — just terminating in the blackhole `deny_cluster` instead of a pinned upstream:
- opaque tcp/ssh/udp → its OWN dedicated listener (`tcp_<host>_<port>` / `udp_<host>_<port>`) → `tcp_proxy`/`udp_proxy` → `deny_cluster` (STATIC, zero endpoints → reset) + access-log `action: denied`. A CIDR opaque deny rides the shared egress listener as a `prefix_ranges` + `destination_port` chain → `deny_cluster`.
- A deny listener is a FIRST-CLASS listener: it gets an eBPF route to it, exactly like an allow listener. **A deny listener with an eBPF route is NOT an orphaned-listener violation — it is the intended shape.** The orphaned-listener invariant ("every `TCPMapping`/`UDPMapping` must have a matching `RoutesFromRules` route") applies to deny mappings too: that route is what makes the denied port get ACTIVELY reset and logged (`action: denied`) instead of silently dropped. Do not "fix" the route away.
- **Deny ALWAYS wins on overlap.** `resolveOpaquePortConflicts` (rules_store.go) folds each `(dst, opaque-proto)` into allow + deny port spans and carves every denied port out of the allow spans (`subtractSpans`) — allow `45-50` + deny `47` → allow `{45-46, 48-50}` + explicit deny `47`; deny `45-50` + allow `47` → deny `45-50`, allow swallowed; both directions, range∩range too.
- **A carve REQUIRES a range.** An all-single allow/deny clash on the SAME port (`tcp 4242 allow` + `tcp 4242 deny`) has no range to carve — it is a contradictory config, not a deny-wins carve. The resolver leaves both rules and `checkOpaquePortActionConflicts` (a generation pre-check) FAILS LOUD ("no range to carve"). `NormalizeAndDedup`'s dedup key folds in the action so both survive to be caught (RuleKey alone, port-only, would silently drop one).

**2. Fall-through deny FLOOR — the safety net for UNRECOGNIZED tokens.** The shared egress listener's `default_filter_chain` (`installEgressDenyFloor` → `deny_cluster`, `stat_prefix: egress_deny`) and the SNI/Host/path deny defaults exist to catch flows that match NO built chain: a misspelled or unsupported proto token (`tfp`, `tpc`, …) that `layersFor` returns nil for (the deriver soft-skips it with a warning — never a hard failure, so one bad token can't deny everything), an unknown/absent SNI, a Host not on the whitelist, a denied path. We deny those because there is no stack to build — no other choice.

**The floor is a backstop for the unknown case, never the enforcement path for intent.** Reasoning "the floor will catch it" / "deny by absence" to avoid building an explicit chain for a real `action: deny` rule is the lazy escape hatch that left denied ports silently allowed under overlapping allow ranges. Build the explicit chain. Equally: never delete or weaken the floor — it is the unrecognized-token safety net. See the `deny-floor-is-safety-net-only` memory.

### Verified Envoy facts (this codebase relies on these — re-verify before changing)

- **`require_sni` is `[#not-implemented-hide:]`** in `tls.proto`. It does NOT gate SNI. A single multi-cert `DownstreamTlsContext` serves the **first cert** on SNI mismatch/absence (`full_scan_certs_on_sni_mismatch:false` default) and proceeds to L7 — i.e. no server-side gate. **The only server-side SNI gate is per-SNI `filter_chain_match.server_names` + `tls_inspector`**, with unmatched SNI falling to a deny `default_filter_chain` (`tcp_proxy` → zero-endpoint `deny_cluster` → reset).
- **`server_names` wildcards are asterisk-form `*.apex`** (NOT leading-dot `.apex`, which Envoy treats as an exact literal). Envoy stores `*.apex` as `.apex` and matches by stripping one label at a time, so `*.apex` covers the whole subtree (incl. multi-label). The bare apex needs its own entry. A wildcard rule and an exact rule for the same apex must not duplicate a `server_names` entry (Envoy rejects the whole config).
- **Per-SNI chain + own-vhost replaces the legacy `sni-lock` filter.** Each https/wss/h3 SNI gets its OWN filter chain carrying ONLY its own vhost + `deny_all`. SNI structurally pins the Host (a mismatched Host hits `deny_all` 403, never an upstream), so there is NO `set_filter_state`/sni-lock/`dynamic_host` L7 dance. If you find that legacy coupling, it is gone — do not reintroduce it.
- **Upstream identity:** `HttpProtocolOptions.upstream_http_protocol_options{auto_sni, auto_san_validation}` (SNI from `:authority`, SAN validated against it). `auto_host_sni` does NOT work for a `dynamic_forward_proxy` dynamic host (no static hostname) — use `auto_sni`. Reencrypt validates against the **system CA** (`/etc/ssl/certs/ca-certificates.crt`), never the MITM CA.
- **Dynamic forward proxy is Host-keyed** (the DFP LB derives host:port from `:authority`), system-resolver (CoreDNS) — no hardcoded resolver. Plaintext and reencrypt variants use distinct dns_caches so the secure-upstream default port (443 vs 80) is honored. Disable DFP per-vhost (`typed_per_filter_config`) on every non-Host-following vhost so it never pre-resolves a pinned/denied request.
- **h3 = QUIC over UDP**: a UDP listener (`udp_listener_config.quic_options`) + `QuicDownstreamTransport` (same per-SNI MITM certs) + HCM `codec_type: HTTP3`. The sibling TCP chain advertises it via an `alt-svc` response header on the **origin authority port** (what the client dials + eBPF redirects), not Envoy's listener port.
- **QUIC siblings are SNI-selectable ONLY — IP/CIDR https/wss is TCP-only.** A QUIC chain selects either by SNI (`server_names`, in the QUIC ClientHello → survives the redirect) or by recovered original dst (`prefix_ranges`, no SNI). **UDP/QUIC has no original-dst recovery** (grounded vs Envoy: `use_original_dst` is TCP-only — `QuicListenerFilterManagerImpl` forbids it — and there is no UDP equivalent of the `original_dst` listener filter), so a `prefix_ranges`-matched QUIC chain never matches under redirect. Hence the deriver emits a QUIC sibling for **FQDN** https/wss only; an **IP-literal or CIDR** https/wss rule is TCP-only and advertises no `alt-svc: h3` (`tlsSNIChainLayer` sets `advertiseH3 = !needOriginalDst`; `quicSNIChainLayer` fails closed on an IP/CIDR dst). This is the single sanctioned per-dst-type QUIC carve-out — do NOT re-add an IP/CIDR QUIC chain to "complete the atom"; it is unreachable, not missing.
- **WS over h2 (RFC 8441 Extended CONNECT)** needs `allow_connect` on the relevant `http2_protocol_options`; WS over h1.1 uses RFC 6455 Upgrade. Verify the upstream codec before assuming.
- **Client session cache is per-context, NOT per-SNI** (`ClientContextImpl::session_keys_`): a TLS 1.3 ticket minted for host A is offered on the next connection from the same `UpstreamTlsContext` toward host B; a resumed session carries A's certificate and fails `auto_san_validation` against B's name — the "verify SAN list" 503 of issue #518. Any multi-host reencrypt cluster (DFP, CIDR origdst) therefore sets `max_session_keys: 0` (the `multiHost` arm of `upstreamReencryptSocket`); single-host exact clusters keep resumption.
- **On-demand downstream cert selection (Envoy ≥1.38):** `CommonTlsContext.custom_tls_certificate_selector` (field 16) + `envoy.tls.certificate_selectors.on_demand_secret` + the `envoy.tls.certificate_mappers.sni` mapper. The handshake pauses at ClientHello, the mapper derives the secret name from the SNI, the secret is fetched over SDS (the reference integration uses `api_config_source` with `DELTA_GRPC`; a `resources[].resource` packing a `tls.v3.Secret`, inline bytes accepted), and the handshake resumes. A resource REMOVAL fails the paused handshake — that is the fail-closed deny lever. Pair it with `disable_stateless_session_resumption` + `disable_stateful_session_resumption` (a resumed session skips selection; the integration test pins both true). **QUIC does not support the selector** — the factory returns InvalidArgument `for_quic` (v1.39.1 `config_test.cc::BasicLoadTestQuic`), so QUIC listeners keep static certs. clawker uses this on wildcard https/wss MITM chains (`downstreamOnDemandMITMSocket` → the CP `SDSServer`) so a wildcard rule covers every label depth; exact/IP/CIDR chains keep static file certs. Source: v1.39.1 `api/envoy/extensions/transport_sockets/tls/cert_selectors/on_demand_secret/v3/config.proto`, `cert_mappers/sni/v3/config.proto`, `test/extensions/transport_sockets/tls/cert_selectors/on_demand/integration_test.cc`.
- **Method gating = a `:method` `RouteMatch.headers` matcher** (a path rule's `methods`). `HeaderMatcher.string_match` (field 13, `StringMatcher`) is the non-deprecated matcher in v1.37 — legacy `exact_match`/`safe_regex_match` are `deprecated_at_minor_version 3.0`, do NOT use them. One method → `string_match: {exact: GET}`; multiple → `string_match: {safe_regex: {regex: "GET|HEAD"}}` (RE2 **full-string** match, so the bare alternation needs no `^…$` anchors; `RegexMatcher.google_re2` is deprecated — OMIT it, RE2 is the default engine). It's a MATCH condition, not a verdict: the route's existing allow/deny is the polarity, non-matching methods fall through to later routes / `path_default` (`EffectivePathDefault` unchanged). HTTP-family only — `methods`/`path_rules` on opaque protos are ignored at generation + warned (`pathRuleEnforcementWarning`). Methods are sanitized to a token charset (`^[A-Za-z][A-Za-z-]*$`) at `ValidateRule` so they can't inject regex metacharacters into the alternation; `NormalizeRule` uppercases/dedups/sorts them.

### Gotchas — past regressions (do NOT reintroduce)

Real failures that have bitten this firewall. Each is a security AND correctness issue; the mitigations are load-bearing. Both are about **upstream IP resolution integrity** — they apply to every transport (TCP, UDP, QUIC), not just HTTP.

#### 1. Same-SAN cert, different IPs → connection coalescing makes "first IP win"

When two+ allowed hosts present upstream certs with overlapping SANs (e.g. `api.anthropic.com` and `statsig.anthropic.com` both covered by a `*.anthropic.com` SAN) but resolve to DIFFERENT IPs, a multiplexed (h2/h3) upstream can **coalesce connections**: Envoy reuses the existing connection to the first-resolved host for a request whose `:authority` is a *different* same-SAN host, because the live connection's cert already covers that name (RFC 7540 §9.1.1 connection reuse). The first IP's pool "wins" — every other same-SAN host is silently dialed at the WRONG IP, so connections to those domains break, and traffic meant for host B egresses to host A's endpoint (a cross-host leak).

Mitigations, both load-bearing:
- **Per-(host,port) pool isolation.** Exact rules each get their OWN cluster (one connection pool per allowed host) so coalescing has nothing to reuse across hosts. Never collapse multiple allowed hosts into a single shared pool that can coalesce.
- **Disable cross-host coalescing on any shared cluster** (the dynamic-forward-proxy cluster, where multiple hosts legitimately share one cluster). Verify the exact upstream knob against the proto before relying on it (historically a "no coalesced connections" option) — do NOT assume the default is safe.

A single-host golden will not catch this — exercise any change with ≥2 allowed hosts that share a real SAN cert but resolve to different IPs.

#### 2. Confused deputy — NEVER honor the client's desired destination IP

A compromised/injected agent picks BOTH the host AND, if allowed, the destination IP (via `/etc/hosts`, `curl --resolve`, a hardcoded IP, a crafted UDP dst). The host is gated (SNI/Host vhost + CoreDNS NXDOMAIN); the IP must be gated too — by **ignoring it entirely**.

**Any flow that passes host validation MUST have its upstream IP resolved by Envoy itself — never taken from the client's chosen destination.** Concretely: `LOGICAL_DNS` pinned to the rule's host (exact), or `dynamic_forward_proxy` resolving the validated `:authority` via CoreDNS (wildcard). **`ORIGINAL_DST` is FORBIDDEN for any host-validated flow** — it forwards to whatever IP the client's socket targeted, which lets a compromised agent resolve an allowed hostname to an attacker IP and have the firewall faithfully forward allowed-host traffic there. The client may choose the *host* (within the allow list); Envoy alone chooses the *IP*. This is why the port-range design rejected `ORIGINAL_DST` in favor of per-port pinned clusters, and it holds identically for TCP, UDP, and QUIC.

**Carve-out — `ORIGINAL_DST` is CORRECT for a range-validated (CIDR) flow.** "Host-validated" means the grant is a single host/FQDN, so honoring the arriving dst is delegating that single-host decision to the datapath (the deputy). A CIDR rule's grant is the whole range, enforced on the chain itself by `filter_chain_match.prefix_ranges` + `use_original_dst` — Envoy validates the recovered original dst against the range BEFORE the cluster sees it, so forwarding to the in-range dst is *honoring* the grant, not trusting the client. Hence: bare IP → `STATIC` pin (the address IS the resolution); FQDN → `LOGICAL_DNS`/DFP; **CIDR → `ORIGINAL_DST`/`CLUSTER_PROVIDED` scoped by the chain's `prefix_ranges`** (plaintext for http/ws, reencrypt for https/wss). UDP-CIDR has no filter chains to range-gate on, so it fails closed — the carve-out needs the chain-level `prefix_ranges` to hold.

#### 3. CIDR-TLS range cert — invalid-by-design, and that is fine

A `https`/`wss` rule to a CIDR mints ONE MITM leaf whose iPAddress SAN is the network address — it cannot validate against any single in-range host. That is intentional and not a bug to "fix" by enumerating SANs or capping the range: **agent-side verification is not clawker's enforcement boundary** (egress gating + MITM inspection are — see the `agent-cert-trust-not-load-bearing` memory). The leaf still encrypts the downstream hop and lets Envoy MITM-inspect; a client connecting to a raw in-range IP sets its own no-verify, exactly as it must for any self-signed endpoint. The *upstream* hop is still secure-by-default: do NOT gate `insecure_skip_tls_verify` — when unset, Envoy `VERIFY_TRUST_CHAIN`s and refuses an untrusted in-range upstream (fail-closed); a non-fatal generation warning is the only UX nudge. `certBasename` folds the CIDR `/`→`_` so the cert is one flat file and the `DownstreamTlsContext` ref agrees.

### HCM hardening contract (when an HCM exists)

Every clawker HCM MUST carry the full `httpConnectionManagerHardening()` set (applied via `maps.Copy` so no site forgets one):
- `normalize_path: true` + `merge_slashes: true` + `path_with_escaped_slashes_action: UNESCAPE_AND_REDIRECT` — defeats URL-encoded traversal (`/allowed/%2e%2e/denied`) that would otherwise bypass path rules.
- `common_http_protocol_options.headers_with_underscores_action: REJECT_REQUEST` — RFC 9110 §5.4.5 header aliasing.
- `http2_protocol_options.max_concurrent_streams: 100` — h2 amplification cap.

Timeouts and `per_connection_buffer_limit_bytes` are deliberately UNSET: LLM API calls stream for minutes with multi-MB bodies; short caps rupture mid-stream. Envoy defaults are correct for this workload.

### Deny body — non-fingerprinting

Every `direct_response: 403` (path-rule deny, `deny_all` vhost) uses the generic `firewallBlockedBody` ("Forbidden\n") — never names clawker. An injected-prompt adversary must not distinguish a clawker block from a generic upstream Forbidden by body. The verdict travels on the `action` access-log field, not the body.

### Access-log schema

Records use OTel semantic conventions (`network.*`, `server.*`, `client.*`, `tls.*`) plus the clawker `action` verdict. Full field reference + sources live in code comments (`buildTCPAccessLog`/`buildHTTPAccessLog`). Non-obvious rules:
- `action` (`allowed`/`denied`) is the canonical verdict, stamped at generation (per-route `%METADATA(ROUTE:clawker:action)%` for HCMs; hardcoded for opaque `tcp_proxy`/`udp_proxy`). NEVER inferred from `response_code`/`response_flags` — a legitimate upstream 403 is still `action: allowed`.
- `network.transport` must reflect the ACTUAL L4 (`tcp`/`udp`/`quic`) — do not hardcode `tcp` once UDP/QUIC transports exist.
- Config vocabulary (`allow`/`deny`, present tense) and event vocabulary (`allowed`/`denied`, past tense) are independent by design — never propagate "consistency" between them.

### Testing — STRICT, non-negotiable

Three hard requirements. A test touching Envoy generation that violates ANY of them does not belong in this package — delete it.

1. **Real egress-rules YAML, loaded via `NewRulesStoreFromString`.** Every case's input is a real egress-rules YAML document run through the production storage read engine (the firewall package's own in-memory store constructor), read back with `storage.Get[[]config.EgressRule](store, rulesField)`, then `NormalizeAndDedup` → `GenerateEnvoyConfig`. NEVER hand-built `EgressRule`/`config` structs, NEVER mocks, NEVER calling generator internals directly. The test must exercise the exact parse + normalize path production uses.
2. **Compare the resulting Envoy CONFIG against a control.** Every case generates the COMPLETE config and compares it against a committed control — a golden file (preferred: byte-for-byte against `testdata/envoy/<case>.envoy.golden`) or explicit string matches on the rendered YAML. NEVER assert on intermediate Go structures or poke individual fields of the in-memory tree. Assert against the produced config artifact, nothing else.
3. **One comprehensive golden — extend it, do NOT add one-off cases.** All cases live in ONE table-driven test (`cases := []struct{...}` + `t.Run`), and the primary golden is `comprehensive` (+ `comprehensive_mtls` — the identical `comprehensiveRules` const generated with `als.MTLS` on). It packs every co-existable feature into ONE config so cross-rule interactions are pinned in a single diff (same host/CIDR on two ports under two protos, FQDN-QUIC interleaved with IP/CIDR-no-QUIC, the shared egress listener carrying TLS SNI chains + plaintext http catch-all + prefix_ranges chains + `use_original_dst` + deny floor all at once — interactions a per-feature golden never exercises). **DEFAULT for new coverage: add rule line(s) to the `comprehensiveRules` const and re-bless, NOT a new table row / new `*.envoy.golden`.** Before you add ANY new case, you must be able to state which of the two narrow exceptions below it falls under; if neither, it belongs in `comprehensiveRules`.

Why: a whole-config control captures EVERYTHING — every chain, vhost, cluster, filter, listener, access-log field — so any regression surfaces as a diff and is caught inherently. Field-level structural assertions are redundant, brittle, and let drift slip through; they are banned. `validateBootstrap` inside `GenerateEnvoyConfig` is the structural backstop; the control is the behavioral contract. Re-bless goldens with `GOLDEN_UPDATE=1`, then read the diff carefully before committing — a re-bless that quietly drops a chain is a regression, not an update.

**The ONLY two reasons to add a case instead of extending `comprehensiveRules`:**

1. **A generation-wide fact in its OFF/ABSENT state.** A mega-config forces every generation-wide fact ON, so it can NEVER observe a fact being absent. These stay focused — and the list is nearly closed: `http_exact_only` / `https_exact_only` (DFP filter+cluster absent — any wildcard rule turns DFP on globally), `ssh` (opaque-only → no shared egress listener, no deny floor — any http/https rule creates the egress listener). A genuinely new "fact OFF" shape (some future generation-wide flag that can't coexist with the mega's ON state) is the only valid addition here. "I want this feature tested in isolation for a cleaner diff" is NOT a fact-OFF reason — fold it in.
2. **A fail-closed case** (`wantErrContains` set) — it produces no config, so by definition it cannot coexist with a valid mega-config. New validation/error paths get their own row.

Everything else — every new proto token, dst-type, path-rule shape, websocket/DFP/QUIC/cert/port-range permutation, and every *interaction* between them — goes into `comprehensiveRules`. Adding a 30th `foo.envoy.golden` for "the foo feature" is the anti-pattern this rule exists to stop: it re-tests in isolation what the mega-config already covers, loses the cross-rule interaction diff, and rots. If `comprehensiveRules` is missing a permutation, ADD THE RULE, don't add a file. `als.MTLS` is a generation parameter (not a rule), so its on/off split is the two rows over the same `comprehensiveRules` (`comprehensive`/`comprehensive_mtls`); that diff must stay PURELY ADDITIVE (the OTel cluster + `open_telemetry` sink on every listener type, nothing removed) — a non-additive diff there is a regression, not a re-bless.

## See Also

- `../CLAUDE.md` — CP core (Ory auth, startup sequencing, container config, drain callback composition)
- `ebpf/CLAUDE.md` — eBPF subsystem details + pinned map contract
- `.agents/skills/firewall-uat/SKILL.md` — runtime BEHAVIORAL UAT (in-container probe tools, allow/deny/upgrade/SSH-routing discriminators, live config spot-check, C2 harness). Golden+validate prove the config is valid; this proves it enforces. REQUIRED before generator or rule work is declared complete. A reviewer assesses the supplied evidence and does not probe on their own.
