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
| `source.go` | `Source` (git-generic) + `Canonical()` — the syntactic C1 dedup key (sha beats ref; subdir distinguishes monorepo siblings) |
| `bundle.go` | `Component`, `Bundle`, `LoadBundleDir` — parses `.clawker-bundle/bundle.yaml` and enumerates components by convention dir ONLY (does not parse component manifests). Hard-fail (`ManifestError`) vs advisory-warn split |
| `floor.go` | Embedded floor: `FloorNames(t)`, `FloorFS(t, name)`, `floorComponent` |
| `loose.go` | Loose-tier resolution under a project/user base |
| `installed.go` | Cache read side: `scanInstalled`/`scanNamespace`, `installedBundle`, `versionDirs` (keyed by identity; dot-prefixed entries skipped) |
| `resolver.go` | `Resolver.Resolve(t, name)` (bare = user>project>floor, ≤2 lazy stats; qualified = installed/in-place), `List(t)` (eager, with shadow rows), `Bundles()` (memoized, C1; returns `map[BundleID]*ResolvedBundle`) |
| `provenance.go` | `Tier` + `Provenance` (source clause + shadow rendering) |
| `warnings.go` | `Warning` + levenshtein typo suggestions for unknown convention dirs |
| `errors.go` | `ErrNotCached`, `CollisionError` (C1), `SourceError`, `ManifestError` |
| `manager.go` | `Manager` — the command-facing facade: wraps a `Resolver` + a `fetch.Fetcher`, and adds `Validate(dir) Report`, `Remove(id)`, `Declarations()`, `Install(ctx, src)` / `InstallDeclared(ctx)` (fetch a declared source / all declared-but-uncached), `Update(ctx, id)` (`[]UpdateResult`), and `AutoUpdateCheck(ctx)` (`[]Warning`, never errors). Constructed via `NewManager(cfg)`; exposed on the Factory as `BundleManager` |
| `install.go` | Fetch/cache write pipeline: `fetchIntoCache` (stage clone → subdir guard → manifest-validate-before-commit → per-bundle flock → C1 vs `source.yaml` → copy excl `.git`/escaping symlinks → atomic `os.Rename` commit). In-place path sources bypass the cache. |
| `update.go` | `UpdateResult`/`UpdateOutcome`, `Update`/`updateOne` (ref → `ResolveRef` compare → refetch on drift; sha-pin skipped; failure keeps cache), `AutoUpdateCheck` (opt-in `auto_update` entries only, warn+proceed) |
| `sourcemeta.go` | `source.yaml` cache-internal metadata (url/ref/sha-per-version/subdir/fetchedAt): links a cached identity to its declared source for update-compare and the cache-side C1 key. Engine-owned, NOT a lockfile; never read by the resolver |
| `fetch/` | Leaf pkg (stdlib + go-git/go-billy, no config import): `Fetcher` interface (`ResolveRef`, `Clone`), `NewFetcher()` go-git impl. ref path = ls-remote then single-branch shallow clone; sha path = init+fetch-by-sha with full-fetch fallback; ssh = go-git default env-driven agent auth; https = anonymous-first then `git credential fill` shell-out |
| `bundletest/` | In-process git fixture server over http AND ssh (`Server`, `InitRepo`/`Repo.Commit`/`Tag`) for the fetch/install integration tests. Real go-git authoring; env-driven ssh auth via `SSH_KNOWN_HOSTS` + a keyring `SSH_AUTH_SOCK` — no prod seams |

## Resolution semantics

- **Bare** resolves user loose > project loose > floor, stopping at the first
  hit — it NEVER scans the bundle set, so a broken bundle declaration cannot
  block a floor-only build (C3/C4 shadowing).
- **Qualified** resolves from the declared/cached bundle set only. An
  in-place `path:` source overrides a cached bundle of the same identity (the
  dev loop). A declared-but-uncached bundle yields `ErrNotCached`.
- **C1** (`Bundles()`): two declared sources whose manifests resolve to the same
  `(namespace, name)` from different `Canonical()` coordinates → `CollisionError`
  naming both declaring files. Same coordinate = idempotent re-declaration.

## Fetch/cache write side (implemented)

The fetch/cache WRITE side lives in `install.go` + `update.go` + `sourcemeta.go`
+ `fetch/`. Cache-side C1 (a second source fetched to the same identity) is
enforced against `source.yaml` at install; `AutoUpdateCheck` matches declared
opt-in sources to cached identities by `source.yaml` canonical.

Remaining resolver-side note (unchanged this phase, deliberately): the
`Resolver` scans the cache by identity and does NOT gate a cached bundle on its
declaration still being present — removing a `bundles:` entry hides availability
at the command layer (declarations) but leaves the cache resolvable until
`bundle remove`. `selectVersion` still picks the last version deterministically;
source-driven version pinning at resolve time is not wired (the cache content is
authoritative for resolution).

## Tests

Real-filesystem integration via `internal/testenv` (isolated XDG dirs) + the
config mock for the `config` dependency (config has its own parse tests). Covers
floor/loose/installed/in-place resolution, C1/C3/C4, `LoadBundleDir` hard-fail
and warning split, and the bare-ignores-broken-declaration invariant. Run:
`go test ./internal/bundle/...`.
