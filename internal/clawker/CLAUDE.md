# Clawker Package

Application entry point and version metadata.

## Key Symbols

```go
var Version string  // Set via ldflags at build time
var Commit  string  // Set via ldflags at build time

func Main()         // Entry point: builds root command and executes
```

## Usage

Called from `cmd/clawker/main.go`. Sets up the root Cobra command via `internal/cmd/root` and runs it.
