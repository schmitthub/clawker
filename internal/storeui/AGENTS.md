# Store UI Package

Generic orchestration layer for browsing and editing `storage.Store[T]` instances. Bridges typed stores (`internal/storage`) and terminal presentation (`internal/tui`). Domain adapters here edit packages built to the Store-backed package contract in `internal/storage/AGENTS.md`; read it before adding an adapter.

## Mental Model: Multi-Layer Config Editor

Store UI is a **config placement tool**, not an override editor. It gives users a unified view across all layer files so they can make informed decisions about where to place config values based on their project's directory structure.

**Layered inheritance**: Clawker configs use walk-up file discovery. A monorepo might have:
- `./clawker.yaml` — repo root config (cascades to all subdirs)
- `./frontend/.clawker.yaml` — frontend-specific overrides
- `~/.config/clawker/clawker.yaml` — user-level defaults

The same key in different layer files is **inheritance**, not duplication. Merge strategies (`union`, `override`) resolve how values combine across layers.

**The browser shows the merged state** — the effective config for the current working directory, with per-layer breakdown showing which file each value comes from. This is read-only context. When the user edits a field and picks a save target, they're writing to a specific layer file. The user might save a value to the repo root file knowing it won't affect their CWD (a closer layer wins) but will cascade to sibling directories.

**Validation guards writes, not editors.** Editors collect input freely. The write boundary (per-layer) is where validation happens, because that's where layer context is available. Don't put domain validation in TUI editors — they show merged state and can't know the user's intent until a layer is chosen.

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

A third state exists for completeness: a field whose value decodes into neither
its declared kind nor `any` renders as `<unreadable>` with the read error
appended to its description, and the row is forced `ReadOnly`. The read error is
never folded onto "unset" — a blank row invites the operator to save over data
the editor never showed them. Deleting the key (`d`) still works.

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

## When Adding a New Store Editor

1. **Domain adapter** under `internal/config/storeui/<domain>/` exports:
   - `Overrides() []storeui.Override` — TUI-only customizations (`Hidden`, `ReadOnly`, `Kind`, `Options`, `Order`). Labels/descriptions come from struct tags, not overrides.
   - `LayerTargets(store) ([]storeui.LayerTarget, error)` — where the user can save each field. Delegate to `storeui.BuildLayerTargets(store)`, which derives targets from the store's own `WriteTargets()` (walk-up target → "Project", configured dirs → "User", discovered layers → shortened path; each target carries `Filename` so the adapter can relabel filenames it recognizes, e.g. the project adapter's `Local` override file). Never hardcode locations the store cannot rediscover.
   - `Edit(ios, store) (storeui.Result, error)` — convenience wrapper that wires overrides + targets into `storeui.Edit[T]`.
2. **Cobra command** under `internal/cmd/<noun>/edit/` — thin wrapper: load config → get store → call domain `Edit` → print success/cancel. Nothing else belongs here.
3. **Wire into parent** — add `edit.NewCmdEdit(f, nil)` to the parent command's `AddCommand` list.
4. **Tests** — at minimum: `TestOverrides_AllPathsMatchFields` (prevents typo rot against `WalkFields(schema)`), a round-trip integration test that drives the store through `WalkFields → SetFieldValue → store.Set → store.Write → reload`, and a unit test for any non-trivial override decision.

## Override Quick Reference

- `Hidden: true` removes a field. Use prefix-based hiding — hiding `"build.instructions"` also hides `"build.instructions.env"`, `"build.instructions.root_run"`, etc.
- `ReadOnly: true` for fields managed by other systems (e.g., `host_proxy.*` ports)
- `Kind: storeui.KindSelect` + `Options: []string{...}` for enum-like fields
- `Order: N` to control sort position within a tab (lower = first)
- `ApplyOverrides` **panics on duplicate override paths** — catch that in tests

## Gotchas

- `WalkFields` and `SetFieldValue` panic on nil/non-struct input (programming errors)
- `ApplyOverrides` panics on duplicate override paths
- `[]string` fields use comma-separated format — entries with commas will break
- `time.Duration` uses `time.ParseDuration` — accepts formats like `5m30s`, `1h`, `300ms`
- `writeFieldToFile` uses atomic temp+rename; `enc.Close()` error is checked to prevent corrupt writes

## Store UI reference

> For the mental model and the store-editor checklist, see `internal/storeui/AGENTS.md`. This document holds the full architecture, data flow, test patterns, and gotchas.

### Architecture Overview

Store UI is the system for building interactive TUI editors for any `storage.Store[T]` instance. It has four layers:

```
Command layer (cmd/settings/edit, cmd/project/edit)
  → Domain adapter (config/storeui/settings, config/storeui/project)
    → Orchestration (internal/storeui)
      → Presentation (internal/tui — FieldBrowserModel, ListEditorModel, TextareaEditorModel)
      → Persistence (internal/storage — Store[T])
```

