# Storage Migration Guide — engine rewrite + consumer convergence

Order of operations: (1) engine surface rewrite, (2) owning-package constructors, (3) consumer conversions, (4) tests, (5) docs/rules. Do NOT start without user GO.

## Phase 1 — engine (`internal/storage`)

Current state on branch: `Txn`/`Tx`/`txnMu` already deleted; storage tests green.

- `storage.New[T](opts ...Option) (*Store[T], error)` STAYS and becomes EAGER — the constructor IS the load: configure + discover + per-layer migrations + merge + strict typed decode; errors eager (USER RULED 2026-07-25). `storage.NewFromString[T](yaml)` sibling (Write errors afterward — no write path). Variadic `With*` options STAY; options.go survives. NO public `Read`/`ReadFromString` — standalone init verb dropped as redundant; delete the lazy-load path (`ensureLoaded`-style guards) — an unloaded store is unrepresentable. Sub-open: XDG convenience options (WithConfigDir/WithDataDir/WithStateDir/WithCacheDir) stay engine-side vs move to domain-via-consts (resolver.go fate rides on this).
- The load DECODES into the schema type (memory is typed — schema-blind Read was proposed and REJECTED). Mutation validation concentrates in `Set` (kind-check + decode-candidate; undeclared key → error; upsert for declared-but-absent; fails clean). `Write` does no schema work — Set is the only path by which a bad schema could reach disk. Legacy-file problem RESOLVED (USER DIRECTION 2026-07-25): migration machinery stays, runs per-layer at node level inside New pre-decode — the only window old-shape data is readable+repairable; unknown keys ignored (never fail); retyped keys post-migration fail New with schema error (the one user-feedback moment on file validity).
- `Keys(segments ...string) []string` — child-key listing (existence pre-check; supersedes Has). `Get` becomes PACKAGE-LEVEL GENERIC: `storage.Get[V](store, segments...) (V, error)` — decodes typed in-memory value at keys into V; `ErrKeyNotFound` on absent; mismatch error on wrong V. Delete method `Get(path string, out any) (bool, error)`.
- `Set`: `Set(key []string, value any) error` (RATIFIED) — typed value, kind-validate against schema, graft, decode-candidate check (keep fails-clean invariant), mark dirty. `Set(keys, nil)` = caller infraction → `ErrNilValue` sentinel, nothing staged (USER RULED 2026-07-25); callers facing user input skip Set on nil.
- `Remove(segments ...string)`.
- `ErrKeyNotFound` sentinel in errors.go. Unset semantics (USER RULED 2026-07-25, supersedes tombstone model): bare `key:` (`!!null` node) = unset = ignored in ALL layers — merge skips it, lower layer/defaults show through; deliberate empty (`"" `/`0`/`false`/`[]`) = set-and-empty, wins merge. Mechanism = YAML tag discriminator at the node level (`!!null` vs `!!str`), checked in merge before typed decode. Get: `ErrKeyNotFound` when unset post-merge, value otherwise. `Set(keys, nil)` → `ErrNilValue` (ruled). `ErrMigrationType` STAYS (machinery stays). Add `ErrNilValue` to errors.go.
- Segment addressing plumbed through `validatePath`/`markDirty`/provenance keys — internal dirty-path representation should become `[]string` or an escaped join that cannot collide with literal dots in keys.
- Delete: `Refresh`, `Has`, `MarkForWrite`, snapshot `Read() *T` + `atomic.Pointer` value publication IF nothing needs typed snapshots (config heavily uses `Read().X` — see Phase 3; typed decode remains internal for validation). KEEP `WriteTo` AND `WriteFieldTo` (USER RULED 2026-07-25 — single-field-to-explicit-target is a wanted capability).
- Keep: `Write`, `WriteTo`, `MarkSeedForWrite` (init preset flow), `Layers`, `Options`, `WriteTargets`, flock, atomic write substrate (`write.go` temp+fsync+rename — sound, untouched).
- Migration machinery STAYS AS IS (REVERSED 2026-07-25 — earlier delete ruling broke on strict decode): `Migration[T]`, `WithMigrations`, `applyMigrations`, `Noticef`, `MigratingLayerPath`, `ErrMigrationType`, deferred-notice queue, per-layer migration pass all live, running inside New's load pipeline pre-decode. Domain packages keep owning the DEFINITIONS (`internal/config/migrations.go`, `internal/state/migrations.go`) and pass them via `WithMigrations`.

