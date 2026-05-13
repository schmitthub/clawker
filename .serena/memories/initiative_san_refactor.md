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
| Task 3: Refactor `Register` handler — drop redundant checks, use middleware-resolved labels | `pending` | — |
| Task 4: Drop `Registry.Lookup`, simplify Registry surface, rework `Dialer.classifyRegistry` | `pending` | — |
| Task 5: Migration `00002_drop_canonical_cn.sql` | `pending` | — |
| Task 6: Rename `Canonical*` → `AgentFullName*` everywhere (auth + agent + sweeps) | `pending` | — |
| Task 7: Update existing tests + add missing tests | `pending` | — |
| Task 8: Update CLAUDE.md docs + final test run | `pending` | — |

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
- **Task 2 refinements (review-driven, deviate from spec):**
  - `WithResolvedContainer` does NOT panic on empty `ContainerID` — that violates the root CLAUDE.md CP no-panic-on-serving-path rule. Instead: zero-value `ResolvedContainer` is silently dropped (ctx returned unchanged), and `ResolvedContainerFromContext` returns `ok=false` for both absent and empty-ID cases. The "silent identity vacuum" floor moves to the read side. Test pins this contract: `TestWithResolvedContainer_EmptyContainerIDIsNoOp`.
  - Empty cert SAN gets an **explicit** reject stage (2a) with its own `event=agent_identity_no_agent_san` log — rather than relying on the stage-3 `subtle.ConstantTimeCompare` natural-fail on length mismatch. Explicit check gives operators better diagnostic fidelity AND short-circuits the Docker round-trip when the cert is malformed.
  - `peerIdentity.Raw` field removed (was unused — speculation-based YAGNI). `peerIdentityFromContext` delegates to `peerLeafFromContext` instead of duplicating the cert extraction, dropping ~10 lines of duplication.
  - `remoteAddrToNetip` moved from `register_handler.go` to `handler.go` (single source of truth in the same package). Task 3 will sweep `peerLeafAndIP` to share `peerIdentityFromContext`'s extraction path.
  - Test file dropped two linter-replaceable tests (nil-peer-lookup constructor guard, absent-ctx-key returns-false) and merged the two stage-2 resolver-error tests into a 3-row table-driven test that also covers the generic-daemon-error default branch.
  - Five distinct `event=` log fields per reject stage for operator triage: `agent_identity_peer_auth_missing`, `agent_identity_cn_mismatch`, `agent_identity_no_agent_san`, `agent_identity_peer_lookup_no_match` / `_invalid_labels` / `_error`, `agent_identity_san_label_mismatch`. Wire envelope stays uniform `"registration rejected"` regardless.

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
| `internal/auth/agent_cert.go` | `MintAgentCert`, `BuildAgentSAN`, `AgentCanonicalFromCert` (→ rename `AgentFullNameFromCert`), `CanonicalAgentCN` (→ rename `AgentFullName`) |
| `internal/auth/identity.go` | `ProjectSlug`, `AgentName`, `shortNameMax`, `canonicalPrefix` (rename optional) |
| `internal/controlplane/agent/identity_interceptor.go` | The thing being redesigned |
| `internal/controlplane/agent/handler.go` | `peerIdentity`, `peerIdentityFromContext` — needs the new resolved-container ctx helper |
| `internal/controlplane/agent/register_handler.go` | `Handler.Register` — drops cert↔request compare, drops `labelsMatchRequest` |
| `internal/controlplane/agent/registry.go` + `registry_sqlite.go` | Registry interface; drop `Lookup`, keep `LookupByContainerID`, `Add`, `EvictByContainerID`, `Snapshot` |
| `internal/controlplane/agent/dialer.go` | `classifyRegistry` reworked to use `LookupByContainerID` + thumbprint compare |
| `internal/controlplane/agent/start.go` | `Start(ctx, StartDeps)` — needs new dep: `ContainerByPeerIP` resolver |
| `internal/controlplane/agent/migrations/` | Goose migrations; add 00002 to drop `canonical_cn` |
| `cmd/clawker-cp/main.go` | Wires `agent.Start` — pass new `ContainerByPeerIP` constructed from moby client |
| `internal/auth/CLAUDE.md`, `internal/controlplane/agent/CLAUDE.md`, `internal/cmd/container/shared/CLAUDE.md` | Doc updates |

