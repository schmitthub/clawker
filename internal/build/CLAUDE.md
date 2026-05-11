# Build Package

Single source of truth for build-time metadata injected via ldflags.

Leaf package: stdlib only, no internal imports.

## Testing

No unit tests for `build.go` — it is straightforward wiring and regressions surface via downstream command tests and `make test`.

## Exported Symbols

```go
var Version string  // Default "DEV", set via -ldflags
var Date    string  // Default "", YYYY-MM-DD, set via -ldflags
```

### `init()` Fallback

When `Version` is `"DEV"` (no ldflags), `init()` attempts `debug.ReadBuildInfo()` to pick up the module version (set by `go install`). Falls back silently to `"DEV"` when build info is unavailable or reports `"(devel)"` (normal for `go run`/`go build`).

## Usage

```go
import "github.com/schmitthub/clawker/internal/build"

fmt.Println(build.Version) // "DEV" or "1.2.3"
fmt.Println(build.Date)    // "" or "2024-06-01"
```

## Build Injection

```bash
# Makefile (dev builds — Date intentionally not stamped; falls back to "")
-X 'github.com/schmitthub/clawker/internal/build.Version=$(CLAWKER_VERSION)'

# GoReleaser (release builds — Date pinned to tag's commit timestamp)
-X github.com/schmitthub/clawker/internal/build.Version={{.Version}}
-X github.com/schmitthub/clawker/internal/build.Date={{.CommitDate}}
```
