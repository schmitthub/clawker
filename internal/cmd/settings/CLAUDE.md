# Settings Command Package

Parent command for user-level settings management (`~/.config/clawker/settings.yaml`).

## Files

| File | Purpose |
|------|---------|
| `settings.go` | `NewCmdSettings(f)` — parent command, aggregates subcommands |
| `edit/edit.go` | `NewCmdSettingsEdit(f, runF)` — interactive TUI editor |

## Subcommands

- `clawker settings edit` — opens the generic `storeui` TUI against `cfg.SettingsStore()`, wired through the settings domain adapter (`internal/config/storeui/settings`). No args.

## Key Symbols

```go
// settings/settings.go
func NewCmdSettings(f *cmdutil.Factory) *cobra.Command

// settings/edit/edit.go
type EditOptions struct {
    IOStreams *iostreams.IOStreams
    Config    func() (config.Config, error)
}
func NewCmdSettingsEdit(f *cmdutil.Factory, runF func(context.Context, *EditOptions) error) *cobra.Command
```

## Flow

```
editRun(ctx, opts)
  → opts.Config()                              # load config.Config
  → cfg.SettingsStore()                        # Store[Settings]
  → settingsui.Edit(ios, store, cfg)           # domain adapter + generic storeui
  → if result.Saved: print success to stdout   # "Settings saved (N fields modified)"
```

The heavy lifting lives in `internal/config/storeui/settings` (domain adapter — overrides, layer targets) and `internal/storeui` (generic `Edit[T Schema]` orchestrator). This package is intentionally a thin wrapper: flags are TUI-internal, not CLI-exposed.

## Related Rules

- `.claude/rules/storeui.md` — full store-UI architecture
- `internal/storeui/CLAUDE.md` — orchestrator API
- `internal/tui/CLAUDE.md` — `FieldBrowserModel`, `ListEditorModel`, `TextareaEditorModel`

## Testing

`edit_test.go` — Factory DI + Cobra invocation via `runF` seam. Uses `configmocks.NewIsolatedTestConfig(t)` for the mutable `Store[Settings]`.