### Design Patterns

- **Interface seam for Docker:** Mirror existing `ContainerInspector` pattern in `register_handler.go:42-65`. Add `ContainerByPeerIP` interface with method `LookupByIP(ctx, ip netip.Addr) (containerID string, labels map[string]string, err error)`. Production impl wraps moby `ContainerList` with filters `label=dev.clawker.purpose=agent` + network endpoint scan. Test impl is a hand-rolled fake (no moq for one-method interface).
- **Ctx value plumbing:** Follow `WithEntry` / `EntryFromContext` pattern in `identity_interceptor.go:53-82`. Add `WithResolvedContainer` / `ResolvedContainerFromContext` for the peer-IP-resolved (containerID, project, agent_name) triple. Use unexported struct-typed key.
- **Drop `optedOut` map entirely.** With Lookup gone, the universal middleware applies to every method without exception. Register still self-authenticates via per-handler container_id-SAN cross-check, but the trust gate is upstream.
- **Constant-time compares** for every identity equality check (`crypto/subtle.ConstantTimeCompare`). Defense-in-depth against future timing channels.
- **Structured logs** with `event=<kebab_case>` and `peer_ip`, `expected_<thing>`, `cert_<thing>` fields.
- **Generic rejection envelope:** All `PermissionDenied` returns use `"registration rejected"` (or `"register: identity check failed"` in Register) — attackers must not learn which check failed; the structured log carries the classification.

### Rules

- Read root `CLAUDE.md`, `.claude/rules/` files (code-style, testing, dependency-placement), and package `CLAUDE.md` files before starting any task
- Use Serena tools for code exploration — `find_symbol`, `find_referencing_symbols`, `get_symbols_overview` — read symbol bodies only when needed
- All new code must compile; relevant tests must pass (`go test ./internal/auth/... ./internal/controlplane/agent/... ./internal/docker/... ./internal/cmd/container/shared/...`)
- NEVER run `go test ./...` from inside a clawker container — the e2e suite tears down the host CP. Run targeted package tests.
- Follow existing test patterns: `iostreams.Test()`, `configmocks`, moq-generated mocks, table-driven tests with `assert`/`require` from testify
- CP code must NOT panic or `log.Fatal` after `SetReady` — return errors, degrade subsystems with `event=<subsystem>_unavailable` structured logs
- Output streams: data → `ios.Out`, warnings/errors → `ios.ErrOut`. Logger is FILE LOGGING ONLY — never user-visible output.
- No "canonical" in new identifier names — use `AgentFullName`. The sqlite column `canonical_cn` is migrated away in Task 5.

---

## Task 1: Build `ContainerByPeerIP` resolver + interface seam

**Creates/modifies:**
- `internal/controlplane/agent/peer_lookup.go` (new) — `ContainerByPeerIP` interface + `ResolvedContainer` struct
- `internal/controlplane/agent/peer_lookup_moby.go` (new) — moby-backed implementation
- `internal/controlplane/agent/peer_lookup_test.go` (new) — fake + table-driven tests
- `internal/controlplane/agent/start.go` — add `ContainerByPeerIP` to `StartDeps`
- `cmd/clawker-cp/main.go` — construct moby-backed resolver and pass to `agent.Start`

**Depends on:** Task 0 (goose adoption — complete)

### Implementation Phase

1. Define `ResolvedContainer` struct: `{ ContainerID string; Project string; AgentName string }` — the truth source for downstream consumers.
2. Define `ContainerByPeerIP` interface:
   ```go
   type ContainerByPeerIP interface {
       LookupByIP(ctx context.Context, ip netip.Addr) (ResolvedContainer, error)
   }
   ```
   Return a sentinel `ErrNoContainerForPeerIP` when no `purpose=agent` container on clawker-net has the given endpoint IP.
