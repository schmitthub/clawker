# StoreUI and Wizard Systems - Comprehensive Analysis

## Overview

Clawker has two powerful TUI systems that can be composed for guided config experiences:

1. **StoreUI** - Generic config editor framework (read → edit → save to layer)
2. **Wizard** - Multi-step form with conditional field skipping
3. **TUI Building Blocks** - Reusable components (SelectField, TextField, ConfirmField, stepper bar, etc.)

## StoreUI System

### Purpose
Generic orchestration layer for browsing and editing `storage.Store[T]` instances. Bridges typed stores (internal/storage) and terminal presentation (internal/tui).

### Architecture

```
Command layer (cmd/settings/edit, cmd/project/edit)
  → Domain adapter (config/storeui/settings, config/storeui/project)
    → Orchestration (internal/storeui.Edit)
      → Presentation (internal/tui.FieldBrowserModel)
      → Persistence (internal/storage.Store[T])
```

### Core Files

1. **`internal/storeui/edit.go`** - Main orchestration
   - `Edit[T](ios, store, opts...)` - Entry point
   - `Result{Saved, Cancelled, SavedCount}` - Return type
   - `LayerTarget{Label, Description, Path}` - Save destinations
   - Options: `WithTitle`, `WithOverrides`, `WithSkipPaths`, `WithLayerTargets`
   - Helpers: `ShortenHome`, `ResolveLocalPath`, `BuildLayerTargets`, `Ptr`

2. **`internal/storeui/field.go`** - Field types and overrides
   - `Field` struct - represents editable field
   - `Override` struct - customize field behavior per path
   - `ApplyOverrides(fields, overrides)` - merge overrides with filtering/sorting
   - `FieldKind` constants: `KindText`, `KindBool`, `KindSelect`, `KindInt`, `KindStringSlice`, `KindDuration`, `KindMap`, `KindStructSlice`

3. **`internal/storeui/reflect.go`** - Field discovery via reflection
   - `WalkFields(v any)` - reflects struct → fields (runtime values)
   - Maps Go types to FieldKind (string→Text, bool→Bool, time.Duration→Duration, []string→StringSlice, map[string]string→Map, []struct→StructSlice)
   - Handles nested structs and *struct pointers
   - Returns fields with current runtime values

4. **`internal/storeui/value.go`** - Field mutation via reflection
   - `SetFieldValue(v any, path string, val string)` - set field by dotted path
   - Type-aware conversion: parses bool, int, duration, comma-separated strings, YAML for maps/slices
   - Allocates nil pointer-to-struct parents as it walks
   - Returns error on type mismatch or invalid input

### StoreUI Data Flow

```
Edit[T](ios, store, opts...):
  1. Validate layer targets (absolute paths)
  2. store.Read() → *T snapshot
  3. WalkFields(snapshot) → []Field (reflection + runtime values)
  3b. enrichWithSchema(fields, snapshot.Fields()) → replace labels/descriptions/kinds with schema metadata
  4. Filter skip paths, ApplyOverrides (domain overrides — TUI-specific only)
  5. fieldsToBrowserFields() → []tui.BrowserField (type mapping)
  6. tui.NewFieldBrowser(cfg) → tui.RunProgram (presentation)
  7. OnFieldSaved callback per field: store.Set(SetFieldValue...) + store.Write(storage.ToPath(target.Path))
  7b. OnFieldDeleted callback per field: store.Delete(path) + store.Write(storage.ToPath(target.Path))
  8. Return Result (Saved, SavedCount)
```

### Domain Adapters

Located in `internal/config/storeui/{project,settings}/`.

Each adapter exports three functions:
- `Overrides() []storeui.Override` - TUI-specific customizations (kind, options, read-only flags)
- `LayerTargets(store, cfg) []storeui.LayerTarget` - save destination paths
- `Edit(ios, store, cfg) (storeui.Result, error)` - convenience entry point

**Example: Project Adapter** (`internal/config/storeui/project/project.go`)
```go
func Overrides() []storeui.Override {
    return []storeui.Override{
        {Path: "workspace.default_mode",
            Kind: storeui.Ptr(storeui.KindSelect), Options: []string{"bind", "snapshot"}},
        {Path: "agent.claude_code.config.strategy",
            Kind: storeui.Ptr(storeui.KindSelect), Options: []string{"copy", "fresh"}},
    }
}

func LayerTargets(store, cfg) []storeui.LayerTarget {
    return storeui.BuildLayerTargets(cfg.ProjectConfigFileName(), config.ConfigDir(), store.Layers())
}
```

