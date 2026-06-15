# internal/semver

General-purpose semantic-version utility: parse, compare, sort, and
partial-version match, plus v-tolerant string helpers. Used across the DAG —
bundler version resolution, changelog range queries, and the update-checker
comparison all share this one package.

## Files

| File | Purpose |
|------|---------|
| `semver.go` | `Version` struct, `Parse`/`MustParse`/`IsValid`/`IsValidLoose`, `Compare`/`CompareStrings`, `Sort`/`SortDesc`, `SortStrings`/`SortStringsDesc`, `Match`, `FilterValid`. |
| `semver_test.go` | Parse edge cases (partial versions, prerelease, build metadata), sort ordering, `Match` patterns, `CompareStrings`, invalid inputs. |

## Domain Notes

- Partial versions are first-class: `Parse("2")` and `Parse("2.1")` succeed with `Minor`/`Patch` set to `-1`. Use `HasMinor()` / `HasPatch()` instead of comparing against zero.
- `Compare` follows SemVer 2.0.0 ordering: prereleases (`1.2.3-alpha`) sort **before** the corresponding release (`1.2.3`). Build metadata is ignored for comparison but preserved in `Version.Original`.
- `Match(versions, target)` is the partial-match entry point: `Match([...], "2.1")` returns the highest patch version whose `Major.Minor` is `2.1`. This is what `bundler.VersionsManager.ResolveVersions` uses when the user's `clawker.yaml` specifies a loose version constraint like `"2.1"`.
- `SortStrings` / `SortStringsDesc` silently filter out invalid strings (inline parse; unparseable inputs are dropped). Call `IsValid` directly if you need to surface parse errors.

## v-Tolerant String Helpers

- `CompareStrings(a, b string) int` — compares two version strings (returns -1/0/+1), tolerant of a leading `v` (`v1.2.3` == `1.2.3`). Total and panic-free: an unparseable version sorts **below** any valid version, and two unparseable versions compare equal. This keeps callers total — `internal/changelog`'s `Between`/`ForVersion` range queries use it.
- `IsValidLoose(s string) bool` — like `IsValid` but tolerant of a leading `v`. `internal/update`'s `IsNewer` uses it to gate the conservative "unparseable → not newer" contract before comparing.

## Dependencies

Leaf: stdlib only (`regexp`, `sort`, `strconv`, `strings`, `fmt`). No internal imports. Safe to use from any package in the DAG.
