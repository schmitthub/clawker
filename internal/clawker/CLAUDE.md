# Clawker Package

Application entry point and version metadata.

## Exported Symbols

```go
var Version string  // Set via -ldflags at build time
var Commit  string  // Set via -ldflags at build time

func Main() int     // Entry point: builds root command via internal/cmd/root, executes, returns exit code
```

## Usage

Called from `cmd/clawker/main.go`. The `Version` and `Commit` variables are injected by the build system using `-ldflags` and made available to the CLI's `--version` flag.

All symbols are in `cmd.go`.