**Import boundary**: `storeui` does NOT import `bubbletea` or `bubbles`. All presentation is delegated to `internal/tui` via generic types (`BrowserField`, `BrowserConfig`, etc.). The `edit.go` file maps `storeui.FieldKind` → `tui.BrowserFieldKind` to keep the abstraction boundary clean.

### How to Build a New Store UI

#### Step 1: Domain Adapter

Create a package under `internal/config/storeui/<domain>/` that exports:

```go
// Overrides customizes reflected fields for interactive editing. Takes a
// config.Config only when the override set is config-derived (the project
// adapter needs it; the settings adapter does not).
func Overrides() []storeui.Override

// LayerTargets builds save destinations from the store's own write targets.
func LayerTargets(store *storage.Store[T]) ([]storeui.LayerTarget, error)

// Edit is the convenience entry point wiring overrides + targets.
func Edit(ios *iostreams.IOStreams, store *storage.Store[T]) (storeui.Result, error)
```

**Override patterns:**

- Set `Hidden: true` to remove fields the user shouldn't see (complex nested types like `map[string]string`, `[]struct`)
- Use prefix-based hiding: hiding path `"build.instructions"` also hides `"build.instructions.env"`, `"build.instructions.root_run"`, etc.
- Set `ReadOnly` for fields managed by other systems (e.g., `host_proxy.*` ports)
- Set `Kind` + `Options` for constrained fields (e.g., `workspace.default_mode` → `KindSelect` with `["bind", "snapshot"]`)
- Set `Label` and `Description` for human-friendly display text
- Set `Order` to control sort position within tabs (lower = first)

**LayerTarget patterns:**

- `BuildLayerTargets(store)` derives all targets from `store.WriteTargets()`: the walk-up target (the in-play walk-up layer for the write filename, or the CWD dual-placement candidate when none is discovered) is labeled "Project", configured-directory candidates "User", and discovered layers use their shortened path as label. Each target carries the store-reported `Filename`; domain adapters relabel filenames they recognize (the project adapter labels `clawker.local.yaml` layers `storeui.LabelLocal`)
- A store without walk-up (e.g. settings) gets no "Project" target — it could never rediscover a CWD file, so offering one would silently lose the saved value
- Use `ShortenHome()` for the Description field (exported from `internal/storeui`)

#### Step 2: Command Integration

Create a Cobra command under `internal/cmd/<noun>/edit/`:

```go
type EditOptions struct {
    IOStreams *iostreams.IOStreams
    Config   func() (config.Config, error)
}

func NewCmdSettingsEdit(f *cmdutil.Factory, runF func(context.Context, *EditOptions) error) *cobra.Command
// or for project:
func NewCmdProjectEdit(f *cmdutil.Factory, runF func(context.Context, *EditOptions) error) *cobra.Command
```

The run function:
1. Load config via `opts.Config()`
2. Get the store: `cfg.FooStore()` (or `cfg.SettingsStore()`, `cfg.ProjectStore()`)
3. Call domain adapter's `Edit(ios, store, cfg)`
4. Handle result: print success/cancel message

#### Step 3: Wire into Parent Command

Add `edit.NewCmdSettingsEdit(f, nil)` (or the noun-appropriate constructor) to the parent command's `AddCommand` list.

### Orchestration Layer (internal/storeui)

#### Data Flow

```
Edit[T storage.Schema](ios, store, opts...):
  1. schemaFields[T](store) → []Field: T.Fields() metadata (path/label/desc/kind/default/required)
     + one storage.Get[V] per declared leaf for the current merged value
     (ErrKeyNotFound = unset → value blank, default shown)
  2. ApplyOverrides(fields, overrides) → filtered + customized fields (TUI-specific only: Hidden, ReadOnly, Kind, Options)
  3. Map to tui types: fieldsToBrowserFields(), layersToBrowserLayers()
  4. Wire OnFieldSaved, OnFieldDeleted, and OnRefresh callbacks
  5. tui.NewFieldBrowser(cfg) → tui.RunProgram()
  6. Return Result{Saved, Cancelled, SavedCount}
```

There is no whole-struct snapshot anywhere in the flow — each field's value is
decoded individually from the merged tree.

#### Per-Field Save Flow

When a user edits a field and picks a save target:

