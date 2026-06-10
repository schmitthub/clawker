# Alias Command Package

`clawker alias` — manage user-defined command aliases (issue-style: `clawker dev` expands to a full `clawker run ...` invocation).

Expansion/registration itself lives in `internal/cmd/root/useraliases.go`; this package is the management surface.

## Data Model

- **Active aliases**: `Settings.Aliases` (settings.yaml, `merge:"union"`, shipped default `dev`). The ONLY source the root command registers from.
- **Dormant aliases**: `Project.Aliases` (clawker.yaml) — a git-tracked sharing vehicle. NEVER applied automatically; `import`/`export` move entries deliberately. A cloned repo cannot mint command names.
- Disabling: empty-string expansion. Union merge keeps defaults-layer keys present, so `delete` on a shipped default writes `""` instead of removing the key.

## Files

| File | Purpose |
|------|---------|
| `alias.go` | `NewCmdAlias(f, validCommand)` — parent; wires subcommands |
| `shared/shared.go` | `ValidCommandFunc`, `ValidateName`, `SplitExpansion`, `ValidateExpansionTarget`, `DefaultAliases`, `ExportTarget`, `OpenExportStore` |
| `set/set.go` | `alias set <name> <expansion> [--clobber]` — validates name (no builtin shadowing) + expansion target, writes settings |
| `list/list.go` | `alias list` — NAME/EXPANSION/SOURCE table (`default`/`user`), `--json`/`--format`/`-q` |
| `delete/delete.go` | `alias delete <name>` (alias `rm`) — removes user keys, disables defaults via `""` |
| `importcmd/import.go` | `alias import [--clobber]` — copies `cfg.Project().Aliases` into settings; validates + skips shadowing/invalid entries |
| `export/export.go` | `alias export [--clobber]` — writes active aliases into the project's shared config file |

## Key Wiring

- `NewCmdAlias(f, validCommand shared.ValidCommandFunc)` — root passes a closure over `root.builtinCommandExists` AFTER the tree is complete, so set/import can reject names that shadow real commands while still allowing redefinition of registered user aliases.
- **Export writes through `shared.OpenExportStore(target)`** — an isolated `storage.Store[config.Project]` on the target file only. The composite `cfg.ProjectStore()` pre-marks every defaults-provenance field dirty (settings-bootstrap behavior), so writing through it would materialize all schema defaults into the project file. The isolated store writes only alias entries and preserves the rest of the file.
- `shared.ExportTarget(cfg)` picks the highest-priority project layer that is shared: skips `.local.` variants and the user-level project config in the clawker config dir. Errors when not in a project.
- `shared.DefaultAliases()` (via `config.NewBlankConfig()`) distinguishes shipped defaults for delete/list source labeling.

## Testing

Subcommand tests use `configmocks.NewIsolatedTestConfig(t)` for settings/project round-trips on disk; export tests build a real isolated project store over `t.TempDir()`. Full-chain integration tests (set → persist → reload → registration → dispatch) live in `internal/cmd/root/useraliases_integration_test.go`.
