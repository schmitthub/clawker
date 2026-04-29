# Task 01 — agentregistry foundation refactor

**Status**: pending
**Claimed by**: —
**Blocks**: 02, 03, 05
**Blocked by**: none

## Findings covered

- **S4** — `EvictByContainerID` violates documented invariant (DB delete fails → memory still evicted → reload resurrects row → permanent disk/memory drift)
- **C2** — `Lookup` panics CP on malformed row via `auth.MustProjectSlug`/`MustAgentName`
- **Y1 + Y2** — Identity stringly-typed at storage; `Lookup` reconstructs canonical CN from agent_name + project at read time
- **S5** — `RowsAffected` error swallowed in `EvictByContainerID`
- **S6** — Schema apply (`db.Exec(schemaSQL)`) is multi-statement DDL with no transactional boundary
- **S7** — Malformed `thumbprint_hex` row never deleted from disk (eliminated by no-cache, no reload path)
- **S8** — `Reap` aborts entirely on transient docker lister error
- **S17** — `Reap` evicts via `EvictByContainerID` which has no error return
- **T3** — `agentregistry/sqlite.go` (377 lines) has no SQLite-specific test file

## Decisions (from review session 2026-04-28)

1. **Drop the in-memory cache.** `sqliteRegistry.entries map[[sha256.Size]byte]Entry` is removed entirely. Every read hits sqlite. Eliminates S4 + S5 + S7 + S17 by construction (no invariant to violate, no reload to corrupt).
2. **Add `canonical_cn TEXT NOT NULL` column** to the `agents` table. Computed at `Add` time via `auth.CanonicalAgentCN(...)` from validated typed inputs. `Lookup` reads it directly and compares against the peer cert CN with `subtle.ConstantTimeCompare` — no reconstruction, no Must*. Removes the Lookup panic vector entirely (C2 + Y1 + Y2).
3. **Change `EvictByContainerID` interface signature** to return `error`. Reap aggregates failures and surfaces a count in its return value. Subsumes S17. (Note: `EvictByContainerID` is also called from `agentregistry/subscribe.go` `handleDelta` — that caller logs the error and proceeds, since it cannot retry from a delta consumer.)
4. **Wrap schema apply in `BEGIN/COMMIT` TX** (S6). DDL inside an explicit transaction.
5. **Reap retries the lister** with bounded backoff (3 attempts, 100ms → 200ms → 400ms) before giving up (S8). No /healthz integration in this PR.
6. **Add `internal/controlplane/agentregistry/sqlite_test.go`** covering: reader-mode write rejection, UNIQUE constraint on Add, malformed thumbprint_hex behavior (now: rejected by DB schema or pre-validation), concurrent CLI+CP contention via two `NewSQLiteWriter` opens, canonical_cn column round-trip, `Lookup` CN-mismatch path.

## Affected files

| File | Change |
|------|--------|
| `internal/controlplane/agentregistry/registry.go` | `Registry` interface: `EvictByContainerID(string) error`. `Entry` adds `CanonicalCN string` field (or computed at Add — see implementation note). `validateEntry` no longer touches Project/AgentName for identity purposes — they remain validated for the cross-check fields. |
| `internal/controlplane/agentregistry/sqlite.go` | Major rewrite: drop `entries` map + `mu`. Add `canonical_cn` column to schemaSQL. `Add` computes canonical_cn from typed inputs and INSERTs. `Lookup` queries by thumbprint + canonical_cn comparison. `LookupByThumbprint` / `LookupByContainerID` / `Snapshot` query sqlite directly. `EvictByContainerID` returns error. Schema apply in TX. |
| `internal/controlplane/agentregistry/registry.go` `registryImpl` | In-memory Registry impl used by tests — keep but update to match new interface (return nil error from Evict). |
| `internal/controlplane/agentregistry/reap.go` | Bounded retry on lister; aggregate Evict errors into return value (alongside count). |
| `internal/controlplane/agentregistry/subscribe.go` | `handleDelta` logs Evict error and proceeds. |
| `internal/controlplane/agentregistry/mocks/registry_mock.go` | Regenerate via `cd internal/controlplane/agentregistry && go generate ./...`. |
| `internal/controlplane/agentregistry/sqlite_test.go` | NEW — full coverage per decision #6 above. |
| `internal/controlplane/agentregistry/registry_test.go` | Update existing tests for interface change (Evict returns error). |
| `internal/controlplane/agentregistry/reap_test.go` | Add lister-retry-on-transient-error test. |
| `internal/controlplane/agentregistry/CLAUDE.md` | Rewrite "Backing store: sqlite" + "Writers" + "API" + "Schema" sections to reflect cache-less design + canonical_cn column + new interface. |
| All callers (downstream): `internal/controlplane/server.go`, `internal/controlplane/agent/handler.go`, `internal/controlplane/agent/identity_interceptor.go`, `internal/controlplane/agentdial/dialer.go`, `internal/cmd/controlplane/agents.go`, `cmd/clawker-cp/main.go` | Update call sites for new `EvictByContainerID` error return. Most can `_ = reg.EvictByContainerID(id)` if they were doing nothing with it before, but prefer logging the error. |
| `internal/cmd/container/shared/agent_bootstrap.go` | `InstallAgentBootstrap` constructs `Entry` — populate canonical_cn from `auth.CanonicalAgentCN(opts.Project, opts.Agent)` (typed inputs already in scope). |

