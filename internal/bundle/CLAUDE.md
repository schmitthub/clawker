# Bundle Package

Owns the clawker **bundle-install model**: three-tier component resolution
(embedded floor, loose local dirs, installed/in-place bundles), bundle-directory
loading, and the address/identity/source vocabulary. It sits between `config`
(which owns the persisted file shapes) and the consumers that render or seed
components (`internal/bundler` for image generation, `internal/monitor` for
observability).

**Import DAG:** `consts ← config ← bundle ← bundler ← docker`; `bundle ← monitor`.
`config` never imports `bundle`; `bundle` imports `config` for the manifest
shapes only (`BundleManifest`, `BundleSource`, and the harness/stack/monitoring
component manifests). `bundle` NEVER imports `bundler` (bundler imports bundle).

## Model (from the locked brainstorm spec)

Three tiers, all enumerated identically by **convention directory** —
`harnesses/<name>/`, `stacks/<name>/`, `monitoring/<name>/`; the subdirectory
name IS the component name. There is NO bare-manifest-at-root special case.

| Tier | Names | Backing |
|------|-------|---------|
| Embedded floor | bare (`node`) | `//go:embed all:assets` in `floor.go` |
| Loose local | bare | project `<root>/.clawker/<dir>/<name>/`, user `<config-dir>/<dir>/<name>/` |
| Installed / in-place bundle | qualified (`acme.tools.node`) | value-keyed host cache `<data>/bundles/<ns>/<name>/<sourceKey>/`, or a local `path:` source loaded in place |

## Files

| File | Purpose |
|------|---------|
| `component.go` | `ComponentType` enum (`ComponentHarness`/`Stack`/`Monitoring`) + `Dir()`/`String()`; convention-dir ↔ type mapping |
| `address.go` | `Address` (bare or qualified `namespace.bundle.name`), `BundleID` (identity pair; `String()` = dotted `namespace.name` via `consts.JoinIdentity`, the bundle-CLI spelling), `ParseAddress` (via `consts.SplitAddress`) |
| `source.go` | `Source` (git-generic) + `Canonical()` — the declared value in totality (sha beats ref; subdir distinguishes monorepo siblings) — and `Key()`, its digest: the cache directory key. Lookup is exact value equality; any change to the declared source addresses a new entry |
| `bundle.go` | `Component`, `Bundle`, `LoadBundleDir` — parses `.clawker-bundle/bundle.yaml` and enumerates components by convention dir ONLY (does not parse component manifests). Hard-fail (`ManifestError`) vs advisory-warn split |
| `floor.go` | Embedded floor: `FloorNames(t)`, `FloorFS(t, name)`, `floorComponent` |
| `loose.go` | Loose-tier resolution under a project/user base |
| `installed.go` | Cache read side: `InstalledEntry` (one value-keyed entry: identity, key, root), `scanInstalled`/`scanNamespace` (three levels: ns/name/key; dot-prefixed entries skipped), `cachedKeys` |
| `resolver.go` | `Resolver.Resolve(t, name)` (bare = user>project>floor, ≤2 lazy stats; qualified = installed/in-place), `List(t)` (eager, with shadow rows), `Bundles()` (memoized, C1, declaration-gated by exact value key; returns `map[BundleID]*ResolvedBundle` carrying the declaring source/file/version) |
| `status.go` | `Manager.Statuses()` — the declaration↔cache linkage view (`Status`/`StatusState`): resolving, declared-but-uncached, cached-but-undeclared, hand-placed (unmanaged). Backs the `bundle list` rows |
| `inventory.go` | `Manager.Inventory(t)` (`InventoryItem`) — per-type component inventory: name/owning-bundle-version/BundleID/provenance join over `Resolver.List` + `Bundles()`. Backs the per-type listing commands (`harness list`/`stack list`/`monitor extensions` via `cmdutil.NewInventoryListCommand`) |
| `provenance.go` | `Tier` + `Provenance` (source clause + shadow rendering) |
| `warnings.go` | `Warning` + levenshtein typo suggestions for unknown convention dirs |
| `errors.go` | `ErrNotCached`, `CollisionError` (C1), `SourceError`, `ManifestError` |
| `manager.go` | `Manager` — the command-facing facade: wraps a `Resolver` + a `fetch.Fetcher`, and adds `Validate(dir) Report`, `Remove(id)`, `Declarations()`, `Install(ctx, src)` / `InstallDeclared(ctx)` (fetch a declared source / all declared-but-uncached), `Update(ctx, id)` (`[]UpdateResult`), and `AutoUpdateCheck(ctx)` (`[]Warning`, never errors). Constructed via `NewManager(cfg)`; exposed on the Factory as `BundleManager` |
| `install.go` | Fetch/cache write pipeline: `fetchIntoCache` (stage clone → subdir guard → manifest-validate-before-commit → per-entry flock → copy excl `.git`/escaping symlinks + receipt into the staged tree → atomic `os.Rename` onto the value-keyed entry). No install-time collision check — a different declared value is just a sibling entry. In-place path sources bypass the cache. |
| `update.go` | `UpdateResult`/`UpdateOutcome`, `Update`/`updateOne` (declaration-driven: each remote declaration compares its own entry's receipt sha via `ResolveRef`, refetches in place on drift; sha-pin skipped; not-installed skipped; failure keeps cache), `AutoUpdateCheck` (opt-in `auto_update` entries only, warn+proceed) |
| `receipt.go` | `.fetch.yaml` per-entry fetch receipt (canonical/sha/fetched_at/display version). NEVER consulted for resolution — the directory key IS the declared value; exists for display (`bundle list` naming an undeclared entry) and update-compare |
| `fetch/` | Leaf pkg (stdlib + go-git/go-billy, no config import): `Fetcher` interface (`ResolveRef`, `Clone`), `NewFetcher()` go-git impl. ref path = ls-remote then single-branch shallow clone; sha path = init+fetch-by-sha with full-fetch fallback; ssh = go-git default env-driven agent auth; https = anonymous-first then `git credential fill` shell-out |
| `bundletest/` | In-process git fixture server over http AND ssh (`Server`, `InitRepo`/`Repo.Commit`/`Tag`) for the fetch/install integration tests. Real go-git authoring; env-driven ssh auth via `SSH_KNOWN_HOSTS` + a keyring `SSH_AUTH_SOCK` — no prod seams |

## Resolution semantics

- **Bare** resolves user loose > project loose > floor, stopping at the first
  hit — it NEVER scans the bundle set, so a broken bundle declaration cannot
  block a floor-only build (C3/C4 shadowing).
- **Qualified** resolves from the declared bundle set only. A
  declared-but-uncached bundle yields `ErrNotCached`.
- **Declaration-gating (value-keyed)**: everything resolvable traces to an
  explicit declaration. An in-place `path:` declaration loads directly from
  disk. A remote declaration addresses the cache entry whose directory key is
  the digest of the declaration's exact value (`Source.Key()` over
  `Canonical()`) — there is NO matching logic, so a declaration can never
  resolve content fetched from a different value (a ref edit, an ssh↔https url
  swap, a subdir move all address different keys). Deleting the `bundles:`
  entry makes the cached copy inert (it stays on disk until
  `clawker bundle remove`; re-declaring the same value reactivates it
  instantly, no refetch). An entry at a key no declared value digests to
  (hand-placed) never resolves.