3. Moby-backed impl: `ContainerList` with filter `label=dev.clawker.purpose=agent`, iterate results, for each call `ContainerInspect` and check `NetworkSettings.Networks[consts.Network].IPAddress.Unmap() == ip.Unmap()`. Return the first match's labels.
4. Add a 5s timeout on the docker calls (mirrors `register_handler.go:32` `inspectTimeout`).
5. Wire into `agent.StartDeps` as a new required field `PeerLookup ContainerByPeerIP`.
6. In `cmd/clawker-cp/main.go`, construct the moby-backed resolver from the existing `*docker.Client` (whail-wrapped) and pass it to `agent.Start`.

### Acceptance Criteria

```bash
go build ./internal/controlplane/agent/...
go build ./cmd/clawker-cp
go test -run TestContainerByPeerIP ./internal/controlplane/agent/... -v
```

Tests cover:
- Match on first container with matching IP
- No match → `ErrNoContainerForPeerIP`
- Multiple `purpose=agent` containers, only one matches → correct one returned
- Container without clawker-net endpoint → skipped
- Docker error → wrapped and returned (no panic)

### Wrap Up

1. Update Progress Tracker: Task 1 → `complete`
2. Append key learnings
3. Run `pr-review-toolkit:code-reviewer`, `pr-review-toolkit:silent-failure-hunter`, `test-hunter`, `pr-review-toolkit:code-simplifier`, `pr-review-toolkit:comment-analyzer`, `pr-review-toolkit:type-design-analyzer` subagents; fix all findings
4. Commit: `feat(agent): add ContainerByPeerIP resolver for trust-anchor lookup`
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the SAN-refactor initiative. Read the Serena memory `initiative_san_refactor` — Task 1 is complete. Begin Task 2: Redesign IdentityInterceptor as universal peer-IP→Docker→label middleware."

---

## Task 2: Redesign `IdentityInterceptor` as universal peer-IP→Docker→label middleware

**Creates/modifies:**
- `internal/controlplane/agent/identity_interceptor.go` — full rewrite of `resolve` closure
- `internal/controlplane/agent/handler.go` — add `WithResolvedContainer` / `ResolvedContainerFromContext`
- `internal/controlplane/agent/identity_interceptor_test.go` — rewrite tests for new flow

**Depends on:** Task 1

### Implementation Phase

1. Add `WithResolvedContainer(ctx, ResolvedContainer)` and `ResolvedContainerFromContext(ctx) (ResolvedContainer, bool)` to `handler.go`. Unexported ctx key struct. Panic on nil; ok=false on typed-nil.
2. Update `IdentityInterceptor` signature: drop `Registry` and `optedOut` parameters; add `PeerLookup ContainerByPeerIP`:
   ```go
   func IdentityInterceptor(peerLookup ContainerByPeerIP, log *logger.Logger) (UnaryServerInterceptor, StreamServerInterceptor)
   ```
3. Delete `IdentityOptedOutMethods()` entirely. Delete `WithEntry`/`EntryFromContext` (Registry no longer attached).
4. New `resolve` flow:
   ```go
   resolve := func(ctx, method) (ctx, error) {
       pid, err := peerIdentityFromContext(ctx)
       if err != nil { reject }
       // 1. Universal CN pin (constant-time, log peer_cn + expected_cn + agent_full_name).
       if subtle.ConstantTimeCompare([]byte(pid.CommonName), []byte(consts.ContainerClawkerd)) != 1 { reject }
       // 2. Peer IP → Docker → labels.
       resolved, err := peerLookup.LookupByIP(ctx, peerAddr(ctx))
       if err != nil { reject + structured log }
       // 3. Cross-check cert SAN AgentFullName against label-derived AgentFullName.
       labelFullName := auth.AgentFullName(auth.MustProjectSlug(resolved.Project), auth.MustAgentName(resolved.AgentName))
       if subtle.ConstantTimeCompare([]byte(pid.AgentFullName), []byte(labelFullName)) != 1 { reject + structured log }
       return WithResolvedContainer(ctx, resolved), nil
   }
   ```
   NOTE: `auth.AgentFullName` is the renamed `CanonicalAgentCN` (rename happens in Task 6 — for Task 2, keep calling `auth.CanonicalAgentCN` and let Task 6 sweep). Don't use `Must*` constructors on Docker labels — labels are user-supplied at container create time and could be malformed in pathological cases. Use `auth.NewProjectSlug` / `auth.NewAgentName` and reject on error.
