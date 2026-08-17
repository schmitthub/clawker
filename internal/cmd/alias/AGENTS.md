# Alias Command Package

`clawker alias` — manage user-defined command aliases (issue-style: `clawker go` expands to a full `clawker run ...` invocation).

Expansion/registration itself lives in `internal/cmd/root/useraliases.go`; this package is the management surface.

## Data Model

- **One home**: `Project.Aliases` (`merge:"union"`, shipped defaults `go`, `wt`, `claude`, `codex`), read through `cfg.Aliases()`. Active aliases are the merged `aliases` key across ALL project config layers — walk-up files (closest to CWD wins) > user-level `clawker.yaml` in the config dir > shipped defaults. The root command registers from this merged view; project-file aliases apply automatically.
- **Write targets**: `set` always writes the user config-dir `clawker.yaml` (`shared.SetTarget`); `export` writes the most local discovered walk-up file (`shared.ExportTarget`, never creates files); `delete` removes the entry from EVERY file layer that carries it (`shared.LayersContaining`) so one delete clears the name. Every file write prints `Wrote <abs path>`.
- Shipped defaults are immutable: `delete` operates on file entries only — deleting an override restores the default; a pure default errors (override with `set --clobber` instead).
- There is no `alias import` — with all layers live, adoption is automatic.
- Alias names are single command tokens (`shared.ValidateName`): non-empty, one word, no leading `-`. Dots are allowed — an entry is addressed as the key segments `{"aliases", name}`, so a name like `a.b` is one map key, never a nested `a: {b: …}` tree.

## Files

| File | Purpose |
|------|---------|
| `alias.go` | `NewCmdAlias(f, validCommand)` — parent; wires subcommands |
| `shared/shared.go` | `ValidCommandFunc`, `ValidateName`, `SplitExpansion`, `ValidateExpansionTarget`, `DefaultAliases`, `SetTarget`, `ExportTarget`, `AliasKey`, `SamePath`, `WriteAliasEntries`, `DeleteAliasEntry`, `LayersContaining` |
| `set/set.go` | `alias set <name> <expansion> [--clobber]` — validates name (no builtin shadowing) + expansion target, writes the user config-dir file; warns when a walk-up layer shadows the new value |
| `list/list.go` | `alias list` — NAME/EXPANSION/SOURCE table (SOURCE = providing file path via store provenance, or `default`), `--json`/`--format`/`-q` |
| `delete/delete.go` | `alias delete <name>` (alias `rm`) — removes the key from every file layer; errors on a pure shipped default (immutable base) |
| `export/export.go` | `alias export` — publishes active aliases into the most local walk-up config file; skips empty entries, shipped defaults, and entries the target already provides (no `--clobber`: the target is the highest-priority layer, so its entries are always the merged winners) |

## Key Wiring

- `NewCmdAlias(f, validCommand shared.ValidCommandFunc)` — root passes a closure over `root.builtinCommandExists` AFTER the tree is complete, so set can reject names that shadow real commands while still allowing redefinition of registered user aliases.
- **All file writes go through `cfg.ProjectStore()`** — `shared.WriteAliasEntries` stages `Set({"aliases", name}, expansion)` and `shared.DeleteAliasEntry` stages `Remove("aliases", name)`, each flushed with `WriteFieldTo(path, "aliases", name)`. Per-field flush is what keeps the write surgical: exactly the named key is grafted into the target file's current contents, every other staged field stays staged, and schema fields the file lacks are never backfilled. Delete walks the carrying layers highest priority first — each flush drops the entry from one file and the remerge re-exposes the next layer's value for the next removal.
- `shared.ExportTarget(cfg)` returns the first discovered file layer outside the config dir — the most local, highest-priority walk-up file, local variants included. Errors when no walk-up file exists (export never creates files).
- Per-key provenance: union maps merge key-by-key, so `cfg.ProjectStore().Provenance(shared.AliasKey(name)...)` resolves the providing layer — used by list (SOURCE), set (shadow warning), and export (default/target exclusion).
- `shared.DefaultAliases()` (via `config.NewBlankConfig()`) lets delete tailor its messaging/error for shipped defaults.
- `init` does NOT materialize default aliases into project files — `NewProjectStoreFromPreset` carries no defaults layer, so the shipped aliases stay virtual.

## Testing

Subcommand tests are prod-shaped: `testenv.New(t)` isolates the XDG dirs and the factory `Config` closure calls `config.NewConfig()` fresh per invocation, so consecutive command runs see each other's writes exactly like consecutive CLI runs. Export/list tests load a real `config.NewConfig()` from a temp project dir + config dir so provenance is real. The canonical full-journey test is `TestAliasLifecycle_Integration` in `internal/cmd/root/useraliases_integration_test.go`: a prod-shaped factory rebuilt per invocation drives `init --yes` → alias subcommands → alias dispatch → on-disk file review. Project-file fixtures in alias tests should look like init output (fully materialized), not hand-trimmed minimal files — init is the only supported way project files come to exist.
