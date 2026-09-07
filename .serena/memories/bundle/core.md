# Bundles and image generation

- `internal/config` owns persisted manifest types.
- `internal/bundle` owns component discovery, resolution, installed sources, and cache management.
- `internal/bundler` loads harnesses/stacks and generates Dockerfiles/build contexts. It imports bundle; bundle must not import bundler.
- `internal/monitor` loads monitoring extensions and generates collector/stack configuration.

## Resolution

- Components occupy convention directories: harnesses, stacks, and monitoring. Directory names identify components.
- Bare names resolve in this order: user loose component, project loose component, embedded component.
- Qualified names identify a namespace, bundle, and component. Resolution requires the declared installed or in-place source.
- An installed source is addressed by its declared value, including ref/SHA/subdirectory. A changed source value addresses a different cache entry.
- Cache reachability depends on declarations from registered projects. Do not replace declaration-based collection with age-only eviction.
- Bundles and native agent plugins have different owners. `clawker-plugin/` is the plugin Git submodule; `internal/cmd/plugin/` owns CLI plugin operations.
- Old multi-harness branch plans do not define the present install model.

## Build boundaries

- Base and harness image templates live in `internal/bundler/assets/`.
- Harness-required egress and project egress are composed by `internal/bundler/egress.go`.
- Generated asset order affects Docker layer reuse. Check package build-context and golden tests before changing templates.
- Corresponding support-plugin templates must remain in step; `prek.toml` defines the drift check.
- Source contracts: `internal/bundle/AGENTS.md`, `internal/bundler/AGENTS.md`, `internal/cmd/plugin/AGENTS.md`.
- For extension selection and the host monitoring ledger: `mem:monitoring/core`.
- Design and research records: `mem:plans/bundle-install/design`, `mem:plans/multi-harness/status`.
