# Store UI Rules

**Applies to**: `internal/storeui/**`, `internal/config/storeui/**`, `internal/tui/fieldbrowser*`, `internal/tui/listeditor*`, `internal/tui/textareaeditor*`

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

// LayerTargets builds save destinations from discovered store layers.
func LayerTargets(store *storage.Store[T], cfg config.Config) []storeui.LayerTarget

// Edit is the convenience entry point wiring overrides + targets.
func Edit(ios *iostreams.IOStreams, store *storage.Store[T], cfg config.Config) (storeui.Result, error)
```

**Override patterns:**

- Set `Hidden: true` to remove fields the user shouldn't see (complex nested types like `map[string]string`, `[]struct`)
- Use prefix-based hiding: hiding path `"build.instructions"` also hides `"build.instructions.env"`, `"build.instructions.root_run"`, etc.
- Set `ReadOnly` for fields managed by other systems (e.g., `host_proxy.*` ports)
- Set `Kind` + `Options` for constrained fields (e.g., `workspace.default_mode` → `KindSelect` with `["bind", "snapshot"]`)
- Set `Label` and `Description` for human-friendly display text
- Set `Order` to control sort position within tabs (lower = first)

**LayerTarget patterns:**

- Check for `.clawker/` directory existence to decide between dir-form and flat-form local paths
- Label targets descriptively: "Local", "User", "Original", "Project"
- Use `shortenHome()` (or equivalent) for the Description field
- Include `store.Layers()` entries as "Original" targets so users can save back to the file a value came from

### Step 2: Command Integration

Create a Cobra command under `internal/cmd/<noun>/edit/`:

```go
type EditOptions struct {
    IOStreams *iostreams.IOStreams
    Config   func() (config.Config, error)
}

func NewCmdEdit(f *cmdutil.Factory, runF func(context.Context, *EditOptions) error) *cobra.Command
```

The run function:
1. Load config via `opts.Config()`
2. Get the store: `cfg.FooStore()` (or `cfg.SettingsStore()`, `cfg.ProjectStore()`)
3. Call domain adapter's `Edit(ios, store, cfg)`
4. Handle result: print success/cancel message

### Step 3: Wire into Parent Command

Add `edit.NewCmdEdit(f, nil)` to the parent command's `AddCommand` list.

## Orchestration Layer (internal/storeui)

### Data Flow

```
Edit[T storage.Schema](ios, store, opts...):
  1. store.Read() → *T snapshot
  2. WalkFields(snapshot) → []Field via reflection (runtime values)
  2b. enrichWithSchema(fields, snapshot.Fields()) → replace labels/descriptions/kinds with Schema struct tag metadata
  3. ApplyOverrides(fields, overrides) → filtered + customized fields (TUI-specific only: Hidden, ReadOnly, Kind, Options)
  4. Map to tui types: fieldsToBrowserFields(), layersToBrowserLayers()
  5. Wire OnFieldSaved callback
  6. tui.NewFieldBrowser(cfg) → tui.RunProgram()
  7. Return Result{Saved, Cancelled, Modified}
