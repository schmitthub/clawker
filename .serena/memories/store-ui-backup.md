
high level package map

```
internal/storage
  // Generic typed persistence engine.
  // Owns: Read, Set, Write, Layers, merge, provenance, YAML I/O.
  // Knows nothing about terminal UI, prompts, forms, or schemas' UX.

internal/tui
  // Generic terminal presentation primitives.
  // Owns: WizardField, RunWizard, field widgets, layouts, BubbleTea plumbing.
  // Knows nothing about config, project, firewall, or storage semantics.

internal/storeui
  // Generic bridge between a typed store and the generic TUI wizard.
  // Owns: generic EditableStore[T], generic binding model, generic run/apply/write flow,
  // optional generic "choose save target" step using Layers and Write(filename).
  // Knows about storage-shaped behavior and tui-shaped behavior, but not any domain schema.

internal/config
  // Domain package for config schemas and config store construction.
  // Owns: Project, Settings, defaults, NewConfig, path helpers, config semantics.
  // Does not own user-facing wizard copy or BubbleTea-specific field definitions.

internal/config/storeui/settings
  // Settings-specific wizard adapter.
  // Owns: bindings for config.Settings, prompts, validation, save choices if needed.
  // Translates Settings <-> storeui bindings.
  // Knows config.Settings semantics.

internal/config/storeui/project
  // Project-specific wizard adapter.
  // Owns: bindings for config.Project, prompts, validation, conditional steps.
  // Translates Project <-> storeui bindings.
  // Knows config.Project semantics.

internal/project
  // Domain package for project registry / project lifecycle if it has store-backed state.
  // Continues owning its schemas and domain logic.

internal/project/storeui
  // Only if project later needs reusable project-specific store editors.
  // Same pattern as config/storeui, but owned by project domain.

internal/firewall
  // Domain package for firewall rules and runtime behavior.
  // Continues owning rule semantics and store-backed rule files.

internal/firewall/storeui
  // Only if firewall gets interactive editors.
  // Owns firewall-specific bindings and validation.
  // Not created until it is justified.
```

visual arch

```
User Command
  // A command decides "edit settings", "edit project", "edit firewall rules", etc.
  |
  v
Domain-specific adapter package
  // Example: internal/config/storeui/settings
  // Builds fields from current typed snapshot.
  // Parses submitted values back into the typed struct.
  |
  v
internal/storeui
  // Generic orchestration:
  // 1. read current snapshot
  // 2. build generic wizard fields
  // 3. run wizard
  // 4. apply values through Set
  // 5. persist with Write
  |
  +---------------------> internal/tui
  |                        // Generic wizard engine and widgets only
  |
  +---------------------> internal/storage
                           // Generic store mechanics only
```

dep chain

```
internal/config/storeui/settings
  -> internal/config
  -> internal/storeui
  -> internal/tui   (optional, if it needs direct field types/helpers)

internal/config/storeui/project
  -> internal/config
  -> internal/storeui
  -> internal/tui   (optional)

internal/storeui
  -> internal/tui
  -> internal/storage   (or a small store-shaped interface matching it)

internal/tui
  // no config/project/firewall imports

internal/storage
  // no tui/config/project/firewall imports
```

think of it as a three-part pipieline

```
Domain package
  // What does this data mean?
  // What should the user see?
  v
storeui
  // How do we generically edit a typed store?
  v
tui + storage
  // One side renders interaction, the other persists changes
```

arch doc

```
# Architecture

## What Goes Where

### `internal/storage`

Owns:

- `Store[T]`
- `Read`, `Set`, `Write`, `Layers`
- discovery, merge, provenance, YAML write routing
- maybe tiny generic interfaces if they are truly persistence concepts

Does not get:

- `WizardField`
- prompts
- validation strings for users
- field ordering
- save target UI

### `internal/tui`

Owns:

- `WizardField`
- wizard runner
- field widgets like text/select/confirm
- layout/render helpers
- reusable save-target picker widget if it is truly domain-agnostic

Does not get:

- `config.Settings`-specific fields
- `project.Project`-specific prompts
- firewall rule semantics
- direct knowledge of `Store[T]`

### `internal/storeui`

Owns:

- `EditableStore[T]` interface
- `FieldBinding[T]` or `Schema[T]` abstraction
- generic `RunWizardForStore` or `EditStore` function
- generic save-target selection flow
- maybe generic helpers for mapping bool/string/int fields into wizard controls

Does not get:

- concrete `config.Settings` logic
- concrete `config.Project` logic
- firewall-specific validation
- project registry semantics

### `internal/config`

Owns:

- schema structs
- defaults
- config loading
- config path helpers
- typed accessors and domain behavior

Does not get:

- BubbleTea models
- generic wizard runners
- generic save target picker
- terminal copy

### `internal/config/storeui/settings`

Owns:

- list of settings bindings
- titles, prompts, descriptions, validation
- mapping current `Settings` snapshot to fields
- applying submitted values back to `Settings`

### `internal/config/storeui/project`

Owns:

- the same responsibilities as `settings`, but for the `Project` schema

## What Does Not Change

### `internal/storage` does not change conceptually

- It remains the leaf generic engine.
- It still exposes typed snapshots and write mechanics.
- It still should not know about UI.

### `internal/tui` does not change conceptually

- It remains generic presentation infrastructure.
- It still should not know about `config`, `project`, `firewall`, or any schema.
- At most it may gain more generic widgets if store editing needs them.

### `internal/config` does not change conceptually

- It still owns config schemas and config semantics.
- It does not become a UI framework.

## Command Responsibilities

Commands do not need to know much:

- They choose which domain adapter to invoke.
- They pass the relevant store and TUI handle.
- They do not reimplement edit/apply/write flow.

## Suggested Responsibility Boundaries

- `storage`: persist typed data safely
- `tui`: render reusable interactive terminal components
- `storeui`: connect typed stores to reusable editing flow
- `config/storeui/settings`: describe how `Settings` should be edited
- `config/storeui/project`: describe how `Project` should be edited
```
