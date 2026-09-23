# Licenses Command Package

Show the third-party license texts embedded in this build.

## Files

| File | Purpose |
|------|---------|
| `licenses.go` | `NewCmdLicenses(f)` — pages `content(embedFS, rootDir)`; `content` joins `report.txt` and each module's files under `third-party/` |
| `embed_<goos>_<goarch>.go` | Per-platform `//go:embed all:embed/<goos>-<goarch>` and `rootDir` (filename build constraints) |
| `export_test.go` | Exposes `Content` and `Placeholder` to the black-box tests |
| `embed/<goos>-<goarch>/PLACEHOLDER` | Committed so `go:embed` compiles; everything else in `embed/` is ignored by git |

## Generation

`scripts/licenses.sh <goos> <goarch>` writes the embed directory. goreleaser runs it as a per-build pre hook, and `make clawker` (so also `make restart` and the install targets) runs it for the build platform. `make clawker-clean` removes the output. `make licenses-check` runs `--check` for all release platforms in CI. A build without generated files (plain `go build`, `go install`) prints a placeholder.

The script adds modules that go-licenses v2.0.1 cannot classify by hand (`APACHE_OVERRIDES`). It keeps vendored licenses under `internal/` and drops only clawker's own root license files.

## Testing

`licenses_test.go` covers `content` with `fstest.MapFS` (placeholder, sort order, nested modules, read errors) and that the command prints this build's embedded content (placeholder or generated texts). No Docker required.