### Key Design Decisions

1. **Schema metadata from struct tags** - `desc`, `label`, `default`, `required` tags are single source of truth
2. **Per-field save model** - each edit is persisted immediately to user-chosen layer
3. **Layer targets always shown** - even layers that don't currently define a field (allows user to understand inheritance)
4. **Per-layer validation** - validation guards writes, not editors; only write boundary knows layer context
5. **Consumer-defined kinds are read-only** - kinds > KindLast get forced to read-only to prevent data loss
6. **No hardcoded YAML templates** - defaults come from struct tags via `GenerateDefaultsYAML[T]()`

### Storage Integration

- **Read snapshots** - `store.Read()` returns immutable `*T` (lock-free atomic load)
- **Mutation** - `store.Set(func(*T) error)` deep-copies, mutates, re-merges, atomically swaps
- **Persistence** - `store.Write(storage.ToPath(path))` writes dirty fields to specific file
- **Provenance** - `store.Provenance(path)` and `store.ProvenanceMap()` show which layer owns each field
- **Layered inheritance** - merged state is read-only view; writes target specific layer files

---

## Wizard System

### Purpose
Multi-step form with conditional field skipping, stepper bar progress indicator, and per-step help text.

### Core Types

Located in `internal/tui/wizard.go`:

```go
type WizardFieldKind int
const (
    FieldSelect WizardFieldKind = iota  // Arrow-key selection
    FieldText                           // Text input
    FieldConfirm                        // Yes/no toggle
)

type WizardField struct {
    ID           string                    // Unique field ID
    Title        string                    // StepperBar label
    Prompt       string                    // Question text
    Kind         WizardFieldKind
    
    // Select-specific
    Options      []FieldOption
    DefaultIdx   int
    
    // Text-specific
    Placeholder  string
    Default      string
    Validator    func(string) error
    Required     bool
    
    // Confirm-specific
    DefaultYes   bool
    
    // Conditional: skip when predicate returns true
    SkipIf       func(WizardValues) bool
}

type WizardValues map[string]string
type WizardResult struct {
    Values    WizardValues
    Submitted bool
}
```

### Entry Point

```go
// On TUI struct
func (t *TUI) RunWizard(fields []WizardField) (WizardResult, error)
```

### Navigation & Flow

- **Forward**: Enter key advances to next visible step
- **Backward**: Esc key goes to previous step, or cancels on first step
- **Quit**: Ctrl+C cancels at any time
- **Conditional skipping**: `SkipIf` predicate evaluated with current `WizardValues` at each navigation
- **Validation**: Per-field (TextFields have optional validator); runs on Enter before advancing

### Wizard Model Implementation Details

Internal `wizardModel` maintains:
- `fields []WizardField` - definitions
- `currentStep int` - active step index
- `values WizardValues` - accumulated answers
- Per-field instances: `selectFields map[int]SelectField`, `textFields map[int]TextField`, `confirmFields map[int]ConfirmField`
- `submitted`, `cancelled` - termination states
- `width`, `height` - terminal dimensions

**Key methods:**
- `nextVisibleStep(from int)` - find next non-skipped step
- `prevVisibleStep(from int)` - find previous non-skipped step
- `isStepSkipped(idx int)` - check SkipIf predicate
- `activateStep(idx int)` - move to step, recreate field if going backward
- `confirmAndAdvance(idx int)` - store value, move to next step or quit

### View Components

**StepperBar** (top of wizard):
- Shows all steps with state icons: `○` pending, `◉` active, `✓` complete, `-` skipped
- Uses `RenderStepperBar(steps []Step, width)` from `stepper.go`

**Field Rendering** (middle):
- Delegates to current field's View() method (SelectField, TextField, or ConfirmField)

**Help Bar** (bottom):
- Context-sensitive help showing key bindings for current field kind
- "↑↓ select  enter confirm  esc back  ctrl+c quit" (example for FieldSelect)

---

## TUI Building Blocks

### Field Models (`internal/tui/fields.go`)

All use **value semantics** (immutable, setters return copies).

#### SelectField
```go
NewSelectField(id, prompt string, options []FieldOption, defaultIdx int) SelectField

// Interaction
field.Update(msg tea.Msg) (SelectField, tea.Cmd)
field.View() string

// Accessors
field.Value() string           // Label of selected option
field.SelectedIndex() int
field.IsConfirmed() bool
field.SetSize(w, h)            // Update available space
```