5. Rewrite `identity_interceptor_test.go`:
   - Drop tests using `Registry.Lookup`
   - Add: wrong-CN → rejected; missing labels → rejected; cert SAN ≠ label AgentFullName → rejected; happy path → ctx carries `ResolvedContainer`; **Register opt-out variant gone** — Register hits the same middleware now
   - Fake `ContainerByPeerIP` in test (hand-rolled, not moq — one method)
6. Update `cmd/clawker-cp/main.go` wiring for new `IdentityInterceptor` signature.

### Acceptance Criteria

```bash
go build ./internal/controlplane/agent/...
go build ./cmd/clawker-cp
go test -run TestIdentityInterceptor ./internal/controlplane/agent/... -v
```

All identity-interceptor tests pass. Register no longer opts out of the trust check (it's still opt-out of registry lookup, but registry lookup is gone — Task 4).

### Wrap Up

1. Update Progress Tracker: Task 2 → `complete`
2. Append key learnings
3. Run review subagents; fix findings
4. Commit: `refactor(agent): IdentityInterceptor as universal peer-IP-grounded middleware`
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the SAN-refactor initiative. Read Serena memory `initiative_san_refactor` — Tasks 1-2 complete. Begin Task 3: Refactor Register handler to use middleware-resolved labels."

---

## Task 3: Refactor `Register` handler — drop redundant checks, use middleware-resolved labels

**Creates/modifies:**
- `internal/controlplane/agent/register_handler.go` — drop cert↔request compare, drop `labelsMatchRequest`, use `ResolvedContainerFromContext`
- `internal/controlplane/agent/register_handler_test.go` — update tests for new flow

**Depends on:** Task 2

### Implementation Phase

1. Read `ResolvedContainer` from ctx via `ResolvedContainerFromContext` — set by the middleware. If missing → 500 Internal (wiring bug — middleware MUST have run); structured log `event=agent_register_no_resolved_container`.
2. Drop:
   - `peerLeafAndIP` no longer needs to return `peerIP` (middleware already used it). Keep returning `leaf` for thumbprint + container_id SAN extraction.
   - `auth.AgentCanonicalFromCert` call + `ok` check (middleware verified non-empty + matching).
   - `expectedCanonical` computation + constant-time compare against request — middleware compared cert SAN to labels; trusting labels is the new floor.
   - `labelsMatchRequest` — labels were the input to middleware, no need to recheck. Delete the function.
   - `peerIPMatchesContainer` — middleware resolved by peer IP, so by construction the IP matches. Delete the function.
3. Validate request fields (`auth.NewProjectSlug`, `auth.NewAgentName`) as inputs. Cross-check against resolved labels (cheap sanity): if `req.Project != resolved.Project` or `req.AgentName != resolved.AgentName` → reject with `event=agent_register_request_label_mismatch` (the client is lying about its own identity; cert+labels+request must all align).
4. Read `container_id` from cert URI SAN as before. Cross-check against `resolved.ContainerID` (cert SAN claim vs Docker-resolved truth): mismatch → reject with `event=agent_register_container_id_mismatch`.
5. Idempotency check via `Registry.LookupByContainerID` stays.
6. Registry write: use values FROM `resolved` (label-derived), NOT from request. Request is the client's claim; labels are authoritative.
7. Update tests: drop `labelsMatchRequest_*` tests, drop `peerIPMismatch_*` tests, add `request_label_mismatch_*` and `container_id_mismatch_*`.

### Acceptance Criteria

```bash
go build ./internal/controlplane/agent/...
go test -run TestRegister ./internal/controlplane/agent/... -v
```

All Register tests pass.

### Wrap Up

1. Update Progress Tracker: Task 3 → `complete`
2. Append key learnings
3. Run review subagents; fix findings
4. Commit: `refactor(agent): Register handler consumes middleware-resolved identity`
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the SAN-refactor initiative. Read Serena memory `initiative_san_refactor` — Tasks 1-3 complete. Begin Task 4: Drop Registry.Lookup, simplify surface, rework dialer."

---

## Task 4: Drop `Registry.Lookup`, simplify surface, rework `Dialer.classifyRegistry`

**Creates/modifies:**
- `internal/controlplane/agent/registry.go` — drop `Lookup` from the interface
- `internal/controlplane/agent/registry_sqlite.go` — drop `Lookup` method + `canonical_cn` references in Add/scan; `LookupByContainerID` stays
- `internal/controlplane/agent/registry_mock_test.go` — regen moq mock
- `internal/controlplane/agent/dialer.go` — `classifyRegistry` uses `LookupByContainerID` + thumbprint compare; outcomes refit
- `internal/controlplane/agent/dialer_test.go` — update outcome assertions

**Depends on:** Task 3

### Implementation Phase

1. Drop `Lookup(thumbprint, agentFullName)` from `Registry` interface. Drop the sqlite impl + the in-memory `NewRegistry` impl's method.
2. Drop the `canonical` field write/scan in `Add`/`scanEntry`. Update `selectEntryCols` to remove `canonical_cn`. **Note:** the column itself still exists in the DB at this point — Task 5 drops it via migration. Code stops writing/reading it now so the column is unused, and the Task-5 migration is safe.
3. Regenerate `registry_mock_test.go` via `cd internal/controlplane/agent && go generate ./...`. Strip the self-import (see existing file's post-edit shape).
4. Rework `Dialer.classifyRegistry`:
   - New flow: `LookupByContainerID(containerID)` → if Not Found = `outcomeRegistryMiss`; if found and thumbprint matches = `outcomeRegistryMatch`; if found and thumbprint differs = `outcomeRegistryThumbprintMismatch` (renamed from current).
   - Drop the agent-full-name CN comparison entirely — that lived in `Lookup` and is no longer the dialer's job. The dialer is OUTBOUND CP→clawkerd; trust on the agent side comes from clawkerd's listener pinning CP's CN.
   - Keep `outcomeRegistryCNMismatch` enum value but rename to `outcomeRegistryThumbprintMismatch` — the substantive check is now thumbprint. (Per user: `UntrustedReasonCNMismatch` enum stays as-is because the CN-pin failure still references the binary identity CN. Be precise: the dialer's enum becomes `UntrustedReasonThumbprintMismatch` because that's what changed.)
5. Update `events_session.go` `SessionConnected` event: keep `PeerAgentFullName` field (still valuable diagnostic); keep `PeerThumbprint`.
6. Update `dialer_test.go` to assert new outcomes.

### Acceptance Criteria

```bash
go build ./...
go test -run "TestDialer|TestRegistry" ./internal/controlplane/agent/... -v
```

All dialer + registry tests pass.

### Wrap Up

1. Update Progress Tracker: Task 4 → `complete`
2. Append key learnings
3. Run review subagents; fix findings
4. Commit: `refactor(agent): drop Registry.Lookup; dialer classifies by thumbprint`
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the SAN-refactor initiative. Read Serena memory `initiative_san_refactor` — Tasks 1-4 complete. Begin Task 5: Migration 00002 to drop canonical_cn."

---

## Task 5: Migration `00002_drop_canonical_cn.sql`

**Creates/modifies:**
- `internal/controlplane/agent/migrations/00002_drop_canonical_cn.sql` (new)

**Depends on:** Task 4 (code stops reading/writing `canonical_cn` before migration drops it — guarantees no broken queries during migration window).

### Implementation Phase

1. Verify modernc.org/sqlite version supports `ALTER TABLE DROP COLUMN` (SQLite 3.35+, released March 2021 — modernc tracks recent).
2. Write `00002_drop_canonical_cn.sql`:
   ```sql
   -- +goose Up
   ALTER TABLE agents DROP COLUMN canonical_cn;
   
   -- +goose Down
   ALTER TABLE agents ADD COLUMN canonical_cn TEXT NOT NULL DEFAULT '';
   ```
   The down migration is best-effort — the original column was `NOT NULL` without a default. Restoring requires a default; existing rows that survived an up→down round-trip won't have meaningful canonical_cn values. Acceptable for alpha.
3. Verify `idx_name_project ON agents(project, agent_name)` is unaffected by the column drop (it doesn't reference canonical_cn).

### Acceptance Criteria

```bash
go test ./internal/controlplane/agent/... -v
```

All tests pass including a new test verifying the column is gone after migration applies. Specifically:

- `TestMigration_002_DropsCanonicalCN`: Open a DB, apply migrations, query `PRAGMA table_info(agents)`, assert no `canonical_cn` column.

### Wrap Up

1. Update Progress Tracker: Task 5 → `complete`
2. Append key learnings
3. Run review subagents; fix findings
4. Commit: `chore(agent): migration 00002 drop canonical_cn column`
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the SAN-refactor initiative. Read Serena memory `initiative_san_refactor` — Tasks 1-5 complete. Begin Task 6: Rename Canonical* → AgentFullName* everywhere."

---

## Task 6: Rename `Canonical*` → `AgentFullName*` everywhere

**Creates/modifies:** All packages with `canonical` references in identity contexts. Memory `feedback_no_canonical_naming` documents the rule.

**Depends on:** Task 5

### Implementation Phase

Mechanical rename, preserving semantics. Use Serena `rename_symbol` where possible; fall back to Edit for log fields/event names/comments.

1. **`internal/auth` package:**
   - `auth.CanonicalAgentCN` → `auth.AgentFullName` (function)
   - `auth.AgentCanonicalFromCert` → `auth.AgentFullNameFromCert`
   - Comments mentioning "the canonical" / "canonical agent identity" → "the agent full name" / "the AgentFullName"
   - `canonicalPrefix` constant in `identity.go:135` may stay if it's purely a "looks-like-AgentFullName" parse-rejection guard (not identity naming). If kept, rename to `agentFullNamePrefix` for consistency.

2. **`internal/controlplane/agent` package:**
   - `canonicalCNFromStrings` → `agentFullNameFromStrings`
   - `canonicalCNFromEntry` → `agentFullNameFromEntry`
   - Log fields: `cert_canonical` → `cert_agent_full_name`, `expected_canonical` → `expected_agent_full_name`, `peer_cn` (in CN-pin rejection) stays — that IS the CN being checked
   - Event names: `agent_register_canonical_mismatch` → `agent_register_agent_full_name_mismatch`
   - Variables: `certCanonical` → `certAgentFullName`, `expectedCanonical` → `expectedAgentFullName`, `expectedCN` (in `dialer.go classifyRegistry`) → `expectedAgentFullName`
   - Comments throughout

3. **Sweep stale "canonical CN" descriptions outside diff scope** (these refer to OLD design — the cert no longer carries the canonical in CN):
   - `internal/cmd/container/shared/agent_bootstrap.go:64-68, 121`
   - `internal/cmd/container/shared/container_start.go:30-32, 46`
   - `internal/cmd/container/shared/CLAUDE.md:57`
   - `internal/controlplane/agent/registry.go:48-49, 65, 100, 148-152, 238`
   - `internal/controlplane/agent/registry_sqlite.go:26-27` (now obsolete with Task 5 — column gone)
   - `internal/docker/env.go:117`

4. **Skip:**
   - `outcomeRegistryCNMismatch` / `UntrustedReasonCNMismatch` — per user call, these stay (semantic continuity with old check). Task 4 already renamed dialer-side to `Thumbprint*`.
   - Generic English "canonical attach-then-start pattern" (`internal/cmd/container/start/start.go:169`), "canonical container ID is exactly that charset" (`internal/auth/agent_cert.go:69`) — these are non-identity uses of "canonical" and are fine.
   - sqlite goose-recorded migration `00001_init.sql` still references `canonical_cn` because that was the schema at that version. Don't rewrite history.

### Acceptance Criteria

```bash
go build ./...
go test ./internal/auth/... ./internal/controlplane/agent/... ./internal/cmd/container/shared/... ./internal/docker/... -v
grep -rn "canonical" --include="*.go" internal/auth/ internal/controlplane/agent/ internal/cmd/container/shared/
```

Final grep should show only:
- `canonicalPrefix` if you chose to keep it under that name
- `outcomeRegistryCNMismatch`-related references (intentionally kept)
- Non-identity uses of "canonical" (generic English)

### Wrap Up

1. Update Progress Tracker: Task 6 → `complete`
2. Append key learnings
3. Run review subagents; fix findings
4. Commit: `refactor: rename Canonical* → AgentFullName* in identity contexts`
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the SAN-refactor initiative. Read Serena memory `initiative_san_refactor` — Tasks 1-6 complete. Begin Task 7: Update tests + add missing tests."

---

## Task 7: Update existing tests + add missing tests

**Creates/modifies:**
- All `_test.go` files affected by the rename in Task 6
- Add new tests identified in review

**Depends on:** Task 6

### Implementation Phase

1. **Mechanical rename in tests:** `gotCN` → `gotAgentFullName` in `identity_interceptor_test.go:111`, similar patterns elsewhere.

2. **Add new tests:**
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

3. **Drop tests for removed functionality:**
   - Tests for `Registry.Lookup` (now gone)
   - Tests for `labelsMatchRequest` / `peerIPMatchesContainer` if those functions were deleted in Task 3
   - Tests for the `optedOut` map / `IdentityOptedOutMethods`

4. **Run `test-hunter` agent on the test diff** to catch any wasteful tests slipped in.

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

**Depends on:** Task 7

### Implementation Phase

1. **`internal/auth/CLAUDE.md`:** Update API table — `CanonicalAgentCN` → `AgentFullName`, `AgentCanonicalFromCert` → `AgentFullNameFromCert`. Update prose describing CN-as-canonical to reflect CN=`ContainerClawkerd` literal + canonical-as-URI-SAN.

2. **`internal/controlplane/agent/CLAUDE.md`:**
   - "Identity contract" section — replace "CN composed from (project, agent_name) is stored pre-computed" with the new design (peer-IP→Docker→labels is the trust anchor; registry stores thumbprint + container_id + agent_full_name fields for bookkeeping/display)
   - "Register flow" section — strike step about cert↔request compare; describe the new universal-middleware-then-handler split
   - "Trust outcomes via overseer events" table — `CNMismatch` row → `ThumbprintMismatch` (per Task 4 rename), describe SAN-sourced semantics
   - "Method scopes + interceptor opt-out" section — delete the opt-out subsection; the interceptor has no opt-outs now. Register is no longer exempt from the trust check (only the Lookup half is gone for everyone).
   - Files table — remove stale entries; add `peer_lookup.go`

3. **`internal/cmd/container/shared/CLAUDE.md:57`:** Strike "compose the cert's canonical CN" framing; cert CN is now `ContainerClawkerd` literal and AgentFullName lives in the URI SAN.

4. **`internal/controlplane/CLAUDE.md`:** Step 8 of startup describes IdentityInterceptor wiring. Update to reflect new constructor signature (no Registry/optedOut params; ContainerByPeerIP dep instead).

5. **Final test run:**
   ```bash
   make test
   go test ./internal/auth/... ./internal/controlplane/... ./internal/docker/... ./internal/cmd/container/... -v
   ```
   All pass. Build cleanly:
   ```bash
   go build ./...
   ```

6. **CLI doc regen** (if any user-visible behavior changed — unlikely for this refactor):
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
5. Inform the user the initiative is complete. PR can be opened from `fix/invalid-name-len` → `main` with all 9 commits (existing `94ec999a` + `0dce04cb` goose + 7 new from Tasks 1-8).

> **Initiative complete.** No further handoff prompt.

---

## Deferred / Out-of-scope

Items surfaced in PR review but deferred to follow-up issues (not addressed by this initiative):

- `sanTailFromCert` third-state return (`agent_cert.go:129`) — distinguish malformed-empty SAN from absent SAN. File issue.
- `sethvargo/go-retry` and other transitive deps from goose: track license noise in NOTICE — already handled by pre-commit hook regen.
- Volume-name headroom regression beyond what Task 7 covers (e.g., test against future longer purpose suffixes added to `internal/docker/names.go`) — keep `TestContainerName_HeadroomForMaxFields` updated as suffixes change.
- Per-agent Session teardown on registry evict (noted in `internal/controlplane/CLAUDE.md` known limitations) — orthogonal to identity trust.