```

### Per-Field Save Flow

When a user edits a field and picks a save target:

1. `store.Set(func(t *T) { SetFieldValue(t, fieldPath, value) })` — update in-memory
2. `writeFieldToFile(targetPath, fieldPath, value)` — persist single field to the chosen file

`writeFieldToFile` reads existing YAML (or starts empty), builds a nested map from the dotted path, merges it into the existing map, and writes atomically (temp + rename). Values are coerced to appropriate YAML types (bool, int, `[]string`) via `coerceYAMLValue`.

### Field Discovery (WalkFields)

Reflection-based struct walker. Type mapping:

| Go Type | FieldKind | Editor |
|---------|-----------|--------|
| `string` | `KindText` | TextField |
| `bool` | `KindBool` | SelectField (true/false) |
| `*bool` | `KindBool` | SelectField (nil → false display) |
| `int`, `int64` | `KindInt` | TextField |
| `[]string` | `KindStringSlice` | ListEditorModel |
| `time.Duration` | `KindDuration` | TextField |
| `struct` | (recursed) | — |
| `*struct` | (recursed, nil → zero value) | — |
| everything else | `KindComplex` | Read-only display |

Uses `yaml` struct tags for field naming. Falls back to lowercase field name.

### Reverse Reflection (SetFieldValue)

Sets a field on a struct pointer by dotted YAML path (`"build.image"` → `Build.Image`). Allocates nil `*struct` parents as it walks. Panics on non-pointer input.

### Override Merging (ApplyOverrides)

- Non-nil override pointer fields replace original values
- `Hidden: true` removes the field (exact match + prefix-based for hiding entire subtrees)
- `KindComplex` fields are auto-enforced as `ReadOnly`
- Result sorted by `Order` (stable sort)
- Panics on duplicate override paths

## TUI Components

### FieldBrowserModel (`tui/fieldbrowser.go`)

Domain-agnostic tabbed field browser. States: Browse → Edit → PickLayer.

**Configuration**: `BrowserConfig` with `Title`, `Fields []BrowserField`, `LayerTargets []BrowserLayerTarget`, `Layers []BrowserLayer`, `OnFieldSaved func(path, value string, targetIdx int) error`

**Features:**
- Fields grouped into tabs by top-level path key (e.g., "build", "security")
- Sub-section headings for 3+ segment paths
- Per-layer value breakdown when browsing (shows which layers define a value)
- Modified field tracking with count display
- Scroll management with auto-scroll to selection

**Key bindings:** `←/→` tabs, `↑/↓` navigate, `Enter` edit, `Esc/q/Ctrl+C` quit

### ListEditorModel (`tui/listeditor.go`)

Manages `[]string` fields. Parses comma-separated input into items.

**Constructor:** `NewListEditor(label, value string)`
**Result:** `Value() string` (comma-separated), `IsConfirmed()`, `IsCancelled()`
**Key bindings:** `a` add, `e` edit, `d/backspace` delete, `Enter` confirm list, `Esc` cancel

### TextareaEditorModel (`tui/textareaeditor.go`)

Multiline text editor wrapping `bubbles/textarea`.

**Constructor:** `NewTextareaEditor(label, value string)` — auto-sizes height from content
**Result:** `Value() string`, `IsConfirmed()`, `IsCancelled()`
**Key bindings:** `Ctrl+S` save, `Esc` cancel

## Storage API Used by Store UI

| Method | Purpose |
|--------|---------|
| `store.Read()` | Get immutable `*T` snapshot |
| `store.Set(func(*T))` | Mutate in-memory via closure |
| `store.Layers()` | All discovered layers (for layer breakdown display) |
| `store.Provenance(path)` | Which layer won a specific field |
| `store.ProvenanceMap()` | All fields → source file paths |
| `store.WriteTo(path)` | Write to explicit absolute path |

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

    // Edit through the plumbing
    require.NoError(t, store.Set(func(s *myStruct) {
        require.NoError(t, storeui.SetFieldValue(s, "field.path", "new-value"))
    }))
    require.NoError(t, store.Write())

    // Reload from disk — independent verification
    fresh := reloadStore[myStruct](t, dir)
    got := fresh.Read()
    assert.Equal(t, "new-value", got.Field.Path)
}
```

Use `testenv.New(t)` for isolated XDG directories. Create stores with `storage.NewStore[T]` + `WithFilenames` + `WithPaths` for filesystem-backed tests.

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
    m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})  // switch tab
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
- `KindComplex` is always enforced as read-only — even if an override sets `ReadOnly: false`
- `writeFieldToFile` coerces string values to YAML types (bool, int, list) before writing
- Provenance display uses exact field match + parent path walk-up for nested fields
