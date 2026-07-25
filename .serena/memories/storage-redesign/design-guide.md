# Storage Design Guide (canon)

## Worldview

`internal/storage.Store[T]` is a **type-safe, file-backed data handler**. Nothing more. It knows: file discovery (walk-up vs single file vs explicit dirs), YAML layer merge, schema-kind validation, provenance-routed atomic writes, flock. Migrations: domain packages own the DEFINITIONS (schema evolution is domain knowledge); the engine owns the EXECUTION POINT — per-layer, node-level, inside `New`'s load pipeline before merge+decode (the only window where old-shape data is readable and repairable, since the load decode is strict).

A **caller is a domain worldview** that owns:
1. the **tagged schema struct** (the type contract — `yaml`/`label`/`desc`/`default` tags per `.claude/rules/storage-schema.md`),
2. the **constructor pair** — domain `New`/`NewFromString` wrapping eager `storage.New[T](opts...)`/`storage.NewFromString[T](yaml)`, wiring paths/filenames/migrations/lock via options, and
3. optionally, **typed accessor methods** built on the verbs — the PREFERRED consumer surface (USER RATIFIED 2026-07-25): convenience wrappers like `ProjectEgressRules()` that internally call `storage.Get[V]`. Raw engine verbs stay available to every caller as the escape hatch for edge cases. Accessors are domain convenience ON the verbs — never machinery replacing them (no interfaces-as-store-contract, no closures, no seams).

A schema may reuse another package's types as elements (firewall's `EgressRulesFile` wraps `[]config.EgressRule`) — that reuse is a decision the owning package makes; it does NOT relocate store ownership.

Everything else a caller does goes through the verbs. The uniformity across the codebase — every file-backed thing read and written the same way — IS the design value.

## The verbs

Construction (`storage.New` — eager: files seed the data; `storage.NewFromString` — eager: string seeds the data); persistence verb (`Write`); memory verbs (`Keys`, `Get`, `Set`, `Remove`). There is NO separate init verb — a standalone `Read()` was considered and dropped (USER 2026-07-25: "having Read() at that point is redundant over just returning everything during New. it just turns into an additional call with no value").