1. Coerce the TUI string into the field's typed value via a fresh `T`: `SetFieldValue(&fresh, fieldPath, value)` then `GetFieldValue(&fresh, fieldPath)`
2. Stage it: `store.Set(fieldKey(fieldPath), typed)` — `fieldKey` splits the dotted schema path into segments. `Set` is unconditionally dirty, so saving to a non-provenance-winner layer needs no force-dirty step. An editor that produced no value (cleared map/struct) routes to `Remove` instead — `Set(key, nil)` is `ErrNilValue` by design.
3. `store.WriteFieldTo(target.Path, fieldKey(fieldPath)...)` — persist exactly this field to the chosen layer file; other staged fields stay staged.

`WriteFieldTo` internally remerges layers, so re-read values reflect the true merged state after each save. Deletes go through `store.Remove(key...)`, tolerating `ErrKeyNotFound` on an already-unset row.

#### Field Discovery (WalkFields)

Reflection-based struct walker. Type mapping:

| Go Type | FieldKind | Editor |
|---------|-----------|--------|
| `string` | `KindText` | TextareaEditorModel |
| `bool` | `KindBool` | SelectField (true/false) |
| `*bool` | `KindBool` | SelectField (nil → false display) |
| `int`, `int64` | `KindInt` | TextField |
| `[]string` | `KindStringSlice` | ListEditorModel |
| `time.Duration` | `KindDuration` | TextField |
| `map[string]string` | `KindMap` | KVEditorModel |
| `[]struct` | `KindStructSlice` | TextareaEditorModel (raw YAML) |
| `struct` | (recursed) | — |
| `*struct` | (recursed, nil → zero value) | — |
| consumer-defined kind | (via `KindFunc`) | Read-only (enforced by `fieldsToBrowserFields`) |
| unrecognized type | — | Falls back to `KindStructSlice` (`enrichWithSchema` overwrites kind from schema) |

Uses `yaml` struct tags for field naming. Falls back to lowercase field name.

**Extension model**: `classifyAndFormat` falls back to `KindStructSlice` for unrecognized types — this is expected when consumers register custom kinds via `KindFunc`. `enrichWithSchema` overwrites the kind from the authoritative schema metadata afterward. `fieldKindToBrowserKind` maps unrecognized `FieldKind` values to `BrowserStructSlice`, and `fieldsToBrowserFields` forces `ReadOnly = true` for consumer-defined kinds (`> KindLast`) to prevent data corruption via the raw textarea editor.

#### Reverse Reflection (SetFieldValue)

Sets a field on a struct pointer by dotted YAML path (`"build.image"` → `Build.Image`). Allocates nil `*struct` parents as it walks. Panics on non-pointer input.

#### Override Merging (ApplyOverrides)

- Non-nil override pointer fields replace original values
- `Hidden: true` removes the field (exact match + prefix-based for hiding entire subtrees)
- Unrecognized `FieldKind` values map to `BrowserStructSlice` (read-only) in `fieldKindToBrowserKind`
- Result sorted by `Order` (stable sort)
- Panics on duplicate override paths

### TUI Components

#### FieldBrowserModel (`tui/fieldbrowser.go`)

Domain-agnostic tabbed field browser. States: Browse → Edit → PickLayer → PickLayerDelete.

**Configuration**: `BrowserConfig` with `Title`, `Fields []BrowserField`, `LayerTargets []BrowserLayerTarget`, `Layers []BrowserLayer`, `OnFieldSaved func(path, value string, targetIdx int) error`, `OnFieldDeleted func(fieldPath string, targetIdx int) error`, `OnRefresh func() (fields []BrowserField, layers []BrowserLayer)`

**Features:**
- Fields grouped into tabs by top-level path key (e.g., "build", "security")
- Sub-section headings for 3+ segment paths
- Per-layer value breakdown when browsing (shows which layers define a value)
- Modified field tracking with count display
- Scroll management with auto-scroll to selection

**Key bindings:** `←/→` tabs, `↑/↓` navigate, `Enter` edit, `Esc/q/Ctrl+C` quit

#### ListEditorModel (`tui/listeditor.go`)

Manages `[]string` fields. Parses comma-separated input into items.

**Constructor:** `NewListEditor(label, value string, opts ...ListEditorOption)`
**Options:** `WithListValidator(fn func(string) error)` — external validator run on confirm
**Result:** `Value() string` (comma-separated), `IsConfirmed()`, `IsCancelled()`, `Err() string`
**Key bindings:** `a` add, `e` edit, `d/backspace` delete, `Enter` confirm list, `Esc` cancel

#### TextareaEditorModel (`tui/textareaeditor.go`)

Multiline text editor wrapping `bubbles/textarea`.

**Constructor:** `NewTextareaEditor(label, value string, opts ...TextareaEditorOption)` — auto-sizes height from content
**Options:** `WithTextareaValidator(fn func(string) error)` — external validator run on save (Ctrl+S)
**Result:** `Value() string`, `IsConfirmed()`, `IsCancelled()`, `Err() string`
**Key bindings:** `Ctrl+S` save, `Esc` cancel

