# Generate Command Package

Generate Dockerfiles for Claude Code releases from npm registry.

## Files

| File | Purpose |
|------|---------|
| `generate.go` | `NewCmdGenerate(f, runF)` — Dockerfile generation command |

## Key Symbols

```go
type GenerateOptions struct {
    IOStreams  *iostreams.IOStreams
    Versions  []string
    SkipFetch bool
    Cleanup   bool
    OutputDir string
}

func NewCmdGenerate(f *cmdutil.Factory, runF func(context.Context, *GenerateOptions) error) *cobra.Command
```

## Flags

- `--skip-fetch` — use cached versions.json
- `--cleanup` (default: true) — remove stale files
- `--output` / `-o` — custom output directory (default: `~/.clawker/build/`)

## Behavior

- Fetches versions from npm (`@anthropic-ai/claude-code`)
- Version patterns: `latest`, `stable`, `next` (dist-tags), `2.1` (partial), `2.1.2` (exact)
- Shows existing `versions.json` if no versions specified
- Generates multi-version/variant Dockerfiles via `bundler.DockerfileManager`

## Pattern

Top-level command with options struct and `runF` injection for testing.
