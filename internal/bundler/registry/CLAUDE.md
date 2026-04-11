# bundler/registry Subpackage

npm registry client and version metadata types for `internal/bundler`. Full API surface is documented in the parent `internal/bundler/CLAUDE.md` under the "Subpackage: `registry/`" section — treat this file as a pointer to the source of truth, not a duplicate.

## Files

| File | Purpose |
|------|---------|
| `fetcher.go` | `Fetcher` interface — `FetchVersions(ctx, pkg) ([]string, error)`, `FetchDistTags(ctx, pkg) (DistTags, error)`. The test seam for version resolution. |
| `npm.go` | `NPMClient` — concrete `Fetcher` backed by `https://registry.npmjs.org`. Configurable via `WithHTTPClient`, `WithBaseURL`, `WithTimeout`. Default timeout 30s. |
| `types.go` | `DistTags`, `VersionInfo`, `VersionsFile`, `NPMPackageInfo`, `NewVersionInfo(...)`. `VersionsFile.MarshalJSON` emits keys in semver-descending order for deterministic `versions.json` output. |
| `errors.go` | `NetworkError` (with `Unwrap`), `RegistryError` (with `IsNotFound` for 404 detection), sentinel `ErrVersionNotFound`/`ErrInvalidVersion`/`ErrNoVersions`. `bundler/errors.go` re-exports these as type aliases so callers outside `registry` import `bundler` instead. |
| `npm_test.go` | `httptest.Server` stubs for `FetchVersions`, `FetchDistTags`, 404 handling, network error paths. |

## Key Invariants

- `NewNPMClient()` defaults are safe for production (`baseURL = defaultNPMRegistry`, `timeout = defaultTimeout = 30s`).
- `RegistryError.IsNotFound()` is the canonical way to distinguish "package doesn't exist" from other HTTP failures — don't grep the error string.
- `VersionsFile.MarshalJSON` delegates ordering to `semver.SortStringsDesc` so written files are stable across runs (golden tests rely on this).
- `NPMClient.fetchPackageInfo` reads at most 1 KiB of error body on non-200 responses — avoids runaway memory on a broken registry mirror.

## Dependencies

Imports: stdlib only plus `internal/bundler/semver`. Leaf of the registry subtree — don't pull in config/docker/logger here.