## Phase 2 — owning packages (constructor pairs)

Every implementer: tagged schema + `New` + `NewFromString`, per `.claude/rules/store-backed-package.md` constructor section.

- `internal/config` — has both routes; adapt to eager New (load errors stay at construction, same as today). Accessor-layer question RESOLVED (2026-07-25): typed domain accessors are the PREFERRED consumer surface — config accessors stay, reimplemented over `storage.Get[V]` internally.
- `internal/project` (registry) — adapt.
- `internal/state` — already plain-verb after Txn conversion; constructor collapses to one eager storage.New call; keeps `NewFromString` per rules.
- `controlplane/firewall` rules store (`rules_store.go`) — CORRECTED (user ruling 2026-07-25: "EVERY STORE OBJECT NEEDS A STRUCT OWNER … EVERY SINGLE ONE NEEDS TO EMBED A STORE OBJECT AND IMPLEMENT AN INTERFACE"): `EgressRulesStore` interface + unexported impl embedding `*storage.Store[EgressRulesFile]`; `NewRulesStore(cfg)` / `NewRulesStoreFromString(yaml)` both return the INTERFACE. An earlier revision of this guide said "no interface" — that was wrong and is superseded.
- `controlplane/firewall` identity store (`identity.go`) — same shape: `RouteIdentityStore` interface (`Entries`/`Cursor`/`SetTable`) + unexported impl; `NewIdentityStore(cfg)` / `NewIdentityStoreFromString(yaml)` return the interface; `NewIdentityAllocator(store RouteIdentityStore)`.

## Phase 3 — consumer conversions (violation inventory)

| Site | Violation | Conversion |
|---|---|---|
| `firewall/handler.go` (`addRulesToStore`, `removeRuleFromStore`, `removePathRuleFromStore`) | store mutation living at the call site | DONE: moved onto the impl as `EgressRulesStore.AddRules` / `RemoveRule` / `RemovePathRule` (inline `storage.Get` + fold, then `Set`+`Write`). The "already serialized by ActionQueue" note in an earlier revision was WRONG: rule RPCs mutate PRE-Submit on the gRPC goroutine while `Stack.ensureConfigs` canonicalizes INSIDE the queue worker — two concurrent writers, so the impl holds an `sync.RWMutex` across each read-modify-write. |
| `firewall/stack.go` (`ensureConfigs` rules heal) | store mutation living at the call site | DONE: `EgressRulesStore.Canonicalize()` (+ `needsCanonicalRewrite`); Stack reads `Rules()` for warnings, then calls it. |
| `firewall/identity.go` (`readPersistedTable`, `persistLocked`, the old one-method seam) | free-fn wrapper + Txn + one-method test seam | DONE: allocator holds `RouteIdentityStore`; construction reads `Entries()`/`Cursor()`, `persistLocked` calls `SetTable(entries, cursor)`. Serialization split: `a.mu` owns the in-memory read-modify-write, the store's own mutex makes one persist indivisible. Failure-injection tests use a real store on an unwritable tempdir. |
| `internal/controlplane/cmd.go` (`buildEnforcement`, `grpcStackDeps`) | threaded raw `*storage.Store[fwhandler.EgressRulesFile]` across a package boundary | DONE: the field/param type is `fwhandler.EgressRulesStore`; `internal/storage` is no longer imported by `internal/controlplane`. |
| `internal/cmd/alias/shared/shared.go:123` (`OpenFileStore`, `WriteAliases`) | cmd-layer raw engine store; rationale VERIFIED STALE (composite ProjectStore never marks seed dirty — only init preset flow `config.go:269` does) | Delete both. Alias set/delete: `cfg.ProjectStore().Set(["aliases","<name>"], v)` / `Remove` → targeted write to the project's own file via `WriteTo` (segment addressing also fixes the dotted-alias-name corruption). Target file = existing `ProjectConfigPath` walk (already in shared.go). |
| `internal/cmd/project/shared/discovery.go:37` (`HasLocalProjectConfig` slow path) | cmd-layer raw probe store | Owning package's constructor is the only construction point. Existence predicate belongs behind config/project surface — Phase-3 implementation detail, no pre-ruling needed (user 2026-07-25); pick the natural owner during conversion. Note: prospective config deconstruction (settings vs project domains — see README future directions) may relocate it. |
| `internal/state/state.go` | (converted already) | Done — bare Set/Write, disjoint-field comments rewritten. |
| `internal/storeui` (`field.go`, `edit.go`) + `internal/config/storeui/*` | uses `WriteFieldTo` + typed snapshot? | `WriteFieldTo` KEPT — per-field save stays Set → WriteFieldTo. Survey during implementation. |
| `internal/config/migrations.go`, `validate.go`, `state/migrations.go` | migration fns on old Get/Set | Port to new signatures. |

