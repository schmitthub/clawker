# Store UI Reference

> For the essential rule (Mental Model + checklist when adding a field), see `.claude/rules/storeui.md`. This document holds the full architecture, data flow, test patterns, and gotchas.

## Architecture Overview

Store UI is the system for building interactive TUI editors for any `storage.Store[T]` instance. It has four layers:

```
Command layer (cmd/settings/edit, cmd/project/edit)
  → Domain adapter (config/storeui/settings, config/storeui/project)
    → Orchestration (internal/storeui)
      → Presentation (internal/tui — FieldBrowserModel, ListEditorModel, TextareaEditorModel)
      → Persistence (internal/storage — Store[T])
```

**Import boundary**: `storeui` does NOT import `bubbletea` or `bubbles`. All presentation is delegated to `internal/tui` via generic types (`BrowserField`, `BrowserConfig`, etc.). The `edit.go` file maps `storeui.FieldKind` → `tui.BrowserFieldKind` to keep the abstraction boundary clean.

## How to Build a New Store UI

### Step 1: Domain Adapter

Create a package under `internal/config/storeui/<domain>/` that exports:

```go
// Overrides customizes reflected fields for interactive editing.
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

- `BuildLayerTargets(store)` derives all targets from `store.WriteTargets()`: the walk-up CWD candidate (dual placement, `.clawker/` dir form or flat dotfile) is labeled "Local", configured-directory candidates "User", and discovered layers use their shortened paths as labels
- A store without walk-up (e.g. settings) gets no "Local" target — it could never rediscover a CWD file, so offering one would silently lose the saved value
- Use `ShortenHome()` for the Description field (exported from `internal/storeui`)

### Step 2: Command Integration

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

### Step 3: Wire into Parent Command

Add `edit.NewCmdSettingsEdit(f, nil)` (or the noun-appropriate constructor) to the parent command's `AddCommand` list.

## Orchestration Layer (internal/storeui)

### Data Flow

```
Edit[T storage.Schema](ios, store, opts...):
  1. store.Read() → *T snapshot
  2. WalkFields(snapshot) → []Field via reflection (runtime values)
  2b. enrichWithSchema(fields, snapshot.Fields()) → replace labels/descriptions/kinds/defaults with Schema struct tag metadata
  3. ApplyOverrides(fields, overrides) → filtered + customized fields (TUI-specific only: Hidden, ReadOnly, Kind, Options)
  4. Map to tui types: fieldsToBrowserFields(), layersToBrowserLayers()
  5. Wire OnFieldSaved, OnFieldDeleted, and OnRefresh callbacks
  6. tui.NewFieldBrowser(cfg) → tui.RunProgram()
  7. Return Result{Saved, Cancelled, SavedCount}
