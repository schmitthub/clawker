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
| Installed / in-place bundle | qualified (`acme.tools.node`) | host cache `<data>/bundles/<ns>/<name>/<version>/`, or a local `path:` source loaded in place |

## Files

| File | Purpose |
|------|---------|
| `component.go` | `ComponentType` enum (`ComponentHarness`/`Stack`/`Monitoring`) + `Dir()`/`String()`; convention-dir ↔ type mapping |
| `address.go` | `Address` (bare or qualified `namespace.bundle.name`), `BundleID` (identity pair; `String()` = dotted `namespace.name` via `consts.JoinIdentity`, the bundle-CLI spelling), `ParseAddress` (via `consts.SplitAddress`) |
| `source.go` | `Source` (git-generic) + `Canonical()` — the syntactic C1 dedup key (sha beats ref; subdir distinguishes monorepo siblings) — and `Repository()`, the pin-stripped identity install uses so a re-pinned declaration updates its cache entry in place |
| `bundle.go` | `Component`, `Bundle`, `LoadBundleDir` — parses `.clawker-bundle/bundle.yaml` and enumerates components by convention dir ONLY (does not parse component manifests). Hard-fail (`ManifestError`) vs advisory-warn split |
| `floor.go` | Embedded floor: `FloorNames(t)`, `FloorFS(t, name)`, `floorComponent` |
| `loose.go` | Loose-tier resolution under a project/user base |
| `installed.go` | Cache read side: `scanInstalled`/`scanNamespace`, `installedBundle`, `versionDirs` (keyed by identity; dot-prefixed entries skipped) |
| `resolver.go` | `Resolver.Resolve(t, name)` (bare = user>project>floor, ≤2 lazy stats; qualified = installed/in-place), `List(t)` (eager, with shadow rows), `Bundles()` (memoized, C1, declaration-gated; returns `map[BundleID]*ResolvedBundle` carrying the declaring source/file/version) |
| `status.go` | `Manager.Statuses()` — the declaration↔cache linkage view (`Status`/`StatusState`): resolving, declared-but-uncached, cached-but-undeclared, hand-placed (unmanaged). Backs the `bundle list` rows |
| `inventory.go` | `Manager.Inventory(t)` (`InventoryItem`) — per-type component inventory: name/owning-bundle-version/BundleID/provenance join over `Resolver.List` + `Bundles()`. Backs the per-type listing commands (`harness list`/`stack list`/`monitor extensions` via `cmdutil.NewInventoryListCommand`) |
| `provenance.go` | `Tier` + `Provenance` (source clause + shadow rendering) |
| `warnings.go` | `Warning` + levenshtein typo suggestions for unknown convention dirs |
| `errors.go` | `ErrNotCached`, `CollisionError` (C1), `SourceError`, `ManifestError` |
| `manager.go` | `Manager` — the command-facing facade: wraps a `Resolver` + a `fetch.Fetcher`, and adds `Validate(dir) Report`, `Remove(id)`, `Declarations()`, `Install(ctx, src)` / `InstallDeclared(ctx)` (fetch a declared source / all declared-but-uncached), `Update(ctx, id)` (`[]UpdateResult`), and `AutoUpdateCheck(ctx)` (`[]Warning`, never errors). Constructed via `NewManager(cfg)`; exposed on the Factory as `BundleManager` |
| `install.go` | Fetch/cache write pipeline: `fetchIntoCache` (stage clone → subdir guard → manifest-validate-before-commit → per-bundle flock → C1 vs `source.yaml` → copy excl `.git`/escaping symlinks → atomic `os.Rename` commit). In-place path sources bypass the cache. |
| `update.go` | `UpdateResult`/`UpdateOutcome`, `Update`/`updateOne` (ref → `ResolveRef` compare → refetch on drift; sha-pin skipped; failure keeps cache), `AutoUpdateCheck` (opt-in `auto_update` entries only, warn+proceed) |
| `sourcemeta.go` | `source.yaml` cache-internal metadata (url/ref/sha-per-version/subdir/fetchedAt): links a cached identity to its declared source. Load-bearing for resolution (the declaration gate + version selection read it), plus update-compare and the cache-side C1 key. Engine-owned, NOT a lockfile |
| `fetch/` | Leaf pkg (stdlib + go-git/go-billy, no config import): `Fetcher` interface (`ResolveRef`, `Clone`), `NewFetcher()` go-git impl. ref path = ls-remote then single-branch shallow clone; sha path = init+fetch-by-sha with full-fetch fallback; ssh = go-git default env-driven agent auth; https = anonymous-first then `git credential fill` shell-out |
| `bundletest/` | In-process git fixture server over http AND ssh (`Server`, `InitRepo`/`Repo.Commit`/`Tag`) for the fetch/install integration tests. Real go-git authoring; env-driven ssh auth via `SSH_KNOWN_HOSTS` + a keyring `SSH_AUTH_SOCK` — no prod seams |