### Storage API Used by Store UI

| Method | Purpose |
|--------|---------|
| `storage.Get[V](store, key...)` | Decode one field's merged value into V; `ErrKeyNotFound` = unset |
| `store.Keys(key...)` | Child key names (existence/enumeration) |
| `store.Set(key []string, value)` | Stage an in-memory field by segment key |
| `store.Remove(key...)` | Delete a key (the unset verb) |
| `store.WriteFieldTo(path, key...)` | Persist one dirty field to an explicit layer file |
| `store.Layers()` | All discovered layers (for layer breakdown display) |
| `store.WriteTargets()` | Candidate save locations derived from options + layers (for `LayerTargets`) |
| `store.ProvenanceMap()` | Display-form field keys → source file paths; drives the per-field source column (exact match, then parent path walk-up) |

### Testing Patterns

#### Unit Testing Overrides

Every domain adapter should test that override paths match real struct fields:

```go
func TestOverrides_AllPathsMatchFields(t *testing.T) {
    fields := storeui.WalkFields(config.MySchema{})
    fieldPaths := make(map[string]bool, len(fields))
    for _, f := range fields {
        fieldPaths[f.Path] = true
    }
    for _, ov := range Overrides() {
        assert.True(t, fieldPaths[ov.Path],
            "override path %q does not match any field", ov.Path)
    }
}
```

Also test for duplicate override paths and verify specific override properties (e.g., read-only fields).

#### Round-Trip Integration Tests

Test the full edit pipeline: WalkFields → SetFieldValue → store.Set → store.Write → reload → verify:

```go
func TestRoundTrip(t *testing.T) {
    env := testenv.New(t)
    store, dir := newTestStore[myStruct](t, env, initialYAML)

    // Edit through the plumbing (coerce the string, then set by path)
    var fresh myStruct
    require.NoError(t, storeui.SetFieldValue(&fresh, "field.path", "new-value"))
    typed, err := storeui.GetFieldValue(&fresh, "field.path")
    require.NoError(t, err)
    require.NoError(t, store.Set([]string{"field", "path"}, typed))
    require.NoError(t, store.Write())

    // Reload from disk — independent verification
    reloaded := reloadStore[myStruct](t, dir)
    got, err := storage.Get[string](reloaded, "field", "path")
    require.NoError(t, err)
    assert.Equal(t, "new-value", got)
}
```

Use `testenv.New(t)` for isolated XDG directories. Create stores with `storage.New[T](...)` + `WithFilenames` + `WithPaths` for filesystem-backed tests; `storage.NewFromString[T](yaml)` for in-memory fixtures.

#### Testing WalkFields

Verify walked fields match store reads and that field kinds are correct:

```go
func TestWalkFields_PathsMatchSchema(t *testing.T) {
    fields := storeui.WalkFields(myStruct{})
    // Assert field count, paths, kinds against the schema struct
    // (WalkFields reflects a value, not a store — the editor itself reads
    // values per field via storage.Get)
}
```

#### Testing FieldBrowserModel

The FieldBrowserModel is a BubbleTea model — test via `Init()` + `Update()` + `View()`:

```go
func TestFieldBrowser_TabNavigation(t *testing.T) {
    cfg := tui.BrowserConfig{
        Title:  "Test",
        Fields: []tui.BrowserField{...},
    }
    m := tui.NewFieldBrowser(cfg)
    m.Update(tea.KeyMsg{Type: tea.KeyRight})  // switch tab (pointer receiver mutates in-place)
    view := m.View()
    // Assert tab state, selected field, etc.
}
```

#### Testing ListEditorModel and TextareaEditorModel

```go
func TestListEditor_AddItem(t *testing.T) {
    m := tui.NewListEditor("packages", "git, curl")
    // Send 'a' key to add, type new item, press Enter
    m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
    // ... type and confirm
    assert.Equal(t, "git, curl, newpkg", m.Value())
}
```

### Gotchas

- `WalkFields` and `SetFieldValue` panic on nil or non-struct input — these are programming errors
- `ApplyOverrides` panics on duplicate override paths — catch in tests
- `[]string` fields use comma-separated format — entries containing commas will break the parser
- `time.Duration` uses `time.ParseDuration` — accepts `5m30s`, `1h`, `300ms` (standard Go duration)
- `*bool` fields: nil is treated as `false` for display; `SetFieldValue` allocates a non-nil pointer
- Unrecognized `FieldKind` values (consumer-defined kinds) are enforced as read-only in the browser — no editor exists for them
- `store.WriteFieldTo(path, key...)` persists exactly one dirty field to the target layer file (`WriteTo(path)` sends all of them); type coercion happens during `SetFieldValue`
- Provenance display uses exact field match + parent path walk-up for nested fields
