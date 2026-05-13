# SAN-Refactor Initiative — Universal Trust Middleware

**Branch:** `fix/invalid-name-len`
**Parent memory:** —
**PRD Reference:** PR review thread on commit `94ec999a` (`fix(auth): move agent canonical identity from CN to URI SAN`)

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 0: Goose migrations adopted (00001_init.sql baseline) | `complete` | — |
| Task 1: Build `ContainerByPeerIP` resolver + interface seam | `complete` | — |
| Task 2: Redesign `IdentityInterceptor` as universal peer-IP→Docker→label middleware | `complete` | — |
| Task 3: Refactor `Register` handler — drop redundant checks, use middleware-resolved labels | `complete` | — |
| Task 4: Drop `Registry.Lookup`, simplify Registry surface, rework `Dialer.classifyRegistry` | `complete` | — |
| Task 5: Migration `00002_drop_canonical_cn.sql` | `complete` | — |
| Task 6: Rename `Canonical*` → `AgentFullName*` everywhere (auth + agent + sweeps) + typed `Entry` fields | `complete` | — |
| Task 7: Update existing tests + add missing tests | `complete` | — |
| Task 8: Update CLAUDE.md docs + final test run | `complete` | — |

## Key Learnings

- **Trust anchor is peer IP, not cert claims** — cert SANs are what we VERIFY against an independent source (Docker labels resolved via kernel-attested peer IP). Looking up "container identity" via a cert SAN value (e.g., `urn:clawker:container:<id>`) is begging the question — see memory `feedback_trust_anchor_peer_ip`.
- **No "canonical" in identity naming** — use `AgentFullName` for the `clawker.<project>.<agent>` string. See memory `feedback_no_canonical_naming`.
- **Auth middleware is universal** — applies to all AgentService RPCs including Register. The registry-row lookup is NOT auth; it's bookkeeping populated AFTER auth passes via the peer-IP-grounded check.
- **CP must not panic** — see root `CLAUDE.md`. New code on CP boot path returns `(nil, error)`; degraded subsystems emit `event=<subsystem>_unavailable` structured logs.
- **Goose migration framework** chosen for sqlite; SQL-file migrations co-located at `internal/controlplane/agent/migrations/` and embedded via `embed.FS` so they ship inside the CP binary (no host-side bind mount for source assets).
- **Pre-goose DB transition handled via one-shot pre-flight** — alpha policy: drop pre-goose `agents` table when `goose_db_version` absent. Registry state regenerates on next clawker run.
- **Commit cadence:** intermediate commits at natural phase boundaries (per user preference). Goose adoption already committed as `0dce04cb`.
- **Task 1 refinements (review-driven, deviate from spec):**
  - `ResolvedContainer.Project` / `AgentName` are typed (`auth.ProjectSlug` / `auth.AgentName`), NOT plain strings. The resolver validates labels via `auth.NewProjectSlug` / `auth.NewAgentName` so a zero-value `ResolvedContainer` cannot carry empty strings into a downstream identity compare (closes a fail-open seam — empty cert SAN vs empty label compares equal in `subtle.ConstantTimeCompare`). **Task 2 therefore should NOT re-validate labels** — call `auth.CanonicalAgentCN(resolved.Project, resolved.AgentName)` directly with typed args. The Task 2 `Must*`-constructor concern is moot.
  - Added a second sentinel `ErrInvalidAgentLabels` (file: `peer_lookup.go`). Returned when the IP-matched container's labels can't form a valid identity (missing/malformed). Distinguishes daemon-state corruption from clean "no match". Task 2's interceptor should emit a distinct reject path when the resolver returns this — the structured log was already emitted inside the resolver as `event=peer_lookup_invalid_labels`.
  - Empty project label is LEGITIMATE (2-segment agent name design — `clawker.agent`); `auth.NewProjectSlug("")` returns a zero-value slug with no error. Empty agent label is rejected.
  - Resolver uses `context.WithTimeout(context.Background(), peerLookupTimeout)` (decoupled from per-RPC ctx). Mirrors `register_handler.go:200`. Without this, a caller cancel turns into a spurious `ErrNoContainerForPeerIP`.
  - Per-container `ContainerInspect` failures log + CONTINUE iteration. If no candidate matches AND any inspect erred, the wrapped daemon error propagates so callers can distinguish "no agent owns this IP" from "we couldn't tell". Only `errors.Is(err, ErrNoContainerForPeerIP)` indicates clean no-match.
  - Structured logs added with `peer_ip` + `container_id` fields: `event=peer_lookup_list_failed` (daemon list failure), `event=peer_lookup_inspect_failed` (per-container; continues), `event=peer_lookup_invalid_labels` (matched but unusable).
  - `StartDeps.PeerLookup` is required; `Start` returns error on nil (mirrors Registry/DockerLister/Bus/Dialer guards). Test helper `noopPeerLookup` in `start_test.go` for tests that don't exercise the resolver path.
  - Constructor: `NewMobyPeerLookup(cli mobyclient.APIClient, log *logger.Logger) *MobyPeerLookup`. Concrete return (not interface) — matches `NewMobyContainerInspector` shape.
