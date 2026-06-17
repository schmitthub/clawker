# bundler/registry Subpackage

npm registry client and version metadata types for `internal/bundler`. Full API surface is documented in the parent `internal/bundler/CLAUDE.md` under the "Subpackage: `registry/`" section — treat this file as a pointer to the source of truth, not a duplicate.

## Files

| File | Purpose |
|------|---------|
| `fetcher.go` | `Fetcher` interface — `FetchVersions(ctx, pkg) ([]string, error)`, `FetchDistTags(ctx, pkg) (DistTags, error)`. The test seam for version resolution. |
| `npm.go` | `NPMClient` — concrete `Fetcher` backed by `https://registry.npmjs.org`. Configurable via `WithHTTPClient`, `WithBaseURL`, `WithTimeout`. Default timeout 30s. |
| `types.go` | `DistTags`, `VersionInfo`, `VersionsFile`, `NPMPackageInfo`, `NewVersionInfo(...)`. `VersionsFile.SortedKeys` returns keys in semver-descending order; order-sensitive consumers iterate that. |
| `errors.go` | `NetworkError` (with `Unwrap`), `RegistryError` (with `IsNotFound` for 404 detection), `ParseError` (with `Unwrap`; HTTP-200 body decode failure, distinct from network failure), sentinel `ErrVersionNotFound`/`ErrInvalidVersion`/`ErrNoVersions`. `bundler/errors.go` re-exports these as type aliases so callers outside `registry` import `bundler` instead. |
| `npm_test.go` | `httptest.Server` stubs for `FetchVersions`, `FetchDistTags`, 404 handling, network error paths. |

## Key Invariants

- `NewNPMClient()` defaults are safe for production (`baseURL = defaultNPMRegistry`, `timeout = defaultTimeout = 30s`).
- `RegistryError.IsNotFound()` is the canonical way to distinguish "package doesn't exist" from other HTTP failures — don't grep the error string.
- `VersionsFile.SortedKeys` parses each key into a `semver.Collection` and `sort.Sort(sort.Reverse(...))` (semver-descending). Order-sensitive consumers (`GenerateDockerfiles`, `displayVersionsFile`) iterate it. `versions.json` itself serializes as a plain JSON map — keys re-parse into a map on load, so on-disk key order is not a contract.
- `NPMClient.fetchPackageInfo` reads at most 1 KiB of error body on non-200 responses — avoids runaway memory on a broken registry mirror.

## Dependencies

Imports: stdlib only plus `github.com/Masterminds/semver/v3`. Leaf of the registry subtree — don't pull in config/docker/logger here.
