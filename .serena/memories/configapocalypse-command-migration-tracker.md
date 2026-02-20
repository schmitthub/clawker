# Configapocalypse: Command-Layer Migration Tracker

> **Branch:** `refactor/configapocalypse`
> **Started:** 2026-02-20

## Completed

### Factory (`internal/cmd/factory/`)
- **Production**: `project.NewService(cfg, f.IOStreams.Logger)` → `project.NewProjectManager(cfg)` (NewService didn't exist)
- **Tests deleted**: `TestFactory_Config_Gateway`, `TestFactory_Config_Resolution_NoRegistry`, `TestFactory_Config_Resolution_WithProject`, `TestFactory_Client`, `TestFactory_HostProxy`, `TestFactory_Prompter`, `TestIOStreams_SpinnerDisabledEnvVar`, `TestIOStreams_SpinnerEnabledByDefault`
- **Tests kept**: `TestNew` (version, IOStreams, TUI non-nil)
- **Rationale**: Factory tests were testing other packages (Config resolution, HostProxy, Prompter, IOStreams spinner). Those belong in their own package tests.

## Lessons Learned / Gotchas

1. **Factory tests should only test wiring, not behavior of wired dependencies.** The old tests called `New()` then tested Config resolution, HostProxy creation, etc. — that's testing Config/HostProxy/IOStreams, not the factory.
2. **`NewConfig()` requires all three files to exist** (`settings.yaml`, `clawker.yaml`, `projects.yaml` in `ConfigDir()`). Tests that call `New()` with real config need `CLAWKER_CONFIG_DIR` pointing at a dir with these files. Better to avoid calling `New()` in tests if possible — use `&cmdutil.Factory{}` struct literals.
3. **`hostproxy.NewManager(nil)` panics** — the factory's `hostProxyFunc` sets `cfg = nil` on config error, then passes nil to `NewManager`. Pre-existing bug surfaced by config now returning errors.
4. **`config.clawkerHomeEnv` is unexported** — use `"CLAWKER_CONFIG_DIR"` string literal or `cfg.ConfigDirEnvVar()`.
5. **`ProjectFound()` / `ProjectKey()` are gone from `Config`** — project identity lives in `project.ProjectManager` now.
6. **`config.RegistryFileName` removed** — registry path is internal to config.

## Queue

Next: Pick first command from Phase 1 (simple mechanical sweep).
