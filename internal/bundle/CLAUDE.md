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
| `installed.go` | Cache read side: `InstalledEntry` (one value-keyed entry: identity, key, root), `scanInstalled`/`scanNamespace` (three levels: ns/name/key; dot-prefixed entries skipped), `cachedKeys`, `entriesByKey` — THE key→entry flatten every declaration-to-entry consumer (resolver, update, GC winners) goes through. Identity levels come from the fetched manifest while the key comes from the declared value, so an upstream namespace/name rename leaves one key under two identities until GC collects the twin; `entriesByKey` resolves the duplicate to the entry with the later receipt `fetched_at` (readable receipt beats broken; tie falls to a deterministic identity order), so the fresher fetch is what the declared value addresses |
| `resolver.go` | `Resolver.Resolve(t, name)` (bare = user>project>floor, ≤2 lazy stats; qualified = installed/in-place), `List(t)` (eager, with shadow rows), `Bundles()` (memoized, C1, declaration-gated by exact value key via `entriesByKey`; returns `map[BundleID]*ResolvedBundle` carrying the declaring source/file/version). Readers are deliberately lock-free: a same-value refetch's two-rename swap has an instant-wide ENOENT window a mid-walk reader (or a later `Component.FS` read) can observe — left open by design (closing it would hold per-entry locks across whole builds); failure is a loud transient, never wrong content, and retry self-heals |
| `status.go` | `Manager.Statuses() ([]Status, []Warning, error)` — the declaration↔cache linkage view (`Status`/`StatusState`): resolving, declared-but-uncached, cached-but-undeclared, hand-placed (unmanaged). Backs the `bundle list` rows. An entry whose receipt exists but is unreadable has no source to print, so its row falls back to the unmanaged shape — but "hand-placed" would be a false diagnosis, so the read error is returned as a `Warning` naming the entry (same condition, same treatment as `resolver.go`); a corrupt receipt never fails the listing. The warning points at `bundle prune`, which collects such an entry once no declaration addresses it — unlike a genuinely receipt-LESS entry, which GC never collects (keep this hint in step with `gc.go`'s `collectEntry`) |
| `inventory.go` | `Manager.Inventory(t)` (`InventoryItem`) — per-type component inventory: name/owning-bundle-version/BundleID/provenance join over `Resolver.List` + `Bundles()`. Backs the per-type listing commands (`harness list`/`stack list`/`monitor extensions` via `cmdutil.NewInventoryListCommand`) |
| `provenance.go` | `Tier` + `Provenance` (source clause + shadow rendering) |
| `warnings.go` | `Warning` + levenshtein typo suggestions for unknown convention dirs |
| `errors.go` | `ErrNotCached`, `CollisionError` (C1), `SourceError`, `ManifestError` |
| `manager.go` | `Manager` — the command-facing facade: wraps a `Resolver` + a `fetch.Fetcher`, and adds `Validate(dir) Report`, `Remove(ctx, id)` (per-entry-locked removal that — unlike the GC's `removeEntry`, whose entries are condemned/unrooted — leaves the lock FILES in place, because a still-declared identity has legitimate concurrent installers that must keep locking one inode; leftover locks keep the identity dir non-empty, so Remove does not sweep empty parents. Residual: there is no identity-level lock, so a concurrent install of a still-declared value can re-create an entry while Remove runs — true means "removed what the scan found", not "identity absent afterwards"), `Declarations()`, `Install(ctx, src) (BundleID, []Warning, error)` / `InstallDeclared(ctx) ([]BundleID, []Warning, error)` (fetch a declared source / all declared-but-uncached; warnings carry dropped-symlink advisories), `Update(ctx, id)` (`[]UpdateResult`, each carrying refetch `Warnings`), and `AutoUpdateCheck(ctx)` (`[]Warning`, never errors). Constructed via `NewManager(cfg, validate, opts...)` — `validate` is the required `ComponentValidator` (`componentcheck.Validate` in production; the composing subpackage exists because the per-type consumption loaders live in packages that import this one), applied by both `Validate` and the install prefetch so publish-time and fetch-time checks never diverge; `WithRegisteredRoots` wires the GC roots provider; exposed on the Factory as `BundleManager` |
| `gc.go` | Cache GC against exact declaration roots. `collectRoots` = union of the current config's declarations + every registered project root's (`config.BundleDeclarationsAt` probes EVERY directory under the root — nested walk-up layers are declaring layers too; provider closure `RegisteredRootsFn` from the Factory; nil provider = GC off, fail-closed). An entry survives iff its value key ∈ roots AND it is the entry `entriesByKey` says that key addresses — a rooted key's superseded rename twin is collected (lock file kept: the key still has legitimate writers). Receipt-LESS (hand-placed) entries are NEVER collected; an entry whose receipt exists but is unreadable WAS fetched, so it IS collected when condemned, with a warning (coordinate with `status.go`'s unreadable-receipt hint). `Prune(ctx) PruneReport` = staging sweep (`sweepStaging`: reclaims `.tmp` trees older than `stagingSweepAge`; a retired entry whose origin slot is still empty is RESTORED under the entry lock, superseded/crash debris discarded, origin sidecar validated to the entry-slot shape before any rename) + full entry sweep + `MultiSource` report (identity rooted from ≥2 distinct repositories across projects, each repo + declaring files) + `Warnings`. `AutoGC(ctx, ids...) []Warning` = identity-scoped opportunistic sweep whose scope includes same-KEY twins under other identities (how the install/update verbs' fresh-identity ids reach a rename's leftover), called by the install/update verbs and by `AutoUpdateCheck` for refetched ids; never blocks the primary op. No TTL/LRU — liveness is computable, and a wrong collect self-heals with one refetch |
| `install.go` | Fetch/cache write pipeline: `fetchIntoCache` (stage clone → subdir guard → copy excl `.git`/reserved receipt name/non-portable symlinks into the content stage → one manifest read for the display version → exclusive-create receipt into the same stage → **full validation of the FINAL tree** → per-entry flock → swap onto the value-keyed entry). The authoritative validation runs on the exact bytes the swap publishes: never the clone (the two differ — dropped symlinks, `.git`, the receipt name — so validating the clone blesses content the entry cannot carry, a "successful" install of a broken entry), and never a pre-receipt tree (a write into the stage can change what an already-validated link resolves to). Between validation and swap the tree is frozen. `stageReceipt` fails closed on ANY pre-existing state at the reserved name, so a bundle-shipped `.fetch.yaml` — file or symlink — can neither survive nor redirect the write. **Symlink safety is decided by RESOLUTION, not by target text.** `copySymlink` is a cheap lexical first pass (drops absolute targets and targets spelling their way out of the root) — mid-walk the tree is incomplete, so it cannot resolve, and text cannot see through an intermediate directory link. `sanitizeStagedLinks` is the authority: once the tree is whole it `EvalSymlinks` every surviving link and drops any that is unresolvable or lands outside the stage. It runs BEFORE the receipt is written, which is what makes the receipt unreachable rather than merely un-spellable — a link aiming at it, however indirectly (e.g. `dir -> ../..` then `frag -> dir/.fetch.yaml`, which no single-level text compare catches), resolves to nothing and is dropped; link targets are fixed, so writing the receipt afterwards cannot bring a survivor onto it. The same pass closes chained ESCAPES through an in-tree directory link (`escapesRoot` returns false for a link resolving to the root, so such a link is legitimately carried and text-checks cannot see through it). Unresolvable ⇒ dropped: "cannot prove it safe" never means "carry it anyway". Dropped links are named in the validation error on failure and returned as `Warning`s on success (validation reads only what the component loaders need, so a dropped asset can install green and must still be reported). `commitContent` retires the entry it replaces (rename aside → rename in → discard) rather than deleting in place, so a failed swap restores the previously serving content (`retireEntry` records the origin entry path in a sidecar — `retiredOriginFile` — before moving the tree aside, so a stranded retired tree is traceable/restorable; `restoreEntry` recreates a parent a sibling-key GC removed mid-swap; if restore still fails the retired tree stays in `.tmp` as the only copy). No install-time collision check — a different declared value is just a sibling entry. In-place path sources bypass the cache. |
| `update.go` | `UpdateResult`/`UpdateOutcome`, `Update`/`updateOne` (declaration-driven: each remote declaration compares its own entry's receipt sha via `ResolveRef`, refetches in place on drift; sha-pin skipped; not-installed skipped; failure keeps cache). A refetch whose fresh manifest resolves a DIFFERENT identity (upstream rename) warns — the operator must judge it, it is also what a hijacked repo looks like — removes the old identity's superseded same-key entry, and reports the NEW identity on the result so the callers' AutoGC pass targets the live entries. `AutoUpdateCheck` (opt-in `auto_update` entries only, warn+proceed) |
| `receipt.go` | `.fetch.yaml` per-entry fetch receipt (canonical/sha/fetched_at/display version). NEVER consulted for resolution — the directory key IS the declared value; exists for display (`bundle list` naming an undeclared entry) and update-compare |
| `fetch/` | Leaf pkg (stdlib + go-git/go-billy, no config import): `Fetcher` interface (`ResolveRef`, `Clone`), `NewFetcher()` go-git impl. ref path = ls-remote then single-branch shallow clone; sha path = init+fetch-by-sha with full-fetch fallback; ssh = agent auth + OpenSSH-parity known_hosts tolerance (`knownhosts.go`: damaged lines skipped, verification stays fail-closed — go-git's default flow hard-fails the whole file on one malformed line); https = anonymous-first then `git credential fill` shell-out |
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