## Implementation plan

1. **Sketch the new `Entry` shape and `Registry` interface** in `registry.go`. Decide whether `CanonicalCN` is a field on `Entry` or an internal sqlite column not exposed on `Entry`. **Recommendation: internal column.** `Entry` keeps the same shape callers see; `Lookup` uses canonical_cn internally. This avoids adding a field that callers might forget to populate.
2. **Update schemaSQL**: add `canonical_cn TEXT NOT NULL`. Add index on canonical_cn if `Lookup` benefits — measure first; the thumbprint primary key is probably sufficient discriminator.
3. **Schema migration strategy**: since we're alpha and the DB is regenerable, add a version check at open time. If existing DB lacks `canonical_cn`, log a warning, drop the table, recreate. (Or: detect via `PRAGMA table_info(agents)` and run `DROP TABLE IF EXISTS agents` then re-apply schema.) Document this in CLAUDE.md.
4. **Wrap schema apply in TX**:
   ```go
   tx, err := db.Begin()
   if err != nil { return err }
   if _, err := tx.Exec(schemaSQL); err != nil {
       _ = tx.Rollback()
       return err
   }
   return tx.Commit()
   ```
5. **Rewrite `Add`**:
   - Compute `canonical_cn` from validated typed inputs. Caller passes `auth.AgentName` + `auth.ProjectSlug` typed values OR compute inside Add from strings using `auth.NewAgentName`/`NewProjectSlug` (returning err) — **prefer passing typed values from caller** so the Must* panic path is eliminated entirely.
   - Decide: change `Entry.AgentName`/`Entry.Project` to typed (`auth.AgentName`/`auth.ProjectSlug`)? **Recommendation: keep as strings on Entry** (callers + tests + JSON tags depend on string), but require typed inputs at Add boundary by adding a typed constructor: `func (r *sqliteRegistry) AddTyped(thumbprint, containerID string, project auth.ProjectSlug, agent auth.AgentName, registeredAt time.Time) error` — internal helper that all production paths use; legacy string-only Add panics on invalid input (per existing `validateEntry` contract).
   - Actually: simpler path — keep `Entry.AgentName/Project` as strings, but require callers to have validated them. Add takes string Entry, computes canonical_cn via `auth.NewAgentName`/`NewProjectSlug` which RETURN errors (instead of Must*). Add returns the parse error. No new types on Entry.