- **Version**: a display property from the entry's fetch receipt (manifest
  version, else the resolved commit), never a selection axis. Pin coexistence
  falls out of value-keying: two projects pinning one repository differently
  address sibling entries — one project's re-pin never unresolves another's
  (the locked-spec "project A pins v1, project B v2" promise).
- **C1** (`Bundles()`): two DECLARED sources whose manifests resolve to the same
  `(namespace, name)` from different values → `CollisionError` naming both
  declaring files and the remedies. This applies to two in-place declarations,
  two remote declarations, AND an in-place declaration vs a declared cache
  entry — there is never a silent winner (a local dir silently overriding a
  cached identity would let any directory hijack a trusted installed bundle).
  There is NO install-time collision: installing a second source of an already
  cached identity just writes its own sibling entry (duplicated cache content
  is accepted); the collision surfaces at resolve, only when both are declared
  in one scope. An undeclared cache entry is inert, so it cannot collide — the
  bundle author's dev flow is to swap the url declaration for a path
  declaration, no purge needed. Same value = idempotent re-declaration.

## Fetch/cache write side (implemented)

The fetch/cache WRITE side lives in `install.go` + `update.go` + `receipt.go`
+ `fetch/`. Every fetch commits atomically onto the value-keyed entry for the
declared source — a re-fetch of the same value (a moved ref, a forced update)
replaces the entry in place; a different value writes a sibling entry. The
fetch receipt is staged with the content, so an entry can never exist without
it. `Update`/`AutoUpdateCheck` are declaration-driven: each remote declaration
compares its own entry's receipt sha against the remote tip and refetches in
place on drift.

## Tests

Real-filesystem integration via `internal/testenv` (isolated XDG dirs) + the
config mock for the `config` dependency (config has its own parse tests). Covers
floor/loose/installed/in-place resolution, C1/C3/C4, `LoadBundleDir` hard-fail
and warning split, and the bare-ignores-broken-declaration invariant. Run:
`go test ./internal/bundle/...`.