## Resolution semantics

- **Bare** resolves user loose > project loose > floor, stopping at the first
  hit — it NEVER scans the bundle set, so a broken bundle declaration cannot
  block a floor-only build (C3/C4 shadowing).
- **Qualified** resolves from the declared bundle set only. A
  declared-but-uncached bundle yields `ErrNotCached`.
- **Declaration-gating**: everything resolvable traces to an explicit
  declaration. An in-place `path:` declaration loads directly from disk. A
  cached bundle resolves ONLY while a live remote declaration matches it
  (`matchVersion`): the declaration must share the entry's pin-stripped
  `Repository()` AND have a recorded version fetched under its exact pin
  (`versions.*.pin` in `source.yaml`); a legacy entry with no per-version pin
  records falls back to an exact entry-level `Canonical()` match. Deleting the
  `bundles:` entry makes the cached copy inert (it stays on disk until
  `clawker bundle remove`; re-declaring the same source reactivates it
  instantly, no refetch). A cache entry with no `source.yaml` (hand-placed)
  never resolves — no ghost sources.
- **Version selection**: per-pin — the matched declaration resolves the most
  recently fetched version recorded under ITS pin, so two projects declaring
  the same repository at different pins each resolve their own version from
  the shared host cache (the locked-spec "project A pins v1, project B v2"
  promise); when several declarations match one entry, the latest-fetched
  match wins. Legacy fallback: most recently fetched overall, then last-sorted
  directory on a metadata gap.
- **C1** (`Bundles()`): two sources whose manifests resolve to the same
  `(namespace, name)` from different `Canonical()` coordinates → `CollisionError`
  naming both declaring files and the remedies. This applies to two in-place
  declarations, AND an in-place declaration vs a DECLARED cache entry — there
  is never a silent winner (a local dir silently overriding a cached identity
  would let any directory hijack a trusted installed bundle). An UNdeclared
  cache entry is inert, so it cannot collide — the bundle author's dev flow is
  to swap the url declaration for a path declaration, no purge needed. Same
  coordinate = idempotent re-declaration.

## Fetch/cache write side (implemented)

The fetch/cache WRITE side lives in `install.go` + `update.go` + `sourcemeta.go`
+ `fetch/`. Cache-side C1 (a second source fetched to the same identity) is
enforced against `source.yaml` at install using the PIN-STRIPPED
`Source.Repository()` (url + subdir, no ref/sha): the same repository under a
different pin is the SAME source being re-pinned — the cache entry adopts the
new ref/sha in place (CC-literal "replaces the first", no purge ceremony) and
prior version dirs coexist. Only a genuinely different repository claiming the
same identity is a `CollisionError`. `AutoUpdateCheck` matches declared opt-in
sources to cached identities by `source.yaml` canonical.

## Tests

Real-filesystem integration via `internal/testenv` (isolated XDG dirs) + the
config mock for the `config` dependency (config has its own parse tests). Covers
floor/loose/installed/in-place resolution, C1/C3/C4, `LoadBundleDir` hard-fail
and warning split, and the bare-ignores-broken-declaration invariant. Run:
`go test ./internal/bundle/...`.
