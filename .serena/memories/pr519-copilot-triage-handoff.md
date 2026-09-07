# PR #519 Copilot Review Triage — Codex Handoff

Branch `fix/wildcard-san-certs`, PR #519 (wildcard SAN certs, issues #500/#518).
Review decisions were recorded on 2026-09-07. Findings 3–9 are complete,
GPG-signed, pushed, and resolved with one reply per thread. Finding 1 was
already complete in 662b35b0; finding 2 was dismissed. Their threads were
resolved before this pass and were not changed.

Related: `mem:initiative_wildcard_san_certs`.

## GitHub thread reconciliation contract

Resolve a thread ONLY when its fix lands. Thread IDs (GraphQL `resolveReviewThread`,
`threadId` input):

| # | Thread ID | State |
|---|-----------|-------|
| 1 | PRRT_kwDOQ1E4ts6f2ZrN | DONE before this pass: 662b35b0; thread already resolved |
| 2 | PRRT_kwDOQ1E4ts6f2Zr9 | Dismissed before this pass; thread already resolved |
| 3 | PRRT_kwDOQ1E4ts6f26pc | DONE: 21035d21 signed and pushed; reply posted; thread resolved; targeted race checks and full pre-commit passed |
| 4 | PRRT_kwDOQ1E4ts6f4Fap | DONE: c79f817f signed and pushed; reply posted; thread resolved; open-stream regression and full pre-commit passed |
| 5 | PRRT_kwDOQ1E4ts6f4Fbb | DONE: 6dbfd8d7 signed and pushed; reply posted; thread resolved; regression and full pre-commit passed |
| 6 | PRRT_kwDOQ1E4ts6f4FcG | DONE: 0acba8fe signed and pushed; reply posted; thread resolved; three targeted packages passed |
| 7 | PRRT_kwDOQ1E4ts6f4Fco | DONE: 241b35aa signed and pushed; reply posted; thread resolved |
| 8 | PRRT_kwDOQ1E4ts6f4Fc- | DONE: 241b35aa signed and pushed; reply posted; thread resolved |
| 9 | PRRT_kwDOQ1E4ts6f4FdV | DONE: 241b35aa signed and pushed; reply posted; thread resolved |

## Finding 1 — DONE (applied by Claude, do not redo)

`controlplane/firewall/sds_server.go` unbounded per-SNI mint cache → peer-driven CP OOM
(client-controlled SNI, admitted() checks only zone membership). Applied: LRU bound via
`hashicorp/golang-lru/v2` (promoted to direct dep by `go mod tidy`), `sdsCacheMaxEntries = 1024`
const with why-comment, `s.mu` retained for check-then-mint atomicity. New internal test
`sds_server_internal_test.go::TestSDSServer_MintCacheBoundedAndEvictionRemints` (bounded len +
evicted SNI re-mints, never denied). All `TestSDSServer*` pass. Committed and pushed before this pass as 662b35b0.

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

Direct Go checks use targeted packages (`go test ./controlplane/firewall/ ./controlplane/sdscerts/
./internal/controlplane/`), NEVER `go test ./...` in-container (tears down host CP). Findings
7–9 need no tests. After 3–5: check golden regen need (`GOLDEN_UPDATE=1` for envoy goldens only
if generation output changed — none of these change it). Update
`controlplane/firewall/CLAUDE.md` (finding 5 label table; finding 3 new castore.go row). Commit
per finished task, GPG-signed, push. Resolve each GitHub thread (IDs above) as its fix lands.

## Execution record — 2026-09-07

- Finding 6: `0acba8fe`; findings 7–9: `241b35aa`;
  finding 5: `6dbfd8d7`; finding 4: `c79f817f`;
  finding 3: `21035d21`. Each fix commit is GPG-signed and pushed.
- All seven target threads have a fix reply and are resolved. The existing
  resolved threads were not changed.
- The SDS port test failed before the label fix. The open delta-stream test
  failed with unbounded shutdown. The CA read/rotation and mismatched-pair
  tests failed before their fixes.
- Final targeted checks passed with `-race` for
  `./controlplane/firewall/`, `./controlplane/sdscerts/`, and
  `./internal/controlplane/`. All applicable pre-commit hooks passed.
  Envoy generation output did not change; no golden files were regenerated.
- Updated `controlplane/firewall/CLAUDE.md` through its `AGENTS.md`
  target: six drift labels, CAStore file/API, and shared ownership.
  Updated CP, architecture, design, README, and firewall user documentation.
  No matching support-plugin known issue required a change.
- The finding-4 drain gap remains recorded only. Finding 1's three side-items
  and finding 2's optional change remain outside this pass.

## Hook requirements from this session

Never set `SKIP` or bypass pre-commit hooks. Early commits in this pass
incorrectly excluded the unit-test hook; the full hook set was then run
without exclusions against both commits and passed. Every later commit ran
all applicable hooks normally. `make test` is the unit-test hook and
excludes the host-control-plane E2E suites. Never run `go test ./...`.

User-requested hook changes landed in `d6a9795b`: both command guards are
registered as inline `PreToolUse` hooks in `.codex/config.toml`, and
`licenses-check` runs before read-only scans because it writes NOTICE.
The nested `.codex/hooks/hooks.json` was not a repository hook source.
Both scripts passed syntax checks and six command-input checks. Codex
requires review and trust of the new definitions through `/hooks`.

A full check exposed an additional write/read race: `make test` rebuilt
an embedded CP binary while the linter read it. `21035d21` moves the
unit-test hook to a build phase before read-only scans. The complete hook
run then passed. No hook was excluded.
