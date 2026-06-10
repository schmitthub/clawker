# Alias Command Package

`clawker alias` — manage user-defined command aliases (issue-style: `clawker go` expands to a full `clawker run ...` invocation).

Expansion/registration itself lives in `internal/cmd/root/useraliases.go`; this package is the management surface.

## Data Model

- **One home**: `Project.Aliases` (`merge:"union"`, shipped defaults `go` + `wt`). Active aliases are the merged `aliases` key across ALL project config layers — walk-up files (closest to CWD wins) > user-level `clawker.yaml` in the config dir > shipped defaults. The root command registers from this merged view; project-file aliases apply automatically.
- **Write targets**: `set` always writes the user config-dir `clawker.yaml` (`shared.SetTarget`); `export` writes the most local discovered walk-up file (`shared.ExportTarget`, never creates files); `delete` removes the entry from EVERY file layer that carries it (`shared.LayersContaining`) so one delete clears the name. Every file write prints `Wrote <abs path>`.
- Shipped defaults are immutable: `delete` operates on file entries only — deleting an override restores the default; a pure default errors (override with `set --clobber` instead). There is NO disable/masking concept; an empty-string value is just an invalid entry the loader skips.
- There is no `alias import` — with all layers live, adoption is automatic.

## Files

| File | Purpose |
|------|---------|
| `alias.go` | `NewCmdAlias(f, validCommand)` — parent; wires subcommands |
| `shared/shared.go` | `ValidCommandFunc`, `ValidateName`, `SplitExpansion`, `ValidateExpansionTarget`, `DefaultAliases`, `SetTarget`, `ExportTarget`, `OpenFileStore`, `AliasFieldPath`, `LayersContaining` |
| `set/set.go` | `alias set <name> <expansion> [--clobber]` — validates name (no builtin shadowing) + expansion target, writes the user config-dir file; warns when a walk-up layer shadows the new value |
| `list/list.go` | `alias list` — NAME/EXPANSION/SOURCE table (SOURCE = providing file path via store provenance, or `default`), `--json`/`--format`/`-q` |
| `delete/delete.go` | `alias delete <name>` (alias `rm`) — removes the key from every file layer; errors on a pure shipped default (immutable base) |
| `export/export.go` | `alias export` — publishes active aliases into the most local walk-up config file; skips empty entries, shipped defaults, and entries the target already provides (no `--clobber`: the target is the highest-priority layer, so its entries are always the merged winners) |

## Key Wiring

- `NewCmdAlias(f, validCommand shared.ValidCommandFunc)` — root passes a closure over `root.builtinCommandExists` AFTER the tree is complete, so set can reject names that shadow real commands while still allowing redefinition of registered user aliases.
- **All file writes go through `shared.OpenFileStore(target)`** — an isolated `storage.Store[config.Project]` on the target file only, scoping the write to exactly the alias entries. The composite `cfg.ProjectStore()` marks defaults-provenance fields dirty at construction (the mechanism init/bootstrap uses to materialize defaults); on an init-current file that set is empty, but on a file missing newer schema fields a composite write would backfill them as a side effect. Alias writes stay surgical instead.
- `shared.ExportTarget(cfg)` returns the first discovered file layer outside the config dir — the most local, highest-priority walk-up file, local variants included. Errors when no walk-up file exists (export never creates files).
- Per-key provenance: union maps merge key-by-key, so `cfg.ProjectStore().Provenance("aliases.<name>")` resolves the providing layer — used by list (SOURCE), set (shadow warning), and export (default/target exclusion).
- `shared.DefaultAliases()` (via `config.NewBlankConfig()`) lets delete tailor its messaging/error for shipped defaults.
- `init` does NOT materialize the default alias into project files — `NewProjectStoreFromPreset` carries no defaults layer, so the shipped `go` alias stays virtual.

## Testing

Subcommand tests are prod-shaped: `testenv.New(t)` isolates the XDG dirs and the factory `Config` closure calls `config.NewConfig()` fresh per invocation, so consecutive command runs see each other's writes exactly like consecutive CLI runs (the isolated-file-store writes are invisible to a config snapshot constructed earlier). Export/list tests build a real `storage.Store[config.Project]` over `t.TempDir()` layers (defaults + config dir + project dir) so provenance is real. The canonical full-journey test is `TestAliasLifecycle_Integration` in `internal/cmd/root/useraliases_integration_test.go`: a prod-shaped factory rebuilt per invocation drives `init --yes` → alias subcommands → alias dispatch → on-disk file review. Project-file fixtures in alias tests should look like init output (fully materialized), not hand-trimmed minimal files — init is the only supported way project files come to exist.
