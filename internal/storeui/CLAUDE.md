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
| `reflect.go` | `WalkFields(v)` — reflection-based struct walker (consumer-facing; the editor no longer uses it) |
| `value.go` | `SetFieldValue(v, path, val)` / `GetFieldValue(v, path)` — string ↔ typed value coercion via a fresh `T` |
| `edit.go` | `Edit[T](ios, store, opts...)` — orchestration entry point, field rendering (`schemaFields`), `LayerTarget`, `Result`, shared helpers |


## Public API

### Types

```go
type FieldKind = storage.FieldKind  // Alias; constants: KindText, KindBool, KindSelect, KindInt, KindStringSlice, KindDuration, KindMap, KindStructSlice, KindLast
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
func BuildBrowser[T storage.Schema](store *storage.Store[T], opts ...Option) (*tui.FieldBrowserModel, error) // Build the model without running the program (for embedding inside larger TUIs)
func WithTitle(title string) Option
func WithOverrides(overrides []Override) Option
func WithSkipPaths(paths ...string) Option
func WithOnlyPaths(paths ...string) Option                // Inverse of skip — restrict to listed paths
func WithLayerTargets(targets []LayerTarget) Option

// Shared helpers (used by domain adapters)
func ShortenHome(path string) string                     // Replace $HOME with ~
func BuildLayerTargets[T storage.Schema](store *storage.Store[T]) ([]LayerTarget, error) // Save targets from store.WriteTargets(): walk-up target → "Project", dir candidates → "User", layers → shortened path; targets carry Filename for domain relabeling
func Ptr[T any](v T) *T                                 // Pointer helper for Override fields
```

## Domain Adapters

| Package | Schema | Purpose |
|---------|--------|---------|
| `config/storeui/settings` | `config.Settings` | host_proxy read-only |
| `config/storeui/project` | `config.Project` | workspace mode as Select; maps use KV editor |

Each adapter exports `Overrides()`, `LayerTargets(store) ([]storeui.LayerTarget, error)`, and an `Edit(...) (storeui.Result, error)` entry point. The project adapter additionally takes `config.Config` on both (`Overrides(cfg)`, `Edit(ios, cfg, store)`) because its overrides are config-derived; settings takes neither (`Overrides()`, `Edit(ios, store)`). Targets come from the store's own `WriteTargets()` — a store without walk-up (settings) never offers a CWD "Project" target it could not read back.

## Data Flow

```
Edit[T](ios, store, opts...) = BuildBrowser[T](store, opts...) + tui.RunProgram:
  1. Validate layer targets (absolute paths)
  2. schemaFields(store): T.Fields() metadata + one storage.Get[V] per declared leaf
     (there is NO whole-struct read — Get requires at least one key segment)
  3. Filter skip/only paths, ApplyOverrides (domain overrides — TUI-specific only)
  4. fieldsToBrowserFields() → []tui.BrowserField (kind → widget mapping)
  5. tui.NewFieldBrowser(cfg) → tui.RunProgram (presentation)
  6. OnFieldSaved per field: stageFieldValue (Set, or Remove when the editor
     produced nothing) + store.WriteFieldTo(target.Path, key...)
  7. OnFieldDeleted per field: store.Remove(key...) + store.WriteFieldTo(target.Path, key...)
  8. Return Result (Saved, SavedCount)
```

### FieldKind → decode shape

`fieldValue` picks the Go type each field's merged YAML is decoded into; the
browse summary and the editor blob both come off that decode.

| FieldKind | `storage.Get[V]` | Browse value | EditValue |
|-----------|------------------|--------------|-----------|
| KindText, KindSelect | `string` | the value | — |
| KindBool | `bool` | `true`/`false` | — |
| KindInt | `int64` | decimal | — |
| KindDuration | `time.Duration` | `5m0s` | — |
| KindTime | `time.Time` | RFC3339Nano (zero → blank) | — |
| KindStringSlice | `[]string` | `a, b` | — |
| KindMap | `map[string]string` | `N entries` | sorted YAML |
| KindStructSlice | `[]any` | `N items` | YAML |
| KindStructMap, consumer kinds (`> KindLast`) | `any` | `N entries`/`N items` | YAML |

### Unset vs set-empty

The engine distinguishes an unset key (absent or bare `key:` → `ErrKeyNotFound`)
from an explicit empty (`""`, `[]` → a real value that masks lower layers), and
the browser reflects it: an unset field renders blank and keeps its schema
`Default`, which the field browser shows as `<default> (default)`; a set-empty
field renders blank with `Default` cleared, because no default is in effect.
On save, an emptied scalar or list is staged as that explicit empty; an editor
that produced nothing at all (a cleared map/struct blob → a nil value that
`storage.Set` rejects) is routed to `store.Remove` instead — the one unset verb.

## Key Design Decisions

1. `KindTriState` deprecated and mapped to `KindBool` — retained for backward compatibility
2. Consumer-defined `FieldKind` values (`> KindLast`) map to `BrowserStructSlice` and are forced `ReadOnly = true` by `fieldsToBrowserFields`
3. Nil `*struct` recursion in `WalkFields` — produces zero-value fields (domain adapters hide via overrides)
4. `yamlTagName` re-implemented locally (5-line helper, conscious trade-off vs. storage API change)
4b. Schema metadata (label, description, default, kind, required) comes straight from `T.Fields()`; `WalkFields`/`enrichWithSchema` are no longer on the edit path (`WalkFields` stays for consumers validating override paths)
5. `LayerTarget.Path` is the destination for `store.WriteFieldTo` — only the saved field is flushed, so unrelated staged state (e.g. a preset store's seed marks) never lands in the chosen target file
6. Type mapping between `storeui.FieldKind` and `tui.BrowserFieldKind` happens in `edit.go` — tui knows nothing about storeui types
7. `KindMap` → `BrowserMap` → `KVEditorModel` (interactive key-value pair editor); `KindStructSlice` → `BrowserStructSlice` → `TextareaEditorModel` (raw YAML)
8. Per-field save model: each edit is persisted immediately via layer picker → `onFieldSaved` callback. No batch save. `Edit` is `BuildBrowser` + `tui.RunProgram` — one wiring, not two.
9. Per-field delete: `d` key in browse state → layer picker → `onFieldDeleted` callback. Removes key from YAML file and in-memory tree via `store.Remove`. Lets lower-priority layer values show through.

## Gotchas

- `WalkFields` and `SetFieldValue` panic on nil/non-struct input (programming errors)
- `ApplyOverrides` panics on duplicate override paths
- `[]string` fields use comma-separated format — entries with commas will break
- `time.Duration` uses `time.ParseDuration` — accepts formats like `5m30s`, `1h`, `300ms`
- `writeFieldToFile` uses atomic temp+rename; `enc.Close()` error is checked to prevent corrupt writes