```

### Per-Field Save Flow

When a user edits a field and picks a save target:

1. Coerce the TUI string into the field's typed value via a fresh `T`: `SetFieldValue(&fresh, fieldPath, value)` then `GetFieldValue(&fresh, fieldPath)` — then `store.Set(fieldPath, typed)` updates in-memory
2. (conditional) `store.MarkForWrite(fieldPath)` — force-dirty the path when saving to a non-provenance-winner layer (i.e., the merged value is unchanged but the target layer file needs updating)
3. `store.Write(storage.ToPath(target.Path))` — persist dirty fields to the chosen layer file

`Write()` internally remerges layers, so the snapshot reflects the true merged state after each save. The TUI string is type-coerced before `Set` via `SetFieldValue`/`GetFieldValue`; deletes go through `store.Remove(path)`.

### Field Discovery (WalkFields)

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

### Reverse Reflection (SetFieldValue)

Sets a field on a struct pointer by dotted YAML path (`"build.image"` → `Build.Image`). Allocates nil `*struct` parents as it walks. Panics on non-pointer input.

### Override Merging (ApplyOverrides)

- Non-nil override pointer fields replace original values
- `Hidden: true` removes the field (exact match + prefix-based for hiding entire subtrees)
- Unrecognized `FieldKind` values map to `BrowserStructSlice` (read-only) in `fieldKindToBrowserKind`
- Result sorted by `Order` (stable sort)
- Panics on duplicate override paths

## TUI Components

### FieldBrowserModel (`tui/fieldbrowser.go`)

Domain-agnostic tabbed field browser. States: Browse → Edit → PickLayer → PickLayerDelete.

**Configuration**: `BrowserConfig` with `Title`, `Fields []BrowserField`, `LayerTargets []BrowserLayerTarget`, `Layers []BrowserLayer`, `OnFieldSaved func(path, value string, targetIdx int) error`, `OnFieldDeleted func(fieldPath string, targetIdx int) error`, `OnRefresh func() (fields []BrowserField, layers []BrowserLayer)`

**Features:**
- Fields grouped into tabs by top-level path key (e.g., "build", "security")
- Sub-section headings for 3+ segment paths
- Per-layer value breakdown when browsing (shows which layers define a value)
- Modified field tracking with count display
- Scroll management with auto-scroll to selection

**Key bindings:** `←/→` tabs, `↑/↓` navigate, `Enter` edit, `Esc/q/Ctrl+C` quit

### ListEditorModel (`tui/listeditor.go`)

Manages `[]string` fields. Parses comma-separated input into items.

**Constructor:** `NewListEditor(label, value string, opts ...ListEditorOption)`
**Options:** `WithListValidator(fn func(string) error)` — external validator run on confirm
**Result:** `Value() string` (comma-separated), `IsConfirmed()`, `IsCancelled()`, `Err() string`
**Key bindings:** `a` add, `e` edit, `d/backspace` delete, `Enter` confirm list, `Esc` cancel

### TextareaEditorModel (`tui/textareaeditor.go`)

Multiline text editor wrapping `bubbles/textarea`.

**Constructor:** `NewTextareaEditor(label, value string, opts ...TextareaEditorOption)` — auto-sizes height from content
**Options:** `WithTextareaValidator(fn func(string) error)` — external validator run on save (Ctrl+S)
**Result:** `Value() string`, `IsConfirmed()`, `IsCancelled()`, `Err() string`
**Key bindings:** `Ctrl+S` save, `Esc` cancel

## Storage API Used by Store UI

| Method | Purpose |
|--------|---------|
| `store.Read()` | Get immutable `*T` snapshot |
| `store.Get(path, out)` | Decode an in-memory field by dotted path into a typed `out` |
| `store.Set(path, value)` | Set an in-memory field by dotted path |
| `store.Remove(path)` | Remove a dotted path from tree + re-publish snapshot |
| `store.Layers()` | All discovered layers (for layer breakdown display) |
| `store.Provenance(path)` | Which layer won a specific field |
| `store.ProvenanceMap()` | All fields → source file paths |
| `store.MarkForWrite(path)` | Force-mark a dotted path as dirty so it is included in the next `Write` |
| `store.Write(storage.ToPath(path))` | Write to explicit absolute path |

## Testing Patterns

### Unit Testing Overrides

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

### Round-Trip Integration Tests

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
    require.NoError(t, store.Set("field.path", typed))
    require.NoError(t, store.Write())

    // Reload from disk — independent verification
    fresh := reloadStore[myStruct](t, dir)
    got := fresh.Read()
    assert.Equal(t, "new-value", got.Field.Path)
}
```

Use `testenv.New(t)` for isolated XDG directories. Create stores with `storage.New[T]("", ...)` + `WithFilenames` + `WithPaths` for filesystem-backed tests.

### Testing WalkFields

Verify walked fields match store reads and that field kinds are correct:

```go
func TestWalkFields_MatchesStoreRead(t *testing.T) {
    store, _ := newTestStore[myStruct](t, env, yaml)
    snap := store.Read()
    fields := storeui.WalkFields(snap)
    // Assert field count, paths, values, kinds
}
```

### Testing FieldBrowserModel

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

### Testing ListEditorModel and TextareaEditorModel

```go
func TestListEditor_AddItem(t *testing.T) {
    m := tui.NewListEditor("packages", "git, curl")
    // Send 'a' key to add, type new item, press Enter
    m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
    // ... type and confirm
    assert.Equal(t, "git, curl, newpkg", m.Value())
}
```

## Gotchas

- `WalkFields` and `SetFieldValue` panic on nil or non-struct input — these are programming errors
- `ApplyOverrides` panics on duplicate override paths — catch in tests
- `[]string` fields use comma-separated format — entries containing commas will break the parser
- `time.Duration` uses `time.ParseDuration` — accepts `5m30s`, `1h`, `300ms` (standard Go duration)
- `*bool` fields: nil is treated as `false` for display; `SetFieldValue` allocates a non-nil pointer
- Unrecognized `FieldKind` values (consumer-defined kinds) are enforced as read-only in the browser — no editor exists for them
- `store.Write(storage.ToPath(...))` persists dirty fields to the target layer file; type coercion happens during `SetFieldValue`
- Provenance display uses exact field match + parent path walk-up for nested fields