6. **Rewrite `Lookup`**: query sqlite for the row by thumbprint, fetch `canonical_cn` column, `subtle.ConstantTimeCompare(cn, want)`. Return ErrUnknownAgent on no row OR mismatch.
7. **Rewrite `LookupByThumbprint`, `LookupByContainerID`, `Snapshot`**: direct sqlite queries.
8. **Rewrite `EvictByContainerID`**: returns error. Logs `RowsAffected` error at Warn (S5). Real DELETE error returned to caller.
9. **Drop `reload()` method entirely** — no cache, no reload. Drop `sqliteRegistry.entries` field.
10. **Reap retry**: wrap lister call in 3-attempt loop with 100/200/400ms backoff. Aggregate Evict errors; return as combined error if any.
11. **Subscribe handleDelta**: log Evict error at Warn, do not unwind.
12. **Regenerate mocks**: `cd internal/controlplane/agentregistry && go generate ./...`.
13. **Update all call sites** (see Affected files table).
14. **Write `sqlite_test.go`** with the test list from decision #6.
15. **Update `agentregistry/CLAUDE.md`** to describe the cache-less design, canonical_cn column, and new interface.
16. **Run** `make test` and verify all packages compile + agentregistry tests pass.

## Test requirements

New `sqlite_test.go` must cover at minimum:

- `TestSQLiteWriter_Add_PersistsCanonicalCN` — Add then re-open DB; canonical_cn round-trips.
- `TestSQLiteWriter_Add_RejectsDuplicateThumbprint` — UNIQUE constraint on thumbprint_hex returns error.
- `TestSQLiteWriter_Add_RejectsDuplicateContainerID` — UNIQUE constraint on container_id returns error.
- `TestSQLiteReader_RejectsWrites` — `NewSQLiteReader.Add` fails with sqlite "attempt to write a readonly database".
- `TestSQLiteRegistry_Lookup_CNMismatch_ReturnsUnknownAgent` — known thumbprint, wrong CN → ErrUnknownAgent.
- `TestSQLiteRegistry_Lookup_KnownThumbprintAndCN_ReturnsEntry` — happy path.
- `TestSQLiteRegistry_EvictByContainerID_ReturnsErrOnDBFailure` — close DB then call Evict → error returned.
- `TestSQLiteRegistry_ConcurrentWriters` — two `NewSQLiteWriter` opens against same path; concurrent Add ops do not corrupt; busy_timeout absorbs contention.
- `TestSQLiteRegistry_SchemaMigration_FromOldDB` — open a DB with the OLD schema (no canonical_cn); verify the registry detects the drift, drops the table, re-applies schema, and continues. Log line must mention "schema mismatch".
- `TestSQLiteWriter_Add_InvalidProject_ReturnsErr` — Add with malformed project string returns parse error (no panic).
- `TestSQLiteWriter_Add_InvalidAgent_ReturnsErr` — Add with malformed agent name returns parse error (no panic).

Update `reap_test.go`:

- `TestReap_RetriesOnTransientListerError` — lister fails twice then succeeds; Reap completes.
- `TestReap_GivesUpAfterMaxRetries` — lister fails 4 times; Reap returns error.

Update `registry_test.go` for interface change (Evict returns nil error from in-memory impl).

## Verification

```bash
# Compile + lint
go build ./...
go vet ./internal/controlplane/agentregistry/...

# Tests
go test ./internal/controlplane/agentregistry/... -race -v

# Mock regen check (must produce no diff)
cd internal/controlplane/agentregistry && go generate ./... && cd -
git diff --exit-code internal/controlplane/agentregistry/mocks/

# Downstream callers compile
go build ./internal/controlplane/... ./internal/cmd/... ./cmd/...

# Full unit suite (excludes test/e2e + test/whail per project rule)
make test
```

## Dependencies

None. This is the foundation task.

## Risks / gotchas

- **Hot-path performance**: dropping the cache means every AgentPort RPC's `IdentityInterceptor` does a sqlite query. Modernc/sqlite + WAL on local fs is fast (<1ms typical) but not free. If the production agent count grows, consider reintroducing a read-through cache with explicit invalidation. For now, the architectural simplicity wins.
- **Schema migration**: existing DBs from prior commits will lack canonical_cn. The migration path (drop+recreate) is destructive — users who care about persisted registry state lose it on upgrade. This is acceptable for alpha; document loudly in CLAUDE.md and changelog.
- **Mock regen**: moq is sensitive to interface changes. If the regenerated mock differs from a hand-edit, the original was hand-edited (forbidden per project rule). Discard local hand-edits and regen.
- **Caller error handling**: many existing call sites do `reg.EvictByContainerID(id)` and discard. Decide per call site whether to log or propagate. Document the choice in the task's resolution.
- **`agentdial.publishConnected` etc.** call agentregistry methods — those changes overlap with Task #5. Coordinate: complete Task #1 fully (interface change committed), then Task #5 picks up the new interface.
- **Don't accidentally introduce a `Registry` field type rename** — breaks the existing `RegistryMock` consumers in tests. Keep method signatures exact except for the documented Evict change.

