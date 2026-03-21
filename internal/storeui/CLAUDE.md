# Store UI Package

Generic TUI for browsing and editing `storage.Store[T]` instances. Bridges typed stores (`internal/storage`) and terminal presentation (`internal/tui`).

## Architecture

```
cmd/settings/edit, cmd/project/edit
  → config/storeui/settings, config/storeui/project  (domain adapters)
    → internal/storeui                                (generic building blocks)
      → internal/tui (widgets, RunProgram)
      → internal/storage (Store[T] API)
```

**Import boundary**: Imports `bubbletea` for `tea.Model`/`tea.Cmd` (allowed per tui CLAUDE.md). Uses `tui.Is*` key matchers. Styles via `iostreams.*` qualified imports.

## Files

| File | Purpose |
|------|---------|
| `field.go` | `FieldKind`, `Field`, `Override`, `ApplyOverrides` — core types |
| `reflect.go` | `WalkFields(v)` — reflection-based struct walker |
| `value.go` | `SetFieldValue(v, path, val)` — reverse reflection writer |
| `editor.go` | `editorModel` — BubbleTea Browse→Edit→Save state machine |
| `edit.go` | `Edit[T](ios, store, opts...)` — orchestration entry point |

## Public API

### Types

```go
type FieldKind int  // KindText, KindBool, KindTriState, KindSelect, KindInt, KindStringSlice, KindDuration, KindComplex

type Field struct {
    Path, Label, Description string
    Kind        FieldKind
    Value       string
    Options     []string
    Validator   func(string) error
    Required, ReadOnly bool
    Order       int
}

type Override struct {
    Path        string
    Label, Description *string
    Kind        *FieldKind
    Options     []string
    Validator   func(string) error
    Required, ReadOnly *bool
    Order       *int
    Hidden      bool
}

type Result struct { Saved, Cancelled bool; Modified map[string]string }
type Option func(*editOptions)
```

### Functions

```go
func WalkFields(v any) []Field                           // Reflect struct → fields
func SetFieldValue(v any, path string, val string) error // Set field by dotted path
func ApplyOverrides(fields []Field, overrides []Override) []Field

func Edit[T any](ios *iostreams.IOStreams, store *storage.Store[T], opts ...Option) (Result, error)
func WithTitle(title string) Option
func WithOverrides(overrides []Override) Option
func WithSkipPaths(paths ...string) Option
```

## Domain Adapters

| Package | Schema | Purpose |
|---------|--------|---------|
| `config/storeui/settings` | `config.Settings` | Labels, host_proxy read-only |
| `config/storeui/project` | `config.Project` | Labels, complex types hidden, workspace mode as Select |

Each adapter exports `Overrides() []storeui.Override` and `Edit(ios, store) (Result, error)`.

## Key Design Decisions

1. `KindSelect` separated from `KindTriState` — distinct widget semantics
2. `KindComplex` auto-enforces `ReadOnly` in `ApplyOverrides`
3. Nil `*struct` recursion in `WalkFields` — produces zero-value fields (domain adapters hide via overrides)
4. `yamlTagName` re-implemented locally (5-line helper, conscious trade-off vs. storage API change)
5. `filterQuit` in editor prevents child widget `tea.Quit` from terminating parent
6. `Edit[T]` calls `tui.RunProgram` directly (not a TUI method) — avoids import cycle

## Gotchas

- `WalkFields` and `SetFieldValue` panic on nil/non-struct input (programming errors)
- `ApplyOverrides` panics on duplicate override paths
- `[]string` fields use comma-separated format — entries with commas will break
- `time.Duration` uses `time.ParseDuration` — accepts formats like `5m30s`, `1h`, `300ms`
