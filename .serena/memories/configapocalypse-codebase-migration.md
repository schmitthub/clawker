# Config Codebase Migration — Phase 0 → Phase 4

> **Status:** Ready to start Phase 0
> **Branch:** `refactor/configapocalypse`
> **Parent memories:** `configapocalypse-prd`, `configapocalypse-migration-inventory`, `configapocalypse-docs-sync`
> **Last updated:** 2026-02-19

## End Goal

Migrate the entire codebase (~110-120 files) from the removed old config API to the new unified `Config` interface with `Set()`/`Write()`/`Watch()` and ownership-aware file routing.

## Architectural Decisions (ALL RESOLVED)

| Gap | Decision | Status |
|-----|----------|--------|
| Gap 4 — Settings write-back | Use `Set()` + `Write()` with ownership-aware routing | ✅ RESOLVED |
| Gap 5 — Registry access | No separate Registry abstraction. Use `Set("projects.*", ...)` + `Write()`. See Pattern 9 in `internal/config/CLAUDE.md` | ✅ RESOLVED |
| Gap 7 — `ResolveAgentEnv` | Move to `container/shared/` — it's domain logic for container setup, sole caller is `container/shared/container.go` | ✅ RESOLVED |
| Gap 8 — `~/.local/clawker` vs `~/.config/clawker` | `~/.local/clawker` is dead. `ConfigDir()` owns the resolution chain (`CLAWKER_CONFIG_DIR` > `XDG_CONFIG_HOME/clawker` > `AppData/clawker` > `~/.config/clawker`). No package should ever hardcode or know the config directory path. | ✅ RESOLVED |
| Gap 12 — ConfigDirEnvVar | `ConfigDirEnvVar()` is on the Config interface | ✅ RESOLVED |

## Migration Plan

### Phase 0: Config package gap resolution (PREREQUISITE — not started)

Before any caller migration, add missing symbols to `internal/config/`:

- [ ] **Gap 11**: Move `ContainerUID`/`ContainerGID` (value `1001`) to `internal/bundler/` as public constants. All other packages (docker/volume, containerfs, loop/shared, test/harness) will import from bundler.
- [ ] **Gap 10**: Labels — `docker/labels.go` defines all label strings directly using `"dev.clawker."` prefix. No more importing from config. Check hostproxy for circular dep (if circular, define label strings locally in hostproxy).
- [ ] **Gap 9**: Add `Slugify()` back to config package as public utility function (used by test/harness, test/commands/worktree_test).
- [ ] **Gap 3**: Add `DefaultSettings()` function to config (returns `NewMockConfig().Settings()` or equivalent). ~15 call sites in bundler, init, build tests, container tests, fawker.
- [ ] **Gap 2**: Add `NewConfigForTest(project *Project, settings *Settings) (Config, error)` bridge function to `stubs.go`. ~90 test call sites. `FakeConfigOptions` should grow `Project` and `Settings` fields.
- [ ] **Gap 8 doc fix**: Update `~/.local/clawker` references in `CLAUDE.md` (lines 216, 237, 243) and `.claude/docs/DESIGN.md` (lines 47-48) to describe `ConfigDir()` semantically.
- [ ] **Gap 7**: Move `ResolveAgentEnv()` from config to `container/shared/` package.
- [ ] **Gap 13**: Add `ProjectFound() bool` and `ProjectKey() string` convenience methods if still needed, or verify callers can use `GetProjectRoot()` + `Project()` instead.
- [ ] **Gap 5 cleanup**: Remove vestigial `Registry` interface from `schema.go` if it's no longer used, or keep if schema types still reference it. Verify.
- [ ] **Gap 6 cleanup**: Confirm no `configtest/` rebuild needed — all test patterns covered by `NewMockConfig()`, `NewFakeConfig()`, `ReadFromString()`, and the new `NewConfigForTest()` bridge.

### Phase 1: Leaf/foundation infrastructure packages (not started)

Order matters — downstream deps first:

1. [ ] `internal/bundler` — define ContainerUID/GID constants, remove old config deps (`EnsureDir`, `DefaultSettings`, `*config.Config` struct → interface)
2. [ ] `internal/docker` — labels.go self-defined, `client.go` change `*config.Config` to `config.Config` interface, `volume.go` import bundler UID/GID, `defaults.go` use `ConfigDir()` + literal
3. [ ] `internal/containerfs` — import bundler UID/GID (6 sites)
4. [ ] `internal/workspace` — `ConfigDir()` + literal paths, Config param for EnsureShareDir, fix private `clawkerHomeEnv` in tests
5. [ ] `internal/hostproxy` — local path helpers (`pidFilePath()`, `logFilePath()`), label imports (check circular deps with docker)
6. [ ] `internal/socketbridge` — local path helpers (`bridgesDir()`, `bridgePIDFile(id)`)