- **Task 5 refinements (review-driven, deviate from spec):**
  - **Test seeds DB at goose-version-1 + a populated `canonical_cn` row** rather than running on a fresh DB. Silent-failure-hunter CRITICAL: a fresh-DB test cannot distinguish "00002 dropped the column" from "00001 was rewritten and the column never existed". Seeding pre-002 state (manually creating `goose_db_version` with `(0,1)` + `(1,1)` rows, then the v1 `agents` schema with `canonical_cn`, then a row with a valid 64-hex thumbprint) pins the actual migration effect. `migrateFromPreGoose` sees `goose_db_version` exists and no-ops; goose.Up applies ONLY 00002. Test then asserts (a) column gone via `PRAGMA table_info`, (b) the pre-existing row survives via `Snapshot()` — catches stub 00002, accidental `DROP TABLE`, or wrong-column drop.
  - **Seed thumbprint MUST be valid hex of `sha256.Size` (32 bytes / 64 hex chars).** `scanEntry` rejects malformed thumbprints with `agentregistry: malformed thumbprint_hex` and `Snapshot()` skips them — so a placeholder like `'legacy-hex'` makes the row invisible to `Snapshot` and the data-preservation assertion fails for the wrong reason. Use `'00...01'` (62 zeros + `01`).
  - **`applySchema` partial-apply fix (out-of-spec, drive-by improvement):** previously logged `results` only on the success branch — on partial failure the operator only saw the wrapped error, not which version had landed. Iterate `results` before checking `upErr` so a stuck DB triages cleanly. Silent-failure-hunter MEDIUM.
  - **Skipped:** SQLite `ALTER TABLE DROP COLUMN` version probe. modernc.org/sqlite v1.x ships SQLite 3.45+ for years; alpha YAGNI. If a downgrade ever lands, the migration fails with a clear sqlite error on `ALTER TABLE DROP COLUMN`.
  - **Migration Up comment framed as design-state, not historical transition** (comment-analyzer). Reads as "AgentFullName lives in a urn:clawker:agent: URI SAN; trust derives from peer IP via Docker labels" rather than "the SAN refactor moved...". Migration files are read in isolation later; the comment must stand alone.
  - **No `selectEntryCols` change needed** — column list already excluded `canonical_cn` since Task 4. The migration just makes the on-disk schema agree with what readers were already expecting.

- **Task 4 refinements (review-driven, deviate from spec):**
  - **`outcomeRegistryCNMismatch` enum value DELETED** — not "renamed to ThumbprintMismatch" (that variant already existed). Post-Task-4 `classifyRegistry` is thumbprint-only; the CN-compare logic moved entirely upstream to `IdentityInterceptor`. `dispatchAgentEvents` lost its CNMismatch case; the default branch was folded into `outcomeRegistryNotQueried` (same `AgentUntrusted{ReasonCertInvalid}` payload, distinct Detail strings for triage).
  - **`computeCNPinMatch` + `canonicalCNFromStrings` DELETED** as orphan/dead code — `computeCNPinMatch` had no production callers (only its own test exercised it), and `canonicalCNFromStrings` was only used by `computeCNPinMatch` and the deleted CN branch in `classifyRegistry`.
  - **`memEntry` wrapper DELETED** — its sole purpose was caching the pre-computed canonical CN for the old in-memory `Lookup`. With Lookup gone, `registryImpl.entries` is `map[Thumbprint]Entry` directly (five iteration sites simpler; the returned `Entry` is now the stored value, not a projection).
  - **`canonicalCNFromEntry` DELETED** — Add no longer validates Project/AgentName string-validity. Per `register_handler.go:210-225` the wire boundary already validates via `auth.NewProjectSlug`/`auth.NewAgentName` and constructs typed values. Comment on `Registry.Add` calls this out: "Identity-string validity is enforced upstream by the Register handler at the wire boundary." Test-design-analyzer noted the type system doesn't enforce this — `Entry.Project`/`Entry.AgentName` could be typed as `auth.ProjectSlug`/`auth.AgentName` to make the invariant compile-time-checked. **Deferred to Task 6** (the rename sweep is the natural home; Task 4 scope is "drop Lookup + rework dialer").
  - **`canonical_cn` column not yet dropped from schema** — `sqliteRegistry.Add` writes empty string (`""`) until Task 5 migration 00002 drops the column. Comment in `registry_sqlite.go` calls out the handoff. The column is NOT NULL with no default, so the empty literal is required to keep the INSERT valid in the interim. No reader consults the column (already excluded from `selectEntryCols`).
  - **`classifyRegistry` signature narrowed** from `(peerInfo, containerID)` to `([sha256.Size]byte peerThumbprint, containerID)` — function only reads `PeerThumbprint`. Documents the post-refactor truth that the dialer no longer touches `PeerAgentFullName` for trust classification; that field stays on `peerInfo` purely for the SessionConnected event payload diagnostic.
  - **Test deletions:** `TestClassifyRegistry_CNMismatch`, `TestComputeCNPinMatch`, `TestRegistry_Add_RejectsMalformedIdentity`, `TestSQLiteWriter_Add_PersistsCanonicalCN`, `TestSQLiteRegistry_Lookup_CNMismatch_ReturnsUnknownAgent`, `TestSQLiteRegistry_Lookup_UnknownThumbprint`, `TestRegistry_Lookup_EmptyProject`, the `CNMismatch` row in `TestDispatchAgentEvents_OutcomesPinned`. Plus tautological in-memory variants (`TestRegistry_EvictByContainerID`, `TestRegistry_ReRegisterAfterEvict`, `TestRegistry_Snapshot_SortedAcrossProjects`) per test-hunter — production `Registry` is the sqlite impl; in-memory `NewRegistry` has zero production callers so those tests were stub-on-stub. `TestSQLiteRegistry_LookupByContainerID` merged into `TestSQLiteRegistry_EvictByContainerID_DeletesRow` with the thumbprint-round-trip assertion folded in. `mustAdd` helper deleted (no callers left).
  - **CLAUDE.md doc updates in same commit:** trust-outcomes table dropped CNMismatch row + added note that SAN-vs-label drift detection is now upstream in IdentityInterceptor only (silent-failure-hunter flagged the dialer no longer catches it on outbound, which is by-design per spec but needed documentation). Identity-contract paragraph rewritten — Registry rows store `(thumbprint, container_id, project, agent_name, registered_at, last_seen)` and the displayed AgentFullName is reconstructed on demand. The full doc sweep across other CLAUDE.md files lives in Task 8.
  - **Silent-failure-hunter MEDIUM finding (deferred):** Registry.Add silently widened surface — a future second writer could store malformed project/agent_name strings since validation moved upstream. Decision: typed-field refactor of `Entry` (per type-design-analyzer) is the right fix; lives in Task 6 alongside the Canonical* → AgentFullName* sweep. Doc comment on `Registry.Add` calls out the upstream-validation contract so a future reader has the trail.