- Arrow keys navigate, Enter confirms
- Compact single-line per-option layout with label + description
- Selected item highlighted with color

#### TextField
```go
NewTextField(id, prompt string, opts ...TextFieldOption) TextField
  // TextFieldOptions:
  WithPlaceholder(s string)
  WithDefault(s string)
  WithValidator(fn func(string) error)
  WithRequired()

// Interaction
field.Update(msg tea.Msg) (TextField, tea.Cmd)
field.View() string

// Accessors
field.Value() string           // Current input text
field.IsConfirmed() bool
field.Err() string             // Validation error, if any
field.SetSize(w, h)            // Update available space
```

- Type text, Enter confirms
- Validation runs on Enter; if error, displays message and stays in field
- Required validator auto-included via WithRequired()
- Custom validator runs after required check

#### ConfirmField
```go
NewConfirmField(id, prompt string, defaultYes bool) ConfirmField

// Interaction
field.Update(msg tea.Msg) (ConfirmField, tea.Cmd)
field.View() string

// Accessors
field.Value() string           // "yes" or "no"
field.BoolValue() bool
field.IsConfirmed() bool
field.SetSize(w, h)            // (unused, fixed layout)
```

- Left/Right or Tab toggles value
- y/n keys set directly
- Enter confirms
- Shows [ Yes ] [ No ] side-by-side, selected highlighted

#### FieldOption
```go
type FieldOption struct {
    Label       string
    Description string
}
```
Shared between SelectField and WizardField.Options.

### Stepper Bar (`internal/tui/stepper.go`)

```go
type Step struct {
    Title string
    Value string  // Displayed for complete steps
    State StepState
}

const (
    StepPendingState   // ○ not yet done
    StepActiveState    // ◉ current
    StepCompleteState  // ✓ finished
    StepSkippedState   // - not applicable
)

RenderStepperBar(steps []Step, width int) string
```

### Stateless Render Functions

Located in `internal/tui/components.go`. Use `iostreams` styles (qualified imports, no direct lipgloss).

Key functions for guided flows:
- `RenderHeader(HeaderConfig)` - title bar
- `RenderDivider(width)`, `RenderLabeledDivider(label, width)` - separators
- `RenderEmptyState(message, w, h)` - centered message
- `RenderError(err, width)` - error display
- `RenderStatus(StatusConfig)` - status indicator
- `RenderBadge(text)`, `RenderTag(text)` - styled badges

### Program Runner

```go
RunProgram(ios *iostreams.IOStreams, model tea.Model, opts ...) (tea.Model, error)
```

Options:
- `WithAltScreen(bool)` - use alternate screen (default: true for interactive)
- `WithMouseMotion(bool)` - enable mouse events

---

## Storage Integration with Defaults

### Defaults System

Located in `internal/storage/defaults.go`:

```go
GenerateDefaultsYAML[T Schema]() string  // Read struct tags, produce YAML
WithDefaultsFromStruct[T Schema]() Option  // Convenience wrapper
```

Type coercion ensures YAML types match Go field types:
- `KindBool` → YAML bool (true/false)
- `KindInt` → YAML int
- `KindStringSlice` → YAML sequence (comma-separated tag → []string)
- `KindDuration` → YAML string (e.g., "30s")
- `KindText` → YAML string

### Struct Tag Contract

Single source of truth for field metadata:

```go
type MyStruct struct {
    Image string `yaml:"image" label:"Docker Image" desc:"Image to use" default:"debian:bookworm"`
    Port  int    `yaml:"port" label:"Port" desc:"Container port" default:"8080"`
    Enabled *bool `yaml:"enabled" label:"Enable Feature" desc:"Whether to enable" default:"true" required:"true"`
}
```

Schema.Fields() walks these tags and produces FieldSet for consumption by editors.

### Defaults in Action

```go
// Generate defaults YAML at compile-time-ish
defaultsYAML := storage.GenerateDefaultsYAML[config.Project]()

// Use when constructing store
store, err := storage.New[config.Project](
    storage.WithFilenames("clawker.yaml"),
    storage.WithDefaults(defaultsYAML),  // or WithDefaultsFromStruct[config.Project]()
    storage.WithWalkUp(),
    storage.WithConfigDir(),
)
```

Defaults are the **lowest-priority layer**. Discovered files override them.

---

## Composition Patterns for Guided Init

These systems can be composed to create guided initialization flows:

### Pattern 1: Wizard → StoreUI Pipeline

1. **Wizard** - gather basic answers (mode, image, packages)
2. **StoreUI** - let user review and tweak all settings before save

### Pattern 2: StoreUI with Skip Conditions

Use `WithSkipPaths` to hide advanced fields by default:
```go
storeui.Edit(ios, store,
    storeui.WithSkipPaths("security.firewall", "loop.max_loops"),
)
```

### Pattern 3: Custom Field Editors

Adapters can provide custom editors via `Field.Editor` factory:
```go
type Override struct {
    Editor func(label, value string) any  // Returns tea.Model satisfying FieldEditor interface
}
```

### Pattern 4: Conditional Field Creation

Wizard can skip steps based on previous answers via `SkipIf`:
```go
WizardField{
    ID: "packages",
    // ... only show if user chose "custom" build strategy
    SkipIf: func(vals WizardValues) bool {
        return vals["strategy"] != "custom"
    },
}
```

### Pattern 5: Progressive Disclosure

Use defaults for sensible values; let users proceed with enter key through optional fields, edit later via full editor.

---

## Key Insights for New Implementation

1. **Schema is authority** - struct tags are single source of truth (desc/label/default/required)
2. **Reflection is leverage** - WalkFields discovers, SetFieldValue mutates, both via reflection
3. **Storage handles merging** - layered inheritance, provenance tracking, atomic writes
4. **Domain adapters bridge gaps** - TUI-specific concerns (kind override, read-only flags) go here
5. **Validation belongs at write boundary** - domain adapters can validate, but editor stays open
6. **SkipIf enables conditionals** - wizard can skip steps based on accumulated values
7. **Layer targets show inheritance** - all possible targets shown, user picks where to save
8. **Dirty tracking is automatic** - Set() marks dirty, Write() routes per layer
9. **TypeScript-level type safety** - generic Store[T], generic Edit[T] ensures schema consistency
10. **Composition over inheritance** - wrap, delegate, customize; no deep hierarchies

---

## Testing Patterns

### StoreUI Testing

1. **Reflection tests** - WalkFields produces correct Field structs
2. **Mutation tests** - SetFieldValue round-trips through struct properly
3. **Override merging** - ApplyOverrides filters/sorts/customizes correctly
4. **Integration tests** - full Edit flow with mock TUI model
5. **Storage round-trip** - Set/Write/Read cycle preserves values

### Wizard Testing

1. **Step navigation** - nextVisibleStep, prevVisibleStep respect SkipIf
2. **Field delegation** - messages routed to correct field instance
3. **Conditional skipping** - SkipIf predicates evaluated correctly with accumulated values
4. **Validation** - validators run on Enter, prevent advance if fail
5. **Golden view tests** - GOLDEN_UPDATE=1 to verify stepper bar + field rendering

### Integration Testing

- Use `testenv.Env` for isolated XDG dirs
- Use `configmocks.NewIsolatedTestConfig(t)` for file-backed config
- Mock TUI via `tui.RunProgram` callbacks if needed

---

## Architecture Boundary Notes

- **StoreUI imports `tui` but not `bubbletea`** - delegates presentation to tui via generic types
- **TUI imports `bubbletea`/`bubbles` but NOT `lipgloss` directly** - uses qualified imports from `iostreams`
- **Storage imports nothing from internal/** - leaf package, only stdlib + gopkg.in/yaml.v3
- **Config doesn't own project CRUD** - that's `internal/project` responsibility
- **Validation at write boundary** - not in editors; editors collect input freely

---

## Gotchas & Constraints

1. **WalkFields/SetFieldValue panic on non-struct input** - programming errors, not recoverable
2. **ApplyOverrides panics on duplicate paths** - caught in tests
3. **Consumer kinds (> KindLast) are read-only** - prevents data loss, enforced by fieldsToBrowserFields
4. **Empty strings excluded from structToMap** - prevents accidental overrides of lower-layer values
5. **[]string uses comma-separated format** - entries with commas break the parser
6. **time.Duration uses Go duration parsing** - accepts "5m30s", "1h", "300ms", etc.
7. **Nil *bool is treated as false** - SetFieldValue allocates non-nil pointer on update
8. **SkipIf runs on every navigation** - re-evaluated as accumulated values change
9. **Wizard must have unique IDs** - panic on duplicate WizardField.ID
10. **Stepper bar is pure function** - RenderStepperBar takes Step slice, returns string (composable)
