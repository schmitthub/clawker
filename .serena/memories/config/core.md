# Config and project identity

- `internal/config` owns config loading, validation, schema migrations, and config-derived accessors.
- `internal/project` owns registration, project identity, worktrees, and registry-based root resolution.
- The Factory resolves the root through `project.Registry`, then passes `config.WithProjectRoot(root)`. Config must not import project.
- Config composes separate `Store[Project]` and `Store[Settings]` values. Project defaults and host infrastructure settings do not merge into one schema.
- Project config discovery is bounded by the registered root. At each level the directory form takes precedence over the flat dotfile form.
- An empty project-root anchor disables walk-up. This is suitable for CP and host-service processes.
- Project configuration includes build, harness, workspace, and project egress rules. Host settings include `firewall.enable`; this flag does not disable CP.
- Use specific `config.Config` getters. There is no whole-project or whole-settings snapshot getter.
- `ProjectStore().Set` / `Remove` stage changes. `Write` persists them. Settings use the corresponding store.
- Keys use explicit segments; a literal dot in an alias name is not a separator.
- `internal/consts` owns shared names and path primitives. Use config accessors where the Config interface provides the required value.
- `internal/config/schema.go` and the component schema files define persisted fields. `cmd/gen-docs/` generates JSON schemas from them.
- Package contracts: `internal/config/AGENTS.md`, `internal/project/AGENTS.md`, `internal/state/AGENTS.md`.
- For engine mutation and persistence rules: `mem:storage/core`.
- For bundle component resolution, which has its own precedence: `mem:bundle/core`.
- For choosing config doubles with or without file writes: `mem:testing/core`.