- **Task 3 refinements (review-driven, deviate from spec):**
  - **ContainerInspector seam deleted entirely.** Spec said "drop labelsMatchRequest + peerIPMatchesContainer". Once both went, `Inspect` was unused, so `ContainerInspector` interface + `NewMobyContainerInspector` + `mobyContainerInspector` adapter + `inspectTimeout` const + `fakeContainerInspector` test fake + `inspector` field on Handler + inspector arg on `NewHandler` all dropped. The middleware (Task 2) owns Docker resolution; the handler is now Docker-free.
  - **`peerLeafAndIP` deleted; handler uses `peerLeafFromContext`** (defined in `handler.go`). One source of truth for peer cert extraction.
  - **Constant-time compares dropped for non-secret values.** ProjectSlug, AgentName, ContainerID are daemon-attested labels / public docker IDs — not secrets. `subtle.ConstantTimeCompare` is theatre here; plain `!=` is equally secure. `crypto/subtle` import dropped from `register_handler.go`. Thumbprint compare stays as direct `[32]byte` equality.
  - **Missing peer cert post-resolve has its own event.** `event=agent_register_peer_cert_missing_post_resolve` (renamed from `agent_register_no_peer`) — operator triage distinguishes "ctx tampered between interceptor and handler" from "no peer at handler entry". Still PermissionDenied (fail-secure as identity reject), not Internal (debatable — could go either way; PermissionDenied chosen because the interceptor itself classifies its peer-missing case the same way).
  - **Request-vs-resolved cross-check kept and documented.** The middleware confirms cert↔labels; the handler additionally confirms request-body↔resolved. Catches a clawkerd whose cert/labels agree but RPC body claims a different identity (defense-in-depth against client bug, not malicious peer). Justification promoted into the Handler doc comment so a future reader doesn't delete the check as dead weight.
  - **Trust ordering documented at the type:** daemon labels > cert claim > request claim. Persisting `resolved.*` (label-derived) over request body keeps the registry aligned with the daemon view.
  - **Defense-in-depth thumbprint capture:** comment in handler explains thumbprint is derived from the live peer cert at the handler (rather than surfaced via ctx) so the gate that persists the thumbprint and the gate that produced it stay co-located — a future interceptor change can't silently substitute the value the registry stores.
  - **Test consolidation via table-driven layout.** Flat per-failure-mode tests collapsed into `TestRegister_RequestValidation`, `TestRegister_CtxGates`, `TestRegister_IdentityCrossChecks` (4 rows: req-project, req-agent, no-container-SAN, container-SAN-mismatch), `TestRegister_RegistryBranches` (4 rows: idempotent, replay, lookup-error, add-error). Plus standalone `TestRegister_HappyPath`. Cuts ~80 lines vs the prior flat layout.
  - **`TestNewHandler_RejectsNilDeps` deleted** per test-hunter. With only one nil-rejected dep, the test was a one-line linter-equivalent check; the constructor's nil-rejection contract survives via the only call site (`cmd/clawker-cp/main.go`) which already checks the error return.
  - **`resolvedCtx` test helper signature shrunk:** dropped the `remoteIP string` parameter (handler doesn't compare peer.Addr post-refactor — middleware already grounded the resolved container in peer IP). Hardcoded loopback addr inside the helper.
  - **CLAUDE.md doc updates in same commit:** `internal/controlplane/agent/CLAUDE.md` Identity contract rewritten around peer-IP trust anchor; Register flow step 5 reworded; Method-scopes-+-opt-out → universal three-stage gate (no more opt-outs); Files + Test seam tables refreshed for inspector removal + ResolvedContainer ctx helpers. `peer_lookup_moby.go:19` stale `inspectTimeout` reference scrubbed.

- **Task 2 refinements (review-driven, deviate from spec):**
  - `WithResolvedContainer` does NOT panic on empty `ContainerID` — that violates the root CLAUDE.md CP no-panic-on-serving-path rule. Instead: zero-value `ResolvedContainer` is silently dropped (ctx returned unchanged), and `ResolvedContainerFromContext` returns `ok=false` for both absent and empty-ID cases. The "silent identity vacuum" floor moves to the read side. Test pins this contract: `TestWithResolvedContainer_EmptyContainerIDIsNoOp`.
  - Empty cert SAN gets an **explicit** reject stage (2a) with its own `event=agent_identity_no_agent_san` log — rather than relying on the stage-3 `subtle.ConstantTimeCompare` natural-fail on length mismatch. Explicit check gives operators better diagnostic fidelity AND short-circuits the Docker round-trip when the cert is malformed.
  - `peerIdentity.Raw` field removed (was unused — speculation-based YAGNI). `peerIdentityFromContext` delegates to `peerLeafFromContext` instead of duplicating the cert extraction, dropping ~10 lines of duplication.
  - `remoteAddrToNetip` moved from `register_handler.go` to `handler.go` (single source of truth in the same package). Task 3 will sweep `peerLeafAndIP` to share `peerIdentityFromContext`'s extraction path.
  - Test file dropped two linter-replaceable tests (nil-peer-lookup constructor guard, absent-ctx-key returns-false) and merged the two stage-2 resolver-error tests into a 3-row table-driven test that also covers the generic-daemon-error default branch.
  - Five distinct `event=` log fields per reject stage for operator triage: `agent_identity_peer_auth_missing`, `agent_identity_cn_mismatch`, `agent_identity_no_agent_san`, `agent_identity_peer_lookup_no_match` / `_invalid_labels` / `_error`, `agent_identity_san_label_mismatch`. Wire envelope stays uniform `"registration rejected"` regardless.

- **Task 7 refinements (review-driven, deviate from spec):**
  - **All 6 IdentityInterceptor tests required by the spec already existed** post-Task 2 (HappyPath_AttachesResolvedContainer, WrongCN_PermissionDenied, EmptyCertSAN_PermissionDenied, CertSANvsLabelMismatch_PermissionDenied, Stage2_ResolverErrors_PermissionDenied which covers NoContainerForPeerIP, Register_HitsTrustCheck). No new IdentityInterceptor tests added in Task 7. Verified by `get_symbols_overview` on `identity_interceptor_test.go`.
  - **TestAgentCert_MultipleAgentURISANs deleted per test-hunter** — was originally added per spec but pinned phantom behavior. Production `MintAgentCert` only ever emits ONE `urn:clawker:agent:` SAN, so the "first-match-wins" contract had no production consumer. Test-hunter rated DELETE; silent-failure-hunter also flagged a latent gap (no non-matching URI preceding the match). Test removed; `net/url` import removed alongside.
  - **`signLeafCNSAN` helper collapsed into `signLeaf`** per code-simplifier. Original Task 7 commit added a near-duplicate helper for the new `TestCapturePeer_DistinctCNAndSAN` test (only difference: distinct CN vs SAN args + hardcoded notAfter). Refactor: `signLeaf` signature now takes `(cn, sanFullName, notAfter, ...)`. Three existing callers pass cn for both params (chain-mechanics tests don't care); the new distinct-CN-vs-SAN test passes the two distinct strings. Saves ~25 lines + the explanatory comment about why two helpers exist.
  - **`auth.MaxShortNameLen()` accessor added** (`internal/auth/identity.go`) per type-design-analyzer. The first cut of `TestContainerName_HeadroomForMaxFields` hardcoded `maxField = 50` with a runtime `auth.NewProjectSlug(50-char string)` guard. The guard caught a SHRINK of `shortNameMax` but NOT a BUMP — exactly the regression the test claims to catch. Exporting an accessor function (not the const itself; keeps `shortNameMax` private) lets the test consume the source of truth. The dual-assertion dance is gone; the test now actually fails when shortNameMax bumps past the 128-char budget.
  - **`TestCapturePeer_DistinctCNAndSAN` KEPT** as load-bearing — existing `TestCapturePeer_ValidChain` collapses CN==SAN, so a regression that read PeerAgentFullName from `Subject.CommonName` would still pass it. This test is the only one that fails when every agent silently appears as `clawker-clawkerd` to subscribers.
  - **`TestContainerName_HeadroomForMaxFields` KEPT** — values flow through real `auth.NewProjectSlug`/`NewAgentName` / `ContainerName` / `VolumeName`; pins the exact "agent name too long" regression the branch fixes. Walks all three production purposes (`config`, `history`, `workspace`) so adding a longer suffix without re-checking the budget fails here.
  - **Comment scrubs (mechanical, "canonical" → "AgentFullName" in identity contexts):**
    - `internal/auth/identity_test.go:109-112` — `TestCanonicalAgentCN_TwoVsThreeSegment` → `TestAgentFullName_TwoVsThreeSegment`.
    - `internal/auth/agent_cert_test.go:59-65, 117, 123-125` — doc comments + `gotCanonical` → `gotAgentFullName`.
    - `internal/cmd/container/shared/agent_bootstrap_test.go:48, 54` — doc comments.
    - `internal/controlplane/agent/register_handler_test.go:69, 89-90, 226-272` — `agentCanonical` → `agentFullName` (signTestLeaf param), `goodCanonical` → `goodAgentFullName`, `certAgentCN` field → `certAgentSAN` (the old name was the rot — value always held a SAN, never a CN).
    - `internal/controlplane/agent/dialer_test.go:72-81` — `signLeaf` doc trimmed and reframed around the cn/sanFullName split.
    - `internal/cmd/container/shared/agent_bootstrap_test.go:55` — `auth.AgentCanonicalFromCert` ref removed (was already done in Task 6 production code).
    - `internal/docker/names_test.go:358` — "canonical-CN composition" → "urn:clawker:agent: SAN AgentFullName composition" in `TestGenerateRandomName_AlwaysValidAsAgentName`.
  - **Generic-English "canonical" KEPT** in `internal/auth/identity_test.go:98` ("one canonical invalid + one canonical valid per type") — describes test-shape, not identity. Code-reviewer NIT noted ambiguity in the now-AgentFullName-named context; left as-is per initiative spec ("Generic-English canonical may stay").
  - **Test-hunter Finding 1 (latent gap)** — `TestAgentFullNameFromCert_MultipleAgentURISANs` didn't cover the case where a non-matching URI precedes the matching one. Made moot by the test's deletion.
  - **CLAUDE.md doc updates deferred to Task 8** — `internal/auth/CLAUDE.md` and `internal/controlplane/agent/CLAUDE.md` still reference dead symbols `CanonicalAgentCN` / `AgentCanonicalFromCert` in prose. Code-reviewer flagged but per the initiative spec Task 8 is the doc-sweep task.

- **Task 6 refinements (review-driven, deviate from spec):**
  - **Serena `rename_symbol` corrupted `register_handler.go` during `AgentCanonicalFromCert → AgentFullNameFromCert`** — mangled two regions (one comment, one log message + return statement). Restored from `git show HEAD`, re-applied only the typed-field edit. **Lesson:** always `go vet ./...` after each `rename_symbol`. Failure mode silently lands corrupted bytes that pass schematic checks but error at compile.
  - **`Entry.AgentName`/`Project` typed as `auth.AgentName`/`auth.ProjectSlug`** — closes the Task 4 silent-failure-hunter MEDIUM. Construction is unforgeable outside `internal/auth`; the compiler now rejects raw-string writers. Test sites updated to wrap literals via `auth.MustAgentName("dev")` / `auth.MustProjectSlug("p")`. Verified `MustAgentName`/`MustProjectSlug` have ZERO non-test callers (production goes through `auth.NewAgentName`).
  - **`scanEntry` re-validates strings on read** via `auth.NewAgentName` / `auth.NewProjectSlug`. Returns an error that `Snapshot()` logs and skips (now with `event=agentregistry_row_skipped` + a per-Snapshot `event=agentregistry_snapshot_skipped_rows` summary line carrying the count — silent-failure-hunter MEDIUM). `LookupByContainerID` propagates the validation error up; Register handler currently maps to `codes.Internal` and the agent's container is stuck until manual eviction — a DOS path against re-registration if a row ever corrupts. **Deferred** as a Task 7+ follow-up: typed sentinel `ErrMalformedEntry` + handler evicts-and-retries.
  - **`validateEntry`'s panic family stayed as-is** (with new `entry.AgentName.IsZero()` gate added). Pre-existing tech debt: `validateEntry` is reachable from the CP gRPC handler goroutine post-`SetReady`, violating the root CLAUDE.md "no panic on CP serve path" rule. Today the sole production caller (`register_handler.go::Register`) sources Entry from middleware-resolved typed values that cannot trip any gate, but the safety belongs in the API. `FIXME(cp-serve-path)` doc-comment block added so the next reader doesn't dismiss the issue. **Deferred** to a follow-up cleanup; out of Task 6 scope (rename + typed fields, not error-handling rewrite).
  - **`server.go::ListAgents` re-sort deleted** (code-simplifier finding). Snapshot's interface contract guarantees `(Project, AgentName)` ordering; re-sorting on the wire just duplicated the comparator across in-memory + sqlite + this consumer. Drops `sort` import. The two registry impls' sort closures remain duplicates of each other — extracting `sortEntries` is nice-to-have but out of Task 6 scope.
  - **`auth.AgentName.Less` / `ProjectSlug.Less` typed comparators NOT added** (type-design-analyzer + code-simplifier nice-to-have). Three sort sites still use `.String() < .String()`. Trade-off was small win for moderate API surface expansion; left for a future refactor.
  - **`auth.AgentSANScheme` doc** updated to mention dialer's `capturePeer` reads the SAN as a diagnostic for `SessionConnected` events (not for trust gating). The earlier comment dropped the dialer entirely; would have misled a future reader into thinking the dialer is decoupled from the SAN.
  - **Comments scrubbed:** `internal/auth/{agent_cert,identity}.go`, `internal/controlplane/agent/{registry,registry_sqlite,dialer}.go`, `internal/cmd/container/shared/{agent_bootstrap,container_start}.go`, `internal/docker/env.go` — all identity-context "canonical" residue swept. Generic-English "canonical container ID" / "canonical aud claim" intentionally kept.
  - **Skipped intentionally:** test-file comment cleanup (`identity_test.go:98`, `agent_cert_test.go:59,64,117`, `register_handler_test.go:69,231`, `dialer_test.go:74,78`, `identity_interceptor_test.go:88`). These are Task 7 scope.

- **Task 8 refinements (review-driven, deviate from spec):**
  - **`make test` requires GOFLAGS override.** Makefile line 17 hardcodes `GOFLAGS := -trimpath`, overriding any user-set value. Inside a clawker container the worktree's `.git` is a gitdir-pointer file, and go's VCS probe fails with `error obtaining VCS status: exit status 128` — `go list ./...` then drops every package from stdout and `UNIT_PKGS` expands to empty, breaking the test target with "no packages found". Fix: invoke as `make test GOFLAGS="-trimpath -buildvcs=false"` (make-level override, not env). 4643 tests pass / 8 legitimate skips.
  - **`internal/controlplane/agent/CLAUDE.md` Identity contract section** got an Entry-typed-fields paragraph appended (per type-design-analyzer in spirit) — the `auth.ProjectSlug` / `auth.AgentName` typing + `scanEntry` re-validation + skip-row event names are documented inline so a future reader doesn't need to dig into `registry_sqlite.go` to understand the on-read validation policy.
  - **`internal/controlplane/CLAUDE.md` Startup step 8** got a full rewrite. The pre-Task-2 description ("resolves the peer cert thumbprint to a registered agent for every non-opted-out RPC") was load-bearing-wrong post-refactor — the new middleware is peer-IP-grounded, NOT thumbprint-resolved, and there ARE no opt-outs. Rewritten to spell out the three-stage gate (a) CN pin (b) peer-IP→Docker→labels via `ContainerByPeerIP` (c) cert SAN ↔ label compare, plus the `WithResolvedContainer` ctx attachment. Mentions `agent.NewMobyPeerLookup` as the production wiring and "Register included" so a future reader doesn't add a method-allowlist.
  - **`.claude/docs/KEY-CONCEPTS.md` `agent.Registry` row** rewrote the now-stale `Lookup(thumbprint, cn)` API description. Replaced with the current surface (`Add` / `LookupByContainerID` / `EvictByContainerID` / `Snapshot`), the row tuple, typed `Project` / `AgentName` fields, and the goose-managed-migrations note.
  - **`.claude/docs/KEY-CONCEPTS.md` `agent.WithEntry` / `EntryFromContext` row** replaced with `WithResolvedContainer` / `ResolvedContainerFromContext` row — the prior helpers were deleted (Task 2 consolidated the trust-projection seam). New row documents the "zero-value-is-no-op + read-side detects" contract from Task 2.
  - **`.claude/docs/KEY-CONCEPTS.md` `shared.AgentBootstrap` row** deleted `RegisterAgentInRegistry` from the slash-list — function no longer exists (CP is the sole registry writer per Task 3+). Added explicit "registry row is NOT written by the CLI" sentence so a future reader doesn't re-introduce host-side writes.
  - **`.serena/project.yml` auto-churn reverted** from staged diff per code-reviewer NIT. The file is Serena-managed and changes on each onboarding run; staging it with a docs-only commit added 60+ lines of unrelated noise.
  - **Comment-analyzer + code-reviewer reported `clean`.** Silent-failure-hunter flagged two items new (Snapshot query-failure swallow; migration 00002 Down-path empty-CN backfill) plus one that was already in the spec's Deferred list (`sanTailFromCert` three-state). The two new items are added to Deferred — out of Task 8 scope (docs).
  - **CLAUDE.md doc updates NOT done in Task 6** (deferred to Task 8 per the initiative spec). Stale references to track in Task 8:
    - `internal/controlplane/agent/CLAUDE.md:80, 188-189` — `canonical_cn` column past tense; `CanonicalAgentCN`/`AgentCanonicalFromCert` symbol renames in Imports section.
    - `internal/auth/CLAUDE.md:31` — `CanonicalAgentCN`, `AgentCanonicalFromCert`, `urn:clawker:agent:<canonical>`, `canonical_cn` column references.
    - `internal/cmd/container/shared/CLAUDE.md:57` — `auth.CanonicalAgentCN` symbol + "canonical CN" framing.
    - `.claude/docs/KEY-CONCEPTS.md:41,46,47,49` — `canonical_cn` column, `Lookup(thumbprint, cn)`, `CanonicalAgentCN`, pre-computed canonical column writes.
  - **Tests pinning new behavior:** added "zero agent name" subtest to `TestRegistry_Add_RejectsInvariantViolations` (pins the new `validateEntry` IsZero gate). Test-hunter rated KEEP/borderline — the gate catches direct struct-literal omission, which the type system alone cannot. Other test changes are mechanical type adaptation (string → typed wrapper).

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run `pr-review-toolkit:code-reviewer`, `pr-review-toolkit:silent-failure-hunter`, `test-hunter`, `pr-review-toolkit:code-simplifier`, `pr-review-toolkit:comment-analyzer`, `pr-review-toolkit:type-design-analyzer` subagents to review this task's changes, then fix any and all findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

The original commit `94ec999a` moved the per-agent canonical identity from the x509 `Subject.CommonName` to a `urn:clawker:agent:<full-name>` URI SAN because long project/agent name compositions could exceed x509's 64-byte CN limit. CN is now the deterministic literal `consts.ContainerClawkerd` and the new universal CN pin in `IdentityInterceptor` enforces that.

PR review surfaced that the implementation:
1. Added explicit "empty `AgentFullName`" rejection gates in both interceptor (`identity_interceptor.go:163-168`) AND Register handler (`register_handler.go:171-175`) — duplicate, not DRY.
2. Uses `Registry.Lookup` as the auth-time trust gate for non-opt-out methods, but Registry is downstream bookkeeping, not an authoritative source. The chicken-and-egg with Register meant Register opted out of identity check entirely except for the new CN pin.
3. Cross-checked cert SAN `AgentFullName` against REQUEST fields, then separately checked REQUEST against Docker labels. Two compares where one cert↔labels compare suffices.
4. Used "canonical" terminology in log fields, event names, vars, function names — ambiguous identity naming.

The redesign establishes a universal trust middleware grounded in the kernel-attested peer IP:

1. **TLS layer** (already): CLI-CA-signed cert verification
2. **Universal CN pin** (already): `leaf.Subject.CommonName == consts.ContainerClawkerd`
3. **NEW universal step:** Resolve peer IP → Docker `purpose=agent` container with matching clawker-net endpoint IP → read labels → build label-derived `AgentFullName` → constant-time compare against cert SAN `AgentFullName`. Empty SAN fails naturally.
4. Attach resolved (container_id, project, agent_name) to ctx via `WithResolvedContainer`. Handlers read this; no re-inspect.
5. Registry.Lookup goes away. `LookupByContainerID` stays (Register handler idempotency).
6. Dialer's classifyRegistry uses `LookupByContainerID` for thumbprint-replay detection only — no agent-full-name compare needed (middleware on inbound RPCs handles the trust check; the dialer is OUTBOUND CP→clawkerd, where clawkerd's listener enforces strict CP pin).

### Key Files

| File | Role |
|------|------|
| `internal/auth/agent_cert.go` | `MintAgentCert`, `BuildAgentSAN`, `AgentFullNameFromCert`, `AgentFullName` |
| `internal/auth/identity.go` | `ProjectSlug`, `AgentName`, `shortNameMax`, `agentFullNamePrefix` |
| `internal/controlplane/agent/identity_interceptor.go` | Universal peer-IP→Docker→label middleware |
| `internal/controlplane/agent/handler.go` | `peerIdentity`, `peerIdentityFromContext`, `WithResolvedContainer` / `ResolvedContainerFromContext` |
| `internal/controlplane/agent/register_handler.go` | `Handler.Register` — consumes middleware-resolved identity from ctx |
| `internal/controlplane/agent/registry.go` + `registry_sqlite.go` | Registry interface; `Entry` carries typed `auth.AgentName` / `auth.ProjectSlug` fields; `Add`, `LookupByContainerID`, `EvictByContainerID`, `Snapshot` |
| `internal/controlplane/agent/dialer.go` | `classifyRegistry` uses thumbprint compare; `capturePeer` reads agent SAN as diagnostic |
| `internal/controlplane/agent/start.go` | `Start(ctx, StartDeps)` with `PeerLookup ContainerByPeerIP` dep |
| `internal/controlplane/agent/migrations/` | Goose migrations; 00001 init + 00002 drop canonical_cn |
| `cmd/clawker-cp/main.go` | Wires `agent.Start` with the moby-backed peer-IP resolver |
| `internal/auth/CLAUDE.md`, `internal/controlplane/agent/CLAUDE.md`, `internal/cmd/container/shared/CLAUDE.md` | Doc updates (Task 8) |

### Design Patterns

- **Interface seam for Docker:** Mirror existing `ContainerInspector` pattern in `register_handler.go:42-65`. Add `ContainerByPeerIP` interface with method `LookupByIP(ctx, ip netip.Addr) (containerID string, labels map[string]string, err error)`. Production impl wraps moby `ContainerList` with filters `label=dev.clawker.purpose=agent` + network endpoint scan. Test impl is a hand-rolled fake (no moq for one-method interface).
- **Ctx value plumbing:** Follow `WithEntry` / `EntryFromContext` pattern in `identity_interceptor.go:53-82`. Add `WithResolvedContainer` / `ResolvedContainerFromContext` for the peer-IP-resolved (containerID, project, agent_name) triple. Use unexported struct-typed key.
- **Drop `optedOut` map entirely.** With Lookup gone, the universal middleware applies to every method without exception. Register still self-authenticates via per-handler container_id-SAN cross-check, but the trust gate is upstream.
- **Constant-time compares** for every identity equality check (`crypto/subtle.ConstantTimeCompare`). Defense-in-depth against future timing channels.
- **Structured logs** with `event=<kebab_case>` and `peer_ip`, `expected_<thing>`, `cert_<thing>` fields.
- **Generic rejection envelope:** All `PermissionDenied` returns use `"registration rejected"` (or `"register: identity check failed"` in Register) — attackers must not learn which check failed; the structured log carries the classification.
- **Typed identity fields:** `agent.Entry.AgentName` is `auth.AgentName`; `agent.Entry.Project` is `auth.ProjectSlug`. Construction requires `auth.NewAgentName` / `auth.NewProjectSlug` (or `Must*` in tests). `scanEntry` re-runs `NewAgentName` / `NewProjectSlug` on sqlite reads so a malformed row cannot land an invariant-violating value; `Snapshot()` logs and skips on validation failure.

### Rules

- Read root `CLAUDE.md`, `.claude/rules/` files (code-style, testing, dependency-placement), and package `CLAUDE.md` files before starting any task
- Use Serena tools for code exploration — `find_symbol`, `find_referencing_symbols`, `get_symbols_overview` — read symbol bodies only when needed
- **`rename_symbol` corruption risk:** Serena's `rename_symbol` can silently mangle file regions during cross-file renames (encountered in Task 6 on `register_handler.go` during `AgentCanonicalFromCert → AgentFullNameFromCert`). ALWAYS `go vet ./...` after each rename to catch corruption before further edits compound the damage. If corruption is found, restore the affected file from `git show HEAD:<path>` and re-apply only the intended edit.
- All new code must compile; relevant tests must pass (`go test ./internal/auth/... ./internal/controlplane/agent/... ./internal/docker/... ./internal/cmd/container/shared/...`)
- NEVER run `go test ./...` from inside a clawker container — the e2e suite tears down the host CP. Run targeted package tests.
- Follow existing test patterns: `iostreams.Test()`, `configmocks`, moq-generated mocks, table-driven tests with `assert`/`require` from testify
- CP code must NOT panic or `log.Fatal` after `SetReady` — return errors, degrade subsystems with `event=<subsystem>_unavailable` structured logs
- Output streams: data → `ios.Out`, warnings/errors → `ios.ErrOut`. Logger is FILE LOGGING ONLY — never user-visible output.
- No "canonical" in new identifier names — use `AgentFullName`. The sqlite column `canonical_cn` was dropped by Task 5.

---

## Task 7: Update existing tests + add missing tests

**Creates/modifies:**
- All `_test.go` files affected by the rename in Task 6 — comment-level scrub for "canonical" residue (`identity_test.go:98`, `agent_cert_test.go:59,64,117`, `register_handler_test.go:69,231`, `dialer_test.go:74,78`, `identity_interceptor_test.go:88`).
- Add new tests identified in review

**Depends on:** Task 6

### Implementation Phase

1. **Mechanical rename in tests:** `gotCN` → `gotAgentFullName` in `identity_interceptor_test.go:111`, similar patterns elsewhere.

2. **Test-comment scrub:** edit "canonical" → "AgentFullName" (or "agent-full-name" prose) in the test files listed above. Generic-English "canonical" (e.g. test-shape descriptions) may stay.

3. **Add new tests:**
   - `TestIdentityInterceptor_WrongCN_PermissionDenied` — cert with `CommonName != consts.ContainerClawkerd` → reject
   - `TestIdentityInterceptor_NoContainerForPeerIP_PermissionDenied` — peer IP that doesn't match any `purpose=agent` container → reject
   - `TestIdentityInterceptor_CertSANvsLabelMismatch_PermissionDenied` — cert SAN `AgentFullName` differs from label-derived → reject
   - `TestIdentityInterceptor_EmptyCertSAN_NaturallyFails` — no `urn:clawker:agent:` SAN → reject (via label compare, no explicit empty check)
   - `TestIdentityInterceptor_Register_HitsTrustCheck` — Register call goes through universal middleware
   - `TestIdentityInterceptor_HappyPath_AttachesResolvedContainer` — successful resolution puts `ResolvedContainer` on ctx
   - `TestDialer_PeerAgentFullNameFromSAN` — explicit cert with `CN=clawker-clawkerd`, `SAN=clawker.p.dev`, assert `peer.PeerAgentFullName == "clawker.p.dev"` (the helper currently collapses CN==SAN at `dialer_test.go:78-95`)
   - `TestAgentCert_MultipleAgentURISANs` — cert with two `urn:clawker:agent:` URIs → first match wins (pin behavior)
   - `TestContainerName_HeadroomForMaxFields` — `auth.AgentFullName(maxProject, maxAgent)` + longest purpose suffix ≤ 128 bytes
   - `TestMigration_002_DropsCanonicalCN` (added in Task 5 already, just verify it lives)

4. **Drop tests for removed functionality:**
   - Tests for `Registry.Lookup` (now gone)
   - Tests for `labelsMatchRequest` / `peerIPMatchesContainer` if those functions were deleted in Task 3
   - Tests for the `optedOut` map / `IdentityOptedOutMethods`

5. **Run `test-hunter` agent on the test diff** to catch any wasteful tests slipped in.

### Acceptance Criteria

```bash
go test ./internal/auth/... ./internal/controlplane/agent/... ./internal/docker/... ./internal/cmd/container/shared/... -v
```

All tests pass. Coverage on new code paths verified by reading the test functions.

### Wrap Up

1. Update Progress Tracker: Task 7 → `complete`
2. Append key learnings
3. Run review subagents; fix findings
4. Commit: `test(agent): cover universal trust middleware + dialer SAN path`
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the SAN-refactor initiative. Read Serena memory `initiative_san_refactor` — Tasks 1-7 complete. Begin Task 8: Update docs + final test run."

---

## Task 8: Update CLAUDE.md docs + final test run

**Creates/modifies:**
- `internal/auth/CLAUDE.md`
- `internal/controlplane/agent/CLAUDE.md`
- `internal/cmd/container/shared/CLAUDE.md`
- `internal/controlplane/CLAUDE.md` (interceptor wiring step in startup sequence)
- `.claude/docs/KEY-CONCEPTS.md` (canonical_cn / Lookup / CanonicalAgentCN / pre-computed canonical entries)

**Depends on:** Task 7

### Implementation Phase

1. **`internal/auth/CLAUDE.md`:** Update API table — `CanonicalAgentCN` → `AgentFullName`, `AgentCanonicalFromCert` → `AgentFullNameFromCert`. Update prose describing CN-as-canonical to reflect CN=`ContainerClawkerd` literal + AgentFullName-as-URI-SAN. Drop `canonical_cn` column references (column dropped in Task 5).

2. **`internal/controlplane/agent/CLAUDE.md`:**
   - "Identity contract" section — replace any pre-compute `canonical_cn` language with the post-Task-5 reality (column dropped; rows store `(thumbprint, container_id, project, agent_name, registered_at, last_seen)`; the displayed `AgentFullName` is reconstructed on demand from project + agent_name).
   - "Imports" section — replace `CanonicalAgentCN`, `AgentCanonicalFromCert` with `AgentFullName`, `AgentFullNameFromCert`.
   - Note that `Entry.AgentName` / `Entry.Project` are typed (`auth.AgentName` / `auth.ProjectSlug`) — the post-Task-6 invariant guarantee.

3. **`internal/cmd/container/shared/CLAUDE.md:57`:** Strike "compose the cert's canonical CN" framing; cert CN is now `ContainerClawkerd` literal and AgentFullName lives in the URI SAN. Reference `auth.AgentFullName` not `auth.CanonicalAgentCN`.

4. **`internal/controlplane/CLAUDE.md`:** Step 8 of startup describes IdentityInterceptor wiring. Update to reflect new constructor signature (no Registry/optedOut params; ContainerByPeerIP dep instead).

5. **`.claude/docs/KEY-CONCEPTS.md`:** sweep entries for `canonical_cn` column, `Lookup(thumbprint, cn)`, `CanonicalAgentCN`, pre-computed canonical column writes — these are post-Task-4/5 dead references.

6. **Final test run:**
   ```bash
   make test
   go test ./internal/auth/... ./internal/controlplane/... ./internal/docker/... ./internal/cmd/container/... -v
   ```
   All pass. Build cleanly:
   ```bash
   go build ./...
   ```

7. **CLI doc regen** (if any user-visible behavior changed — unlikely for this refactor):
   ```bash
   go run ./cmd/gen-docs --doc-path docs --markdown --website
   ```

### Acceptance Criteria

```bash
make test
go build ./...
grep -rn "canonical" --include="*.md" internal/auth/ internal/controlplane/ internal/cmd/container/shared/
```

Final doc grep shows no stale "canonical" identity references.

### Wrap Up

1. Update Progress Tracker: Task 8 → `complete`. **Initiative complete.**
2. Append final key learnings
3. Run review subagents one last time across the full branch diff vs `main`; fix findings
4. Commit: `docs(agent): update CLAUDE.md for universal trust middleware`
5. Inform the user the initiative is complete. PR can be opened from `fix/invalid-name-len` → `main` with all 10 commits (existing `94ec999a` + `0dce04cb` goose + 8 new from Tasks 1-8).

> **Initiative complete.** No further handoff prompt.

---

## Deferred / Out-of-scope

Items surfaced in PR review but deferred to follow-up issues (not addressed by this initiative):

- `sanTailFromCert` third-state return (`agent_cert.go:129`) — distinguish malformed-empty SAN from absent SAN. File issue.
- `sethvargo/go-retry` and other transitive deps from goose: track license noise in NOTICE — already handled by pre-commit hook regen.
- Volume-name headroom regression beyond what Task 7 covers (e.g., test against future longer purpose suffixes added to `internal/docker/names.go`) — keep `TestContainerName_HeadroomForMaxFields` updated as suffixes change.
- Per-agent Session teardown on registry evict (noted in `internal/controlplane/CLAUDE.md` known limitations) — orthogonal to identity trust.
- **`validateEntry` panic family on CP serve path** — pre-existing tech debt surfaced by Task 6 silent-failure-hunter. Reachable from `register_handler.go::Register` post-`SetReady`. Today unreachable in practice (sole production caller passes middleware-resolved typed values). FIXME comment landed in Task 6; future cleanup should convert to error returns. Out of Task 6 scope (rename + typed fields, not error-handling rewrite).
- **`LookupByContainerID` malformed-row DOS path** — sqlite scanEntry now returns a validation error on hand-edited / corrupted rows; `Register` handler maps to `codes.Internal` and the affected container is stuck until manual eviction. Fix: typed `ErrMalformedEntry` sentinel + handler evicts-and-retries. Surfaced by Task 6 silent-failure-hunter, deferred — out of rename scope.
- **`auth.AgentName.Less` / `ProjectSlug.Less` typed comparators** — three sort sites (`registry.go`, `registry_sqlite.go`, formerly `server.go` until deleted in Task 6) still use `.String() < .String()`. Nice-to-have refactor; trade-off was small win for moderate API surface expansion. Surfaced by Task 6 type-design-analyzer + code-simplifier.
- **`sortEntries` extracted helper** — `registry.go` (in-memory, test-only) and `registry_sqlite.go` (production) have byte-identical sort bodies. Worth de-duplicating into one unexported helper. Surfaced by Task 6 code-simplifier.
- **`Snapshot()` swallows sqlite query failure as nil result** — `registry_sqlite.go::Snapshot()` logs and returns `nil, nil` when `db.Query` fails, and continues with whatever it accumulated when `rows.Err()` fires. `reapOrphans`, `ListAgents`, and worldview consume the truncated/empty list as authoritative — a transient sqlite error during startup reap could mask real rows. Cross-task issue introduced before this initiative but surfaced by Task 8 silent-failure-hunter (the Task 6 scanEntry-skip plumbing feeds the same path). Fix: change `Snapshot()` signature to `([]Entry, error)`, propagate to callers, abort reapOrphans on error rather than evicting-on-empty. Substantial — out of doc-sweep scope.
- **Migration 00002 Down-path silently back-fills empty `canonical_cn`** — `migrations/00002_drop_canonical_cn.sql` Down adds `canonical_cn TEXT NOT NULL DEFAULT ''`. If `goose down` is ever run, pre-existing rows get an empty `canonical_cn` and no warning surfaces. Acceptable for alpha rollback but should either drop the Down half entirely (matching the pre-goose policy in `migrateFromPreGoose` that already treats registry state as regenerable) or warn on startup when the daemon detects post-up empty rows. Surfaced by Task 8 silent-failure-hunter.