## Phase 4 — tests

- Engine: port `storage_test.go` to new surface (Txn test already deleted). Oracle+golden merge tests unaffected in substance.
- Per package: real `New()` + testenv tempdir (file-backed) or `NewFromString` (in-memory). NO mock stores of the engine, no seams. moq mocks of *domain* interfaces (config.Config etc.) unaffected.
- `identity_internal_test.go` seam fake deleted (see Phase 3).
- Targeted runs only: `go test ./internal/storage/... ./internal/state/... ./internal/config/... ./internal/project/... ./controlplane/firewall/... ./internal/cmd/alias/... ./internal/cmd/project/... ./internal/controlplane/...` — NEVER `go test ./...` in-container.

## Phase 5 — docs/rules

- `internal/storage/CLAUDE.md` — full rewrite to new surface (Txn/Refresh/Get(&out)/snapshot-Read references purged; gotcha row "Compound read-modify-write isn't atomic — wrap in Txn" dies).
- `.claude/rules/store-backed-package.md` — constructor-pair section stands; **interface + moq + domain-verb prescription conflicts with the no-wrapper ruling** — rewrite per canon (USER decision on what replaces the interface guidance; `internal/state`'s `StateStore` interface itself may be judged slop — ASK).
- `.claude/rules/storage-schema.md` — mostly stands (tag contract unchanged).
- `internal/config/CLAUDE.md`, firewall `CLAUDE.md`, `.claude/docs/DESIGN.md` §2.4 — update.
- Auto-memory `project_storage_txn_slop_removal` — close out when shipped.

## Known open questions for the user (collect answers before the relevant phase)

1. ~~Ratify Set signature + WriteTo/WriteFieldTo~~ RESOLVED 2026-07-25: Set(key []string, value any) ratified; WriteTo AND WriteFieldTo both kept; Set(keys, nil) → ErrNilValue.
2. ~~Lazy-Read edge~~ RESOLVED by eager New (2026-07-25): no unloaded store can exist; `Get`-before-load unrepresentable.
3. config accessor layer: convert all typed-snapshot reads to string `Get`, or is engine-internal typed decode acceptable inside the owning package?
4. Discovery predicate owner (config vs project).
5. RESOLVED (2026-07-25): `StateStore`-style domain INTERFACE is canon — unexported impl embeds the store, constructor returns `&stateStoreImpl{s}` as the interface. (Rejected thing was verb-replacing wrapper interfaces at the store level, not the domain contract.)