## Reference reading

- `internal/controlplane/agentregistry/CLAUDE.md` — current package doc (will be rewritten by this task)
- `internal/controlplane/agent/CLAUDE.md` — Connect handler identity binding chain (consumes Lookup)
- `internal/auth/identity.go` — `auth.CanonicalAgentCN`, `auth.NewAgentName`, `auth.NewProjectSlug` (the err-returning constructors that replace Must*)
- `.claude/rules/storage-schema.md` — storage layer patterns (canonical_cn column adds nothing storage-specific but the conventions apply)
- Original sqlite design notes in `.serena/memories/cp-initiative-cp-restart-resilience.md` if present

## Resolution

- Commit SHA: (filled by commit)
- Notes:
  - Dropped in-memory cache from `sqliteRegistry`; `Lookup`/`Snapshot`/`LookupByThumbprint`/`LookupByContainerID` now query sqlite directly (S4, S5, S7, S17 collapse by construction).
  - Added `canonical_cn TEXT NOT NULL` column to the `agents` table; `Add` composes it via `auth.NewProjectSlug` + `auth.NewAgentName` (err-returning typed constructors). `Lookup` compares against this column with `subtle.ConstantTimeCompare`. Removes the C2 / Y1 / Y2 panic vector at the call site.
  - `EvictByContainerID` now returns `error`. Updated callers: `reap.go` aggregates per-row evict errors via `errors.Join`; `subscribe.go` `handleDelta` logs at Warn and proceeds (it cannot retry from a delta consumer); `internal/cmd/container/remove/remove.go` logs at Debug because container removal already succeeded by then.
  - Schema apply wrapped in `BEGIN/COMMIT` transaction (S6). Pre-Task-01 schemas (no canonical_cn) are detected via `PRAGMA table_info(agents)` and dropped+recreated on writer open with a Warn log line (alpha policy: re-Register is cheaper than partial migration).
  - Reap retries the lister with bounded exponential backoff (3 attempts: 100ms → 200ms → 400ms) before giving up (S8). Returns evicted count + joined evict errors.
  - In-memory `registryImpl` follows the same canonical_cn shape via internal `memEntry{e Entry, cn string}` so tests and production are symmetric. `validateEntry` keeps panicking on programming-error invariants (zero thumbprint, empty ContainerID, zero RegisteredAt); empty / malformed AgentName + Project now flow through `canonicalCNFromEntry` and surface as returned errors.
  - New `internal/controlplane/agentregistry/sqlite_test.go` (16 tests) covers: canonical_cn round-trip, UNIQUE collisions on thumbprint + container_id, reader-mode write rejection, CN mismatch → ErrUnknownAgent, Lookup variants, Evict happy + DB-failure paths, Snapshot ordering, two concurrent SQLiteWriter opens against same path, schema migration from legacy schema, malformed identity rejection, post-evict Lookup reads disk, panic on zero thumbprint.
  - `reap_test.go`: added `TestReap_RetriesOnTransientListerError` + `TestReap_GivesUpAfterMaxRetries`; existing tests unchanged.
  - `registry_test.go`: split `TestRegistry_Add_RejectsInvariantViolations` — programming-error invariants stay panic-asserted; new `TestRegistry_Add_RejectsMalformedIdentity` covers the err-return path.
  - Mocks regenerated via `go generate ./...`; idempotent.
  - `agentregistry/CLAUDE.md` rewritten to describe cache-less design, canonical_cn column, transactional schema apply, alpha migration policy, and the new Evict signature contract.
  - Verification: `go build ./...`, `go vet ./internal/controlplane/agentregistry/...`, `go test ./internal/controlplane/agentregistry/... -race -v` (all green), and `make test` (4965 tests, 7 skipped, 0 failed).