| Verb | Direction | Semantics |
|---|---|---|
| `storage.New[T](opts ...Option) (*Store[T], error)` | file(s) → memory | **Constructor IS the load**: discover, per-layer migrations (pre-decode), merge, decode into the schema type — **memory is typed**; the contract is enforced at load, the ONE user-feedback moment for invalid files. Errors eager. An unloaded store is unrepresentable. |
| `storage.NewFromString[T](yaml string) (*Store[T], error)` | string → memory | Same pipeline seeded from a string (tests + edge cases). `Write()` errors afterward — nowhere to write. |
| `Keys(segments...) []string` | memory → caller | Child key names at the path (no args = root). Missing path → empty slice. The check-in-advance primitive: existence = name present in parent's Keys. Supersedes `Has`. |
| `storage.Get[V](store, segments...) (V, error)` | memory → caller | **Package-level generic function** (methods can't take type params). Decodes the typed in-memory value at the keys into V; errors on mismatch or absence (`ErrKeyNotFound`). One type mention at the call site — Go's irreducible floor. Works uniformly: scalars, slices, maps, structs (`storage.Get[[]config.EgressRule](s, "security","firewall","rules")`). No `&out` pointer param, no assertions, no string round-trip. Supersedes the string-returning form, which died on containers. |
| `Set(key []string, value any) error` | caller → memory | Stage one field. Typed Go value; engine kind-validates against the schema contract + whole-tree decode-candidate, fails clean. Marks path dirty. RATIFIED 2026-07-25. `Set(keys, nil)` = caller infraction -> `ErrNilValue` sentinel, nothing staged (USER RULED 2026-07-25: "no user would ever do that users can't pass nil to something"); callers handling user input skip the Set on nil and own the nil-vs-`""`/`[]` distinction. |
| `Remove(segments...) error` | caller → memory | Delete a key; staged like Set. |
| `Write() error` | memory → file(s) | Persist dirty fields, provenance-routed, atomic (temp+fsync+rename+flock). Re-reads destination on-disk state before grafting. |

`WriteTo(path)` (whole dirty set -> explicit target) AND `WriteFieldTo(path, field)` (ONE field -> explicit target, rest of dirty tree untouched) both KEPT — USER RULED 2026-07-25 ("What if a user wants to write a specific field only somewhere explicit not the whole dirty tree?" = the WriteFieldTo case; Claude's kill proposal withdrawn).

## Schema enforcement — Set is the front-door (USER RATIFIED 2026-07-25)

`Set` is the ONLY mutation path that can introduce a value, so it carries ALL schema validation — "no path available for a bad schema to be written since Set is the only path to it." `Write` does zero schema work; its failures are real-world only (I/O, flock timeout, unparseable destination file).

- `Set` on a declared key absent from the tree → **upsert** (normal first write; intermediates materialize).
- `Set` on an undeclared key → error (`ErrUnknownKey` or `ErrSchemaDecode` family), nothing staged. Migrations never need a Set loophole — they operate at node level inside New's load pipeline, not through Set.
- `Set` wrong type → kind-check + whole-tree decode-candidate rejection, fails clean (nothing staged).
- Unknown keys in files: tolerated on load/merge, preserved on Write round-trip (hand-edit tolerance) — but `Set` cannot create them.

**REJECTED (2026-07-25): "schema-blind Read".** Claude proposed Read as parse+merge-only with no typed decode (to let legacy files always load). User: "i never said to do that." The load decodes; memory is typed. ("Read" here = the load pipeline, now inside eager New.)

**RESOLVED — the legacy-file problem (USER RULED 2026-07-25, locked: "migrations are locked, keep the setup as is"): migration machinery STAYS, runs inside the load pipeline (eager New) pre-decode. Ownership answer: storage owns the execution point — "clearly yes."** User's reasoning verbatim: "Read has to be strict and you can't migrate legacy at that point. we don't fail on keys outside of the schema they are just ignored and not marshalled. but a key that has a value change is another story and must fail during Read with a schema error, its the only opportunity to give users feedback on if a file is valid. unless callers of the file values handle it at call time but that is sloppy and problematic. so we might have to keep the migration setup as is so that migrations are ran before reads." — Node level, per layer, before merge+decode is the only window where old-shape data is both readable and repairable. Post-load plain-code migration dies for retyped keys (strict decode fails first). Domain owns the migration DEFINITIONS (passed via WithMigrations); engine owns the execution point (designed pipeline stage, not a seam). Set front-door unaffected — migrations operate on nodes, never through Set; no schema loophole needed. Unknown keys: ignored/not marshalled, never a failure; retyped keys post-migration: fail the load with schema error (surfaced from New).

**Unset semantics (USER RULED 2026-07-25 — SUPERSEDES the earlier tombstone model): "unset should not be a tombstone. unset should mean ignore in all cases. ie fallback to lower layer" — "where as a deliberate `""` or `[]` doesn't and means set and empty."** Two merge-effective states:

| State | File forms | Merge | Get |
|---|---|---|---|
| **Unset** | key missing OR `key:` bare (YAML null) | **ignored in ALL cases** — transparent; lower layer, then defaults show through | `ErrKeyNotFound` when unset after the full merge (no layer set it) |
| Set | `key: value` — including deliberate empties `""`, `0`, `false`, `[]`, `{}` | value wins normally; an explicit empty OVERRIDES lower layers with emptiness | value, nil error (`[]` → non-nil empty slice) |

- Bare `key:` never masks anything. To override a lower-layer list with nothing, write the explicit empty: `extensions: []` (set-and-empty), not `extensions:` (unset). monitoring.extensions resolved: bare = "no opinion here, inherit"; `[]` = "explicitly no extensions."
- **No nil-vs-`""` conflation anywhere:** `Get[string]` is fully unambiguous — `ErrKeyNotFound` (unset) vs value (incl. `""`). Pointer-V is never required for state discrimination. The interim `ErrNullValue` proposal is dead — unnecessary once null-as-state died.
- Merged tree contains only set keys — `Keys` reflects merged truth; a key bare-null in every layer is not present. `Remove` → key gone from its layer → unset (transparent).
- Accessor defaults logic collapses to one branch: `errors.Is(err, ErrKeyNotFound)` → apply domain default; otherwise use the value.
- `Set(keys, nil)` — RULED (2026-07-25): caller infraction, returns `ErrNilValue` sentinel, nothing staged. `Remove` is the one programmatic unset verb. Callers dealing with user input "need to be smart and skip the set in the case of nil, and know the difference between nil and \"\", [], etc from user input."

**Errors:** `ErrKeyNotFound` sentinel in `errors.go` — `Get` and `Remove` on an absent key return it wrapped; callers branch with `errors.Is` when absence is an expected case. `Keys` is the non-error existence check. `ErrMigrationType` stands (machinery stays); other sentinels (`ErrSchemaDecode`, `ErrNonMappingRoot`, `ErrMultiDocument`, `ErrAnchorNotAncestor`) stand.

**Key addressing:** slice of segments is canonical — `Get("security", "firewall", "rules")`. Exact, no reparse; kills the dot-in-key bug class (alias `a.b` corrupting the tree when `aliases.a.b` reparses as nesting). Single dotted string permitted as convenience for the common case; keys containing literal dots MUST use segments.

## Construction — eager New (USER RULED 2026-07-25)

> Convergence path: "THERE ARE NO CONSTRUCTORS" (struct literal + Read(Options)) → constructor-configures-then-Read ("domain packages... might have to use a constructor to configure it before then running Read() to seed it") → FINAL: eager New — "yea maybe having Read() at that point is redundant over just returning everything during New. it just turns into an additional call with no value. i can't think of a case where a caller would create a storage struct and not want Read performed immediately."

`storage.New[T](opts ...Option) (*Store[T], error)` — **the constructor IS the load**. Variadic `With*` options stay (the complexity justifies them: discovery tiers, walk-up anchoring, migrations, lock, header). One call: configure + discover + migrate + merge + decode; load/schema errors eager. `storage.NewFromString[T](yaml)` the sibling. No public `Read`/`ReadFromString` — no unloaded store can exist, no "Get before Read" misuse class, no ordering trap for migrations.

Option set carries over from options.go (verified 2026-07-25): WithFilenames (ordered variants; first = merge precedence at same depth), WithDefaultFilename (fresh-write pin; falls back to Filenames[0]), WithDefaults/WithDefaultsFromStruct (YAML base layer, lowest priority), WithWalkUp (anchor must be CWD or ancestor; dual placement .clawker/{f} → .clawker/.{f} → .{f}, each .yaml/.yml — discover.go:91-97), WithDirs (dual-placement probes), WithPaths + XDG conveniences (WithConfigDir/WithDataDir/WithStateDir/WithCacheDir — stay engine-side AS IS, USER RULED 2026-07-25; resolver.go survives), WithDotDefault, WithLock, WithHeader, **WithMigrations (LIVES — migration machinery stays, runs inside New pre-decode)**. Priority walk-up > Dirs > Paths; dedup by resolved path.

Domain packages own the wrapping constructor pair (mandated, `.claude/rules/store-backed-package.md`) — domain contract is an INTERFACE; unexported impl embeds the store; constructor calls storage.New (which loads), returns the impl as the interface:
```go
type StateStore interface {
    // domain accessors — the preferred consumer surface
    State() ...
    RecordUpdateCheck(...) error
}

type stateStoreImpl struct {
    *storage.Store[State]   // embedded engine — verbs promoted; the escape hatch
}

func New() (StateStore, error) {
    s, err := storage.New[State](
        storage.WithFilenames(consts.CLIStateFile),
        storage.WithPaths(consts.StateDir()),
        storage.WithMigrations(StateMigrations()...),
        storage.WithLock(),
    )
    if err != nil {
        return nil, fmt.Errorf("state: loading state: %w", err)
    }
    return &stateStoreImpl{s}, nil
}

func NewFromString(yaml string) (StateStore, error) {
    s, err := storage.NewFromString[State](yaml)
    if err != nil {
        return nil, fmt.Errorf("state: loading state from string: %w", err)
    }
    return &stateStoreImpl{s}, nil
}
```
Consumers never see an unloaded store — one is unrepresentable. Load errors eager at domain construction. Engine-level laziness dropped deliberately: domain stores sit behind Factory sync.Once nouns (`f.CLIState()`), so laziness already lives at the factory layer.

## Kill list (engine API that dies)

- `Txn`/`Tx`/`txnMu` — pseudo-transaction; mutex + forwarding wrapper masquerading as atomicity (DONE)
- `Refresh()` — zero production callers; the read-from-file moments are the New load and the write-path re-read
- `Get(path string, out any)` — pointer-out form (redundant type declaration at call site)
- `Read()` in ALL forms — snapshot-getter `Read() *T` (data access is `Get`) AND the standalone init verb (folded into eager `New`; "an additional call with no value")
- `Has` — superseded by `Keys` (enumeration covers presence)
- `MarkForWrite` — persist-current-value bolt-on
- ~~Migration machinery~~ **REVERSED (2026-07-25): machinery STAYS** — `Migration[T]`, `WithMigrations`, `applyMigrations`, `Noticef`, `MigratingLayerPath`, `ErrMigrationType` all live. The earlier "migrations = plain caller code post-load" ruling died on strict decode: a retyped key fails before caller code runs. Migrations run per-layer at node level inside New's load pipeline, before merge+decode. Domain declares them, passes via `WithMigrations`. See Schema enforcement section.

## Anti-patterns (each was proposed/found and rejected in this initiative)

1. **A store without its domain facade** — USER RULED (2026-07-25, final): "EVERY STORE OBJECT NEEDS A STRUCT OWNER... EVERY SINGLE ONE NEEDS TO EMBED A STORE OBJECT AND IMPLEMENT AN INTERFACE WITH SPECIFIC REQUIREMENTS." Every store — state, config's two, project registry, firewall rules, firewall identity — follows store-backed-package.md verbatim: interface contract (value-specific read accessors + disjoint write methods) + unexported impl embedding `*storage.Store[Schema]` + moq mocks. A bare `*storage.Store[T]` field consumed verb-by-verb at call sites is NOT the design. (Historical: one-method verb-wrappers on a raw store — Load()/Save(), the identityStore Txn seam — were rejected separately; the domain facade is not that.) Write methods own their compound-RMW serialization (impl mutex) when concurrent writers exist.
2. **Free-function wrappers** around store ops (`getRules(tx, &out)`, `writeRules(tx, rules)`, `readPersistedTable(store)`) — NO.
3. **Seams** — USER'S DEFINITION (2026-07-25): "a seam is when you allow for anonymous functions to be passed to override behavior. like Txn. its saying 'this is not designed or i didn't read the design so i'm injecting logic for this specific case.'" No fn-args, no behavior-injecting closures, anywhere. "No bullshit seams."
4. **Narrowed test interfaces** — one-method interfaces shrinking the store so tests can inject failure (`identityStore interface { Txn(...) }`) — NO. Tests use the real store: domain `NewFromString` for in-memory, tempdir for file-backed. (Distinct from seams — no closure — but same "didn't design it" smell.)
5. **Caller-burden side-doors** — bespoke accessors minted because the main path misbehaves (alias `OpenFileStore`). Verify the claimed misbehavior; fix root in the owning package; delete the bypass.
6. **Raw engine construction outside the owning package** — command code calling `storage.New[config.Project]` directly. The schema owner's constructor is the only construction point.
7. **Whole-struct reads** (USER RULED 2026-07-25: "no caller should be getting the entire thing anymore. that is an anti pattern. the caller gets the value it needs.") — no snapshot accessors at ANY layer: no `State() *State`, no root `Get[Schema]`, no `Read().X`. Engine enforces structurally: `Get` requires >= 1 key segment. Domain accessors return the specific value/group a consumer needs.
8. **Type re-declaration at call sites** — any API forcing callers to restate what the schema struct already declares.

## Go-reality notes

- A method cannot have a per-path static return type; text-out (`string`) + caller-side unmarshal keeps the type entirely on the caller's side of the fence (its own schema decision, its own decode).
- Engine-internal typed decode (`decodeNode[T]`) stays — validation + kind checking against the schema contract is the engine's job; it just doesn't leak Go types through the caller API.
- Set-fails-clean invariant preserved: validate → encode → decode-candidate all before commit; a failed Set stages nothing.
- Concurrency is owned by callers/upstream (ActionQueue, allocator mutex, single-writer CLI) — the store serializes per-op (`s.mu`) and per-file (flock) only.
