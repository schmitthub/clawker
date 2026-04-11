# Store UI Rules

**Applies to**: `internal/storeui/**`, `internal/config/storeui/**`, `internal/tui/fieldbrowser*`, `internal/tui/listeditor*`, `internal/tui/textareaeditor*`

> For the full architecture, data flow, TUI component API, test patterns, and gotchas, see `.claude/docs/STOREUI-REFERENCE.md`. This rule keeps only the load-bearing mental model and the checklist for adding a new store editor.

## Mental Model: Multi-Layer Config Editor

Store UI is a **config placement tool**, not an override editor. It gives users a unified view across all layer files so they can make informed decisions about where to place config values based on their project's directory structure.

**Layered inheritance**: Clawker configs use walk-up file discovery. A monorepo might have:
- `./clawker.yaml` — repo root config (cascades to all subdirs)
- `./frontend/.clawker.yaml` — frontend-specific overrides
- `~/.config/clawker/clawker.yaml` — user-level defaults

The same key in different layer files is **inheritance**, not duplication. Merge strategies (`union`, `override`) resolve how values combine across layers.

**The browser shows the merged state** — the effective config for the current working directory, with per-layer breakdown showing which file each value comes from. This is read-only context. When the user edits a field and picks a save target, they're writing to a specific layer file. The user might save a value to the repo root file knowing it won't affect their CWD (a closer layer wins) but will cascade to sibling directories.

**Validation guards writes, not editors.** Editors collect input freely. The write boundary (per-layer) is where validation happens, because that's where layer context is available. Don't put domain validation in TUI editors — they show merged state and can't know the user's intent until a layer is chosen.

## Architecture (one diagram)

```
Command layer (cmd/settings/edit, cmd/project/edit)
  → Domain adapter (config/storeui/settings, config/storeui/project)
    → Orchestration (internal/storeui)
      → Presentation (internal/tui — FieldBrowserModel, ListEditorModel, TextareaEditorModel)
      → Persistence (internal/storage — Store[T])
```

**Import boundary**: `storeui` does NOT import `bubbletea` or `bubbles`. All presentation is delegated to `internal/tui` via generic types. Full details in the reference doc.

## When Adding a New Store Editor

1. **Domain adapter** under `internal/config/storeui/<domain>/` exports:
   - `Overrides() []storeui.Override` — TUI-only customizations (`Hidden`, `ReadOnly`, `Kind`, `Options`, `Order`). Labels/descriptions come from struct tags, not overrides.
   - `LayerTargets(store, cfg) []storeui.LayerTarget` — where the user can save each field. Build from `store.Layers()`; include "Local"/"User"/"Original" targets.
   - `Edit(ios, store, cfg) (storeui.Result, error)` — convenience wrapper that wires overrides + targets into `storeui.Edit[T]`.
2. **Cobra command** under `internal/cmd/<noun>/edit/` — thin wrapper: load config → get store → call domain `Edit` → print success/cancel. Nothing else belongs here.
3. **Wire into parent** — add `edit.NewCmdEdit(f, nil)` to the parent command's `AddCommand` list.
4. **Tests** — at minimum: `TestOverrides_AllPathsMatchFields` (prevents typo rot against `WalkFields(schema)`), a round-trip integration test that drives the store through `WalkFields → SetFieldValue → store.Set → store.Write → reload`, and a unit test for any non-trivial override decision.

## Override Quick Reference

- `Hidden: true` removes a field. Use prefix-based hiding — hiding `"build.instructions"` also hides `"build.instructions.env"`, `"build.instructions.root_run"`, etc.
- `ReadOnly: true` for fields managed by other systems (e.g., `host_proxy.*` ports)
- `Kind: storeui.KindSelect` + `Options: []string{...}` for enum-like fields
- `Order: N` to control sort position within a tab (lower = first)
- `ApplyOverrides` **panics on duplicate override paths** — catch that in tests

## Storage API You'll Actually Call

| Method | Purpose |
|--------|---------|
| `store.Read()` | Immutable `*T` snapshot |
| `store.Set(func(*T))` | Mutate in-memory via closure |
| `store.Write(storage.ToPath(path))` | Persist dirty fields to an explicit layer file |
| `store.Layers()` | Discovered layer files (for `LayerTargets`) |
| `store.Provenance(path)` | Which layer won a specific field (for the breakdown display) |

Everything else — full type→kind→editor table, SetFieldValue semantics, TUI component APIs, test recipes, gotchas — is in `.claude/docs/STOREUI-REFERENCE.md`.
