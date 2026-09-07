# PR #519 Copilot Review Triage — Codex Handoff

Branch `fix/wildcard-san-certs`, PR #519 (wildcard SAN certs, issues #500/#518). 9 open Copilot
review comments triaged 2026-09-07. Every finding validated by 2 independent subagents; all
verdicts unanimous. User ruled on each. Findings 1 is DONE (Claude applied + tests pass).
Findings 3–9 are for codex to implement. Finding 2 dismissed.

Related: `mem:initiative_wildcard_san_certs`.

## GitHub thread reconciliation contract

Resolve a thread ONLY when its fix lands. Thread IDs (GraphQL `resolveReviewThread`,
`threadId` input):

| # | Thread ID | State |
|---|-----------|-------|
| 1 | PRRT_kwDOQ1E4ts6f2ZrN | resolve now (fix applied) |
| 2 | PRRT_kwDOQ1E4ts6f2Zr9 | resolve now (false positive, dismissed) |
| 3 | PRRT_kwDOQ1E4ts6f26pc | resolve after CAStore fix lands |
| 4 | PRRT_kwDOQ1E4ts6f4Fap | resolve after bounded-stop fix lands |
| 5 | PRRT_kwDOQ1E4ts6f4Fbb | resolve after drift-label fix lands |
| 6 | PRRT_kwDOQ1E4ts6f4FcG | resolve after commit-flag defer lands |
| 7 | PRRT_kwDOQ1E4ts6f4Fco | resolve after comment rewrite lands |
| 8 | PRRT_kwDOQ1E4ts6f4Fc- | resolve after comment rewrite lands |
| 9 | PRRT_kwDOQ1E4ts6f4FdV | resolve after comment rewrite lands |

## Finding 1 — DONE (applied by Claude, do not redo)

`controlplane/firewall/sds_server.go` unbounded per-SNI mint cache → peer-driven CP OOM
(client-controlled SNI, admitted() checks only zone membership). Applied: LRU bound via
`hashicorp/golang-lru/v2` (promoted to direct dep by `go mod tidy`), `sdsCacheMaxEntries = 1024`
const with why-comment, `s.mu` retained for check-then-mint atomicity. New internal test
`sds_server_internal_test.go::TestSDSServer_MintCacheBoundedAndEvictionRemints` (bounded len +
evicted SNI re-mints, never denied). All `TestSDSServer*` pass. UNCOMMITTED at handoff time
unless a commit exists on the branch.

Deferred side-items surfaced by validators (NOT ruled on, raise with user if touching the area):
EnsureCA filesystem hit per request even on cache hit (memoize by ca.pem mtime+size); per-SNI
Info log lines are an attacker-driven log-volume amplifier (consider throttle/summary);
`version` = caSerial+"-"+sni not unique across re-mints once eviction exists (derive from leaf
serial — requires GenerateSNICert to return it).

## Finding 2 — FALSE POSITIVE, dismissed (no code change)

Claim: deny-only wildcard rules make listeners reference `sds_cluster` without the cluster
emitted. Wrong: `derive()` skips non-opaque deny rules (`envoy_config.go:153`) before any chain
exists — emit predicate (`anyWildcardTLSAllowRule`) and reference set are equal by construction.
Both validators probed the real generator empirically (deny-only → 0 refs, gen succeeds).
`validateBootstrap` is defense-in-depth. Optional cosmetic hardening (key emit off generated
config, not re-derived predicate) NOT ruled on — do not implement without asking.

## Finding 3 — APPLY: CAStore shared owner (user chose over package-level mutex)

Race: `FirewallRotateCA` runs `RotateCA` (RemoveAll + regen) PRE-Submit, outside ActionQueue
(`handler.go:996-1016`, doc comment says so); SDS stream goroutines call `EnsureCA` per mint
(`sds_server.go` CA load); queued `ensureConfigs→EnsureCA` (`stack.go:509`) races too. During
the RemoveAll window a concurrent EnsureCA takes the generate branch → two generations, four
unordered `os.WriteFile` calls (`certs.go:90-95`) → mismatched ca-cert/ca-key pair that parses
but fails every leaf signing (`x509: provided PrivateKey doesn't match parent's PublicKey`).
Durable, silent (per-SNI `sds_secret_denied` only); `RegenerateDomainCerts` re-signing breaks
the static MITM plane too.

Fix spec (validator-drafted, user-approved shape):
- New `controlplane/firewall/castore.go`: `CAStore` struct { mu sync.RWMutex; certDirFn func()
  (string, error) } + `NewCAStore` (nil-check → sentinel error).
  - `Load()` — RLock, load-only, NEVER generates (fail closed on empty dir; needs an exported
    load-only helper / ErrNoCA in certs.go).
  - `Ensure()` — Lock, existing EnsureCA semantics.
  - `Rotate(rules)` — Lock, existing RotateCA semantics.
- Wire ONE instance into `SDSServerDeps` (replace `CertDirFn` with the store), `HandlerDeps`,
  `Stack`; construct in `internal/controlplane/cmd.go` next to rulesStore.
- SDS `secretFor` switches EnsureCA → `ca.Load()` (load-bearing half: SDS can never be a second
  generator).
- `FirewallRotateCA` → `h.ca.Rotate(rules)`; pre-Submit placement may stay (lock supplies
  serialization, keeps ErrCertRegen mapping).
- Hardening (approved as part of fix): temp+rename atomic PEM writes in certs.go; write key
  before cert (cert presence = commit marker); optional loadCA pubkey-match check → regenerate.
- Test: `castore_test.go` under `-race`: N Load loops vs concurrent Rotate; assert loaded pair
  always signs a probe leaf (fails today, passes after).

## Finding 4 — APPLY: bounded GracefulStop (fix 1) + RECORD adjacent gap

