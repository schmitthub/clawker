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

## Usage

```go
import "github.com/schmitthub/clawker/internal/build"

fmt.Println(build.Version) // "DEV" or "1.2.3"
fmt.Println(build.Date)    // "" or "2024-06-01"
```

## Build Injection

```bash
# Makefile
-X 'github.com/schmitthub/clawker/internal/build.Version=$(CLI_VERSION)'
-X 'github.com/schmitthub/clawker/internal/build.Date=$(shell date +%Y-%m-%d)'

# GoReleaser
-X github.com/schmitthub/clawker/internal/build.Version={{.Version}}
-X github.com/schmitthub/clawker/internal/build.Date={{.Date}}
```
