# PRD: Store UI

## Background

Clawker uses `internal/storage.Store[T]` as a generic layered YAML config engine. Multiple domain packages compose stores with their own schema types: `config.Store[Project]`, `config.Store[Settings]`, `firewall.Store[EgressRulesFile]`, `project.Store[ProjectRegistry]`. The store handles file discovery, multi-file merge with provenance tracking, typed COW snapshots via `Read()/*T`, typed mutation via `Set(func(*T))`, and layer-targeted persistence via `Write(filename)` and `Layers() []LayerInfo`.

Today there is no interactive way for users to browse or edit store-backed configuration. Users must hand-edit YAML files. We need a generic, reusable terminal UI that lets users interactively browse fields, see current values and where they came from, edit values, and choose which layer to save to — for any store instance.

## Goals

1. **Generic** — works with any `Store[T]`, not coupled to any specific schema
2. **Reusable** — domain packages build on shared building blocks, not one-off implementations
3. **Layered architecture** — clear separation between persistence (storage), presentation (tui), orchestration (storeui), and domain semantics (domain adapters)
4. **Hybrid field binding** — reflection auto-discovers fields from any struct `T`; domain adapters provide overrides only where needed (labels, descriptions, validation, widget hints, ordering, skip-lists)
5. **Composable** — the store UI is a component that commands and wizards can invoke, not a standalone application

## Architecture

### Package Responsibilities

**`internal/storage`** — Generic typed persistence engine (leaf package, unchanged).
- Owns: `Store[T]`, `Read`, `Set`, `Write`, `Layers`, discovery, merge, provenance, YAML I/O
- No new methods needed. Existing API is sufficient.
- Imports nothing from tui, storeui, config, project, or firewall.

**`internal/tui`** — Generic terminal presentation primitives (unchanged conceptually).
- Owns: field widgets (text, select, confirm), `RunProgram` BubbleTea plumbing, layout/render helpers, styles via iostreams
- Provides reusable building blocks that storeui composes into store editing flows
- Does NOT own or know about store editing logic, schemas, or `Store[T]`
- May gain new generic widgets if store editing needs them (e.g. a field browser list), but any new widget is domain-agnostic
- Imports nothing from storage, config, project, or firewall.

**`internal/storeui`** — Generic bridge between typed stores and the TUI. The "addon/extension" layer on top of storage.
- Owns: reflection-based struct walker, field binding types, override merging, the generic edit/apply/write orchestration flow, save-target selection using `Layers()` + `Write(filename)`
- Generic on `T` — calls `store.Read()` to get `*T`, `store.Set(func(*T))` to mutate, `store.Write(filename)` to persist. No non-generic store interface needed.
- Builds BubbleTea models by composing tui widgets. Owns the store editing model.
- Imports `internal/storage` and `internal/tui`. Nothing else.

**`internal/config/storeui/settings`** — Domain adapter for `config.Settings`.
- Owns: field overrides (labels, prompts, descriptions, validation), mapping `Settings` snapshot to/from fields
- Uses storeui building blocks to build the settings editing experience
- Imports `internal/config`, `internal/storeui`, optionally `internal/tui` for field type constants

**`internal/config/storeui/project`** — Domain adapter for `config.Project`.
- Same pattern as settings, for the `Project` schema

**Future domain adapters** (`firewall/storeui`, `project/storeui`) follow the same pattern but are not built until justified.

### Dependency Chain

```
internal/config/storeui/settings → internal/config, internal/storeui, (optional: internal/tui)
internal/config/storeui/project  → internal/config, internal/storeui, (optional: internal/tui)
internal/storeui                 → internal/tui, internal/storage
internal/tui                     → (leaf: iostreams, text, bubbletea, bubbles)
internal/storage                 → (leaf: yaml, flock, stdlib)
```

### Data Flow

```
User Command
  │  picks which domain adapter to invoke, passes store + TUI handle
  v
Domain Adapter (e.g. config/storeui/settings)
  │  provides field overrides for its schema
  v
internal/storeui
  │  1. calls store.Read() to get current snapshot
  │  2. reflects on *T to discover fields
  │  3. merges domain overrides onto reflected fields
  │  4. builds TUI model from tui widgets
  │  5. runs interactive editing via tui.RunProgram
  │  6. applies changes via store.Set(func(*T))
  │  7. user picks save target from store.Layers()
  │  8. persists via store.Write(filename)
  │
  ├──→ internal/tui       (field widgets + BubbleTea runner)
  └──→ internal/storage   (read, mutate, persist)
```

### What Does NOT Change

- `storage` remains the leaf generic engine. No UI knowledge added.
- `tui` remains generic presentation infrastructure. No schema knowledge added.
- `config` still owns config schemas and semantics. Does not become a UI framework.
- Existing `tui` components (wizard, progress, dashboard, table) are unaffected.

## Deliverables

1. **`internal/storeui`** — the generic building blocks package
2. **`internal/config/storeui/settings`** — settings editing adapter
3. **`internal/config/storeui/project`** — project config editing adapter
4. **Wire into commands** — `clawker init`, `clawker project init`, `clawker settings`

Build order: storeui first, then domain adapters, then command wiring.

## Non-Goals

- No changes to `internal/storage`'s public API
- No schema-specific logic in `storeui`
- No storage imports in `tui`
- No standalone store editor application — this is a composable component