**Validation gate**: `go build ./internal/bundler/... ./internal/docker/... ./internal/containerfs/... ./internal/workspace/... ./internal/hostproxy/... ./internal/socketbridge/...`

### Phase 2: Mechanical command layer (~25 simple files, not started)

All commands that only need `config.Provider` → `func() (config.Config, error)` change:

- [ ] All container/* subcommands: attach, cp, exec, inspect, kill, list, logs, pause, remove, rename, restart, start, stats, stop, top, unpause, update, wait
- [ ] All worktree/* subcommands: add, list, prune, remove
- [ ] loop/reset, loop/status
- [ ] monitor/status

Each file: change Options struct `Config func() config.Provider` → `Config func() (config.Config, error)`, update run function to handle error return, update tests to use new stubs.

### Phase 3: Complex command packages (not started)

Commands with additional old-API usage beyond Provider:

1. [ ] `internal/cmd/image/build` — remove Validator, use ReadFromString validation
2. [ ] `internal/cmd/project/init` — ProjectLoader → os.ReadFile, registry via Set+Write, settings via Set+Write
3. [ ] `internal/cmd/project/register` — ProjectLoader → os.ReadFile, registry via Set+Write
4. [ ] `internal/cmd/container/shared` — ResolveAgentEnv (moved in Phase 0), SettingsLoader → Set+Write
5. [ ] `internal/cmd/container/create` — SettingsLoader → Set+Write
6. [ ] `internal/cmd/container/run` — SettingsLoader → Set+Write
7. [ ] `internal/cmd/init` — SettingsLoader, DefaultSettings, Config struct literal → Set+Write
8. [ ] `internal/cmd/generate` — BuildDir → ConfigDir() + literal
9. [ ] `internal/cmd/loop/iterate`, `loop/tasks` — remove type assertion, use interface methods directly
10. [ ] `internal/cmd/loop/shared` — Config struct field → interface, ContainerUID/GID from bundler
11. [ ] `internal/cmd/monitor/init`, `up`, `down` — MonitorDir → ConfigDir() + MonitorSubdir()

### Phase 4: Application layer & test infrastructure (not started)

1. [ ] `internal/clawker/cmd.go` — replace `ClawkerHome()` with `ConfigDir()`
2. [ ] `internal/project/register.go` — SettingsLoader → Set+Write, registry via Set+Write
3. [ ] `internal/cmd/factory/default_test.go` — private env var → `ConfigDirEnvVar()`, ProjectFound/Key
4. [ ] `test/harness/` — Slugify, NewConfigForTest, Provider → Config, configtest → stubs
5. [ ] `test/harness/builders/` — configtest.ProjectBuilder → new pattern
6. [ ] `test/internals/` — NewConfigForTest, private env var
7. [ ] `test/commands/` — type assertion removal, Slugify, registry patterns
8. [ ] `test/agents/` — configtest → stubs
9. [ ] `cmd/fawker/` — type assertion removal, DefaultSettings, configtest → stubs

### Final validation

- [ ] `go build ./...` passes
- [ ] `make test` passes
- [ ] Update all CLAUDE.md files, memories, migration status tables
- [ ] Remove "REFACTOR IN PROGRESS" banner from `internal/config/CLAUDE.md`

## Key References

- **Full per-file inventory**: `configapocalypse-migration-inventory` memory (Part 2 has every file, every symbol, every gap)
- **Config interface**: `internal/config/config.go` lines 30-52
- **WriteOptions**: `internal/config/config.go` lines 114-120
- **Key ownership map**: `internal/config/config.go` lines 81-96
- **Migration patterns 1-9**: `internal/config/CLAUDE.md` → Migration Guide section
- **Config CLAUDE.md**: Full package reference at `internal/config/CLAUDE.md`

## Lessons Learned

- `Set()` returns `error` (validates key ownership), not void
- `WriteOptions` has 4 fields: `Path`, `Safe`, `Scope`, `Key`
- `ConfigScope` type: `ScopeSettings`, `ScopeProject`, `ScopeRegistry`
- Thread-safe via `sync.RWMutex` on `configImpl`
- `dirtyNode` tree for structural dirty tracking (not flat set)
- Pattern 9 in config/CLAUDE.md shows registry writes via Set+Write (no Registry abstraction)
- `~/.local/clawker` is dead — `ConfigDir()` owns the resolution chain
- `ResolveAgentEnv` → `container/shared/` (domain logic, not config)

## IMPERATIVE

Always check with the user before proceeding with the next todo item. Start by reading this memory and the `configapocalypse-migration-inventory` memory for full context. If all work is done, ask the user if they want to delete this memory and its parent memories.
