# Store UI Package

Generic orchestration layer for browsing and editing `storage.Store[T]` instances. Bridges typed stores (`internal/storage`) and terminal presentation (`internal/tui`).

## Architecture

```
cmd/settings/edit, cmd/project/edit
  → config/storeui/settings, config/storeui/project  (domain adapters)
    → internal/storeui                                (orchestration)
      → internal/tui (FieldBrowserModel, widgets, RunProgram)
      → internal/storage (Store[T] API)
```

**Import boundary**: storeui does NOT import `bubbletea` or `bubbles` directly. All presentation is delegated to `internal/tui` via generic widget types. storeui owns the reflection-based field discovery, override merging, type mapping, and the read→edit→set→write lifecycle.

## Files

| File | Purpose |
|------|---------|
| `field.go` | `FieldKind`, `Field`, `Override`, `ApplyOverrides` — core types |
| `reflect.go` | `WalkFields(v)` — reflection-based struct walker |
| `value.go` | `SetFieldValue(v, path, val)` — reverse reflection writer |
| `edit.go` | `Edit[T](ios, store, opts...)` — orchestration entry point, `LayerTarget`, `Result`, shared helpers |

## Public API

### Types

```go
type FieldKind = storage.FieldKind  // Alias; constants: KindText, KindBool, KindSelect, KindInt, KindStringSlice, KindDuration, KindMap, KindComplex
// KindTriState is deprecated — maps to KindBool, retained for backward compatibility

type Field struct {
    Path, Label, Description string
    Kind        FieldKind
    Value       string
    Default     string
    Options     []string
    Validator   func(string) error
    Required, ReadOnly bool
    Order       int
}

type Override struct {
    Path        string
    Label, Description, Default *string
    Kind        *FieldKind
    Options     []string
    Validator   func(string) error
    Required, ReadOnly *bool
    Order       *int
    Hidden      bool
}

type LayerTarget struct { Label, Description, Path string }
type Result struct { Saved, Cancelled bool; SavedCount int }
type Option func(*editOptions)
```

### Functions

```go
func WalkFields(v any) []Field                           // Reflect struct → fields
func SetFieldValue(v any, path string, val string) error // Set field by dotted path
func ApplyOverrides(fields []Field, overrides []Override) []Field

func Edit[T storage.Schema](ios *iostreams.IOStreams, store *storage.Store[T], opts ...Option) (Result, error)
func WithTitle(title string) Option
func WithOverrides(overrides []Override) Option
func WithSkipPaths(paths ...string) Option
func WithLayerTargets(targets []LayerTarget) Option

// Shared helpers (used by domain adapters)
func ShortenHome(path string) string                     // Replace $HOME with ~
func ResolveLocalPath(cwd, filename string) string       // Dual-placement CWD dot-file
func Ptr[T any](v T) *T                                 // Pointer helper for Override fields
```

## Domain Adapters

| Package | Schema | Purpose |
|---------|--------|---------|
| `config/storeui/settings` | `config.Settings` | host_proxy read-only |
| `config/storeui/project` | `config.Project` | workspace mode as Select; maps use KV editor |

Each adapter exports `Overrides() []storeui.Override`, `LayerTargets(store, cfg) []storeui.LayerTarget`, and `Edit(ios, store, cfg) (storeui.Result, error)`.

## Data Flow

```
Edit[T](ios, store, opts...):
  1. Validate layer targets (absolute paths)
  2. store.Read() → *T snapshot
  3. WalkFields(snapshot) → []Field (reflection + runtime values)
  3b. enrichWithSchema(fields, snapshot.Fields()) — replace labels/descriptions/kinds with schema metadata
  4. Filter skip paths, ApplyOverrides (domain overrides — TUI-specific only)
  5. fieldsToBrowserFields() → []tui.BrowserField (type mapping)
  6. tui.NewFieldBrowser(cfg) → tui.RunProgram (presentation)
  7. OnFieldSaved callback per field: store.Set(SetFieldValue...) + writeFieldToFile(target)
  7b. OnFieldDeleted callback per field: store.DeleteKey(path) + deleteFieldFromFile(target, path)
  8. Return Result (Saved, SavedCount)
```

## Key Design Decisions

1. `KindTriState` deprecated and mapped to `KindBool` — retained for backward compatibility
2. `KindComplex` auto-enforces `ReadOnly` in `ApplyOverrides`
3. Nil `*struct` recursion in `WalkFields` — produces zero-value fields (domain adapters hide via overrides)
4. `yamlTagName` re-implemented locally (5-line helper, conscious trade-off vs. storage API change)
5. `LayerTarget.Path` used by `writeFieldToFile()` for direct per-field YAML writes to the chosen target file
6. Type mapping between `storeui.FieldKind` and `tui.BrowserFieldKind` happens in `edit.go` — tui knows nothing about storeui types
7. `KindMap` → `BrowserMap` → `KVEditorModel` (interactive key-value pair editor); `KindStructSlice` → `BrowserStructSlice` → `TextareaEditorModel` (raw YAML)
8. Per-field save model: each edit is persisted immediately via layer picker → `onFieldSaved` callback. No batch save.
9. Per-field delete: `d` key in browse state → layer picker → `onFieldDeleted` callback. Removes key from YAML file and in-memory tree via `store.DeleteKey`. Lets lower-priority layer values show through.

## Gotchas

- `WalkFields` and `SetFieldValue` panic on nil/non-struct input (programming errors)
- `ApplyOverrides` panics on duplicate override paths
- `[]string` fields use comma-separated format — entries with commas will break
- `time.Duration` uses `time.ParseDuration` — accepts formats like `5m30s`, `1h`, `300ms`
- `writeFieldToFile` uses atomic temp+rename; `enc.Close()` error is checked to prevent corrupt writes