`internal/controlplane/cmd.go:753`: `startSDSServer` returns raw `grpcSrv.GracefulStop`,
deferred at ~1613. Envoy's delta SDS stream is eternal + eagerly connected
(`prefetch_secret_names`); GracefulStop blocks until stream handlers return. On startup error
after Envoy is up (bringup-gate failure pre-SetReady — which deliberately does NO teardown;
also post-SetReady startFeeder/:~1629 + NewAgentWatcher/:~1688 error arms) PID 1 wedges forever:
container "running", /healthz 503, on-failure restart inert, eBPF pinned unsupervised.

Fix spec: mirror `GRPCStack.GracefulStop` (`controlplane/server/grpc_stack.go:~585`): race
GracefulStop in a goroutine against `sdsStopTimeout = defaultShutdownWait` (const near :410
with why-comment), on expiry `log.Warn` `event=sds_graceful_stop_timeout` (component
firewall.sds) + `grpcSrv.Stop()` (unblocks the goroutine, nothing leaks). Use timer with
defer timer.Stop(), not bare time.After, if matching house style.

ADJACENT GAP (user: record as separate work item, not part of this fix): the startFeeder and
NewAgentWatcher error arms return WITHOUT running the drain sequence — post-SetReady exits that
skip `ebpfMgr.FlushAll`, unlike every other post-ready arm. Needs its own ruling/fix pass.

## Finding 5 — APPLY: labelSDSPort drift label

`stack.go:937-945` driftLabels() stamps 5 labels, omits SDS port. `sds_port`
(ControlPlaneSettings, schema.go:415, default 7445) is baked into the static sds_cluster in
envoy.yaml (Envoy reads at process start only); ensureContainer adopts on label match — stale
Envoy dials old port after a non-drained CP death (crash/reboot/pre-SetReady exit-1, the
labelOtelInfraPort window) + port change → every wildcard MITM handshake connection-refused
(static fallback never engages, Enabled still true).

Fix spec: `labelSDSPort = "dev.clawker.firewall.sds_port"` const (why-comment per validator
draft — do NOT hard-spell the label value elsewhere) + driftLabels() entry
`strconv.Itoa(s.cfg.ControlPlaneSettings().SDSPort)` + desired/running sds_port Str fields on
the `firewall_container_spec_drift` log (~:1008) + drift test row in
`stack_drift_internal_test.go` mirroring the stack_build_sha case (newDriftFixture builds from
driftLabels(), so spec test needs no edit) + fix `controlplane/firewall/CLAUDE.md` stack.go row
(label count already stale: says four, five exist, will be six).

## Finding 6 — APPLY: commit-flag defer in sdscerts writeAtomic

`controlplane/sdscerts/sdscerts.go:143-168` `writeAtomic`: write/close/chmod arms remove the
temp file; rename arm leaks it. Leak is post-chmod 0644 → world-readable private-key temp in the
0o755 dir bind-mounted into Envoy; runs 3×/ensureConfigs, unbounded accumulation, no log.

Fix spec (user chose commit-flag defer over inline remove): `internal/storage/write.go:242-247`
pattern — `committed := false; defer func() { if !committed { _ = os.Remove(tmpName) } }()`
right after CreateTemp (with why-comment carrying the key-material rationale), drop the three
inline removes (defer covers them), set `committed = true` after successful rename. Keep the
existing chmod nolint + why-comments. User declined the companion no-residue failure test.

## Findings 7–9 — APPLY: three stale-comment rewrites (comment-only)

All three describe the pre-d1747c15 shared telemetry lane; production is sdsCertsReady-gated
(independent of infraCertsReady) with dedicated SDS material the SAN pin enforces.

7. `envoy_config_test.go:281` + `:302-307`: replace "rides the same infra mTLS gate as als" /
   "sds rides the same infraCertsReady gate as als.MTLS in production". New text: sds has its
   own sdsCertsReady gate (Stack.sdsConfig), independent of als.MTLS's infraCertsReady
   (Stack.alsConfig); the mtls golden row enables both as a TEST-DESIGN choice (one golden pins
   both additions — selector swap + sds_cluster, diff vs `comprehensive` purely additive except
   wildcard chains' transport sockets). Validator-drafted text in triage transcript; verify
   against current line content before editing.
8. `envoy_consts.go:56-58`: drop "Shared by the OTel ALS cluster and the SDS cluster" →
   telemetry-lane mTLS client material, used ONLY by the OTel ALS cluster; SDS cluster dials
   with the dedicated envoySDSTLS*File identity below.
9. `envoy_types.go:43-48` SDSConfig doc: remove "reuses the same /etc/envoy/otel-tls client
   material" + infra-material degraded-mode claim. New text: dial authenticates with the lane's
   OWN dedicated client leaf (consts.EnvoySDSClientName / envoySDSTLS*File — server pins that
   SAN, refuses the telemetry leaf); Enabled=false = static [apex, *.apex] fallback when the SDS
   lane's own material is unavailable (Stack.sdsConfig gates on sdsCertsReady, independent of
   infraCertsReady). Reference const NAMES, never hard-spell mount paths (repo rule).

## Completion gates for codex

Per-fix: targeted tests only (`go test ./controlplane/firewall/ ./controlplane/sdscerts/
./internal/controlplane/`), NEVER `go test ./...` in-container (tears down host CP). Findings
7–9 need no tests. After 3–5: check golden regen need (`GOLDEN_UPDATE=1` for envoy goldens only
if generation output changed — none of these change it). Update
`controlplane/firewall/CLAUDE.md` (finding 5 label table; finding 3 new castore.go row). Commit
per finished task, GPG-signed, push. Resolve each GitHub thread (IDs above) as its fix lands.
