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

## Phase-2 seams (NOT yet implemented — see the phase report)

This package builds the resolution + read side. The fetch/cache WRITE side
(`internal/bundle/fetch`, `source.yaml` metadata) is a later phase. Consequences
until then, deliberately deferred (never legacy-defaulted):

- Remote (`url:`) declarations are resolved via the cache scan by identity, not
  linked back to their declaration → remote-source C1 and
  declaration-gating of a cached bundle (removing a `bundles:` entry should hide
  its cache) both need the `source.yaml` linkage.
- `selectVersion` picks deterministically among a cached bundle's versions;
  source-driven version pinning is a fetch-phase concern.

## Tests

Real-filesystem integration via `internal/testenv` (isolated XDG dirs) + the
config mock for the `config` dependency (config has its own parse tests). Covers
floor/loose/installed/in-place resolution, C1/C3/C4, `LoadBundleDir` hard-fail
and warning split, and the bare-ignores-broken-declaration invariant. Run:
`go test ./internal/bundle/...`.
