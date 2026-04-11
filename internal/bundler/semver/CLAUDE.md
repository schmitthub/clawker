# bundler/semver Subpackage

Semver parsing, comparison, sorting, and partial-version matching for `internal/bundler`. Full API surface is documented in the parent `internal/bundler/CLAUDE.md` under the "Subpackage: `semver/`" section — treat this file as a pointer, not a duplicate.

## Files

| File | Purpose |
|------|---------|
| `semver.go` | `Version` struct, `Parse`/`MustParse`/`IsValid`, `Compare`, `Sort`/`SortDesc`, `SortStrings`/`SortStringsDesc`, `Match`, `FilterValid`. |
| `semver_test.go` | Parse edge cases (partial versions, prerelease, build metadata), sort stability, `Match` patterns, invalid inputs. |

## Domain Notes

- Partial versions are first-class: `Parse("2")` and `Parse("2.1")` succeed with `Minor`/`Patch` set to `-1`. Use `HasMinor()` / `HasPatch()` instead of comparing against zero.
- `Compare` follows SemVer 2.0.0 ordering: prereleases (`1.2.3-alpha`) sort **before** the corresponding release (`1.2.3`). Build metadata is ignored for comparison but preserved in `Version.Original`.
- `Match(versions, target)` is the partial-match entry point: `Match([...], "2.1")` returns the highest patch version whose `Major.Minor` is `2.1`. This is what `bundler.VersionsManager.ResolveVersions` uses when the user's `clawker.yaml` specifies a loose version constraint like `"2.1"`.
- `SortStrings` / `SortStringsDesc` silently filter out invalid strings via `FilterValid`. Call `IsValid` directly if you need to surface parse errors.

## Dependencies

Leaf: stdlib only (`regexp`, `sort`, `strconv`, `strings`, `fmt`). No internal imports. Safe to use from any package in the DAG.
