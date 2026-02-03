# Init Command Package

Initialize user-level clawker settings (`~/.local/clawker/settings.yaml`).

## Files

| File | Purpose |
|------|---------|
| `init.go` | `NewCmdInit(f, runF)` — user initialization command |

## Key Symbols

```go
type InitOptions struct {
    IOStreams *iostreams.IOStreams
    Prompter *prompter.Prompter
    Yes      bool
}

func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *InitOptions) error) *cobra.Command
```

## Flags

- `--yes` / `-y` — skip interactive prompts

## Behavior

- Creates/updates user settings file
- Interactive prompts (unless `--yes`):
  - Build initial base image (optional)
  - Select Linux flavor (Debian/Alpine)
- Launches background Docker build if requested
- Skips build in `--yes` mode

## Pattern

Top-level command with options struct and `runF` injection for testing.
