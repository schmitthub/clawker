# Config Migration: workspace + containerfs (Items #5 & #6)

> **Branch:** `refactor/configapocalypse`
> **Status:** In Progress — source edits done, tests written, needs build verification + CLAUDE.md updates

## Context

Migrating `internal/workspace` and `internal/containerfs` from legacy `config.*` free functions/constants to the `config.Config` interface. These are items #5 and #6 in the configapocalypse PRD.

## What Changed

### `internal/containerfs/containerfs.go`
- `PrepareOnboardingTar(containerHomeDir string)` → `PrepareOnboardingTar(cfg config.Config, containerHomeDir string)`
- `PreparePostInitTar(script string)` → `PreparePostInitTar(cfg config.Config, script string)`
- All 6 `config.ContainerUID` / `config.ContainerGID` constants replaced with `cfg.ContainerUID()` / `cfg.ContainerGID()` method calls

### `internal/containerfs/containerfs_test.go`
- Added `config` import
- All 3 test call sites pass `config.NewMockConfig()` as first arg

### `internal/workspace/strategy.go`
- **Deleted `EnsureShareDir()`** — callers now use `cfg.ShareSubdir()` directly (it already does `os.MkdirAll`)
- `docker.VolumeLabels(...)` → `cli.VolumeLabels(...)` in `EnsureConfigVolumes` (2 sites) — VolumeLabels is now a `*Client` method

### `internal/workspace/setup.go`
- **Deleted `resolveIgnoreFile()`** — replaced by `cfg.Cfg.GetProjectIgnoreFile()` (new Config method added by user)
- `SetupMountsConfig.Config *config.Project` → `SetupMountsConfig.Cfg config.Config` — replaced legacy schema pointer with Config interface
- `SetupMounts` body updated: uses `cfg.Cfg.Project()` for schema access, `cfg.Cfg.GetProjectIgnoreFile()` for ignore file, `cfg.Cfg.ShareSubdir()` for share dir

### `internal/workspace/strategy_test.go`
- Removed `TestEnsureShareDir` and `TestEnsureShareDir_Idempotent` (tested deleted function)
- Removed unused `config`, `os`, `filepath` imports
- Kept: `TestGetShareVolumeMount`, `TestShareConstants`, `TestConfigVolumeResult`

### `internal/workspace/setup_test.go`
- Removed `TestResolveIgnoreFile` (tested deleted function)
- Removed `TestSetupMounts_IgnoreFileSelectionAndLoadErrorPropagation` (tested behavior now owned by config's `GetProjectIgnoreFile()`)
- Removed `config` import (no longer needed)
- Kept: all `TestBuildWorktreeGitMount_*` tests (unchanged)

## Lessons Learned

- **Never edit `internal/config` package** — it's owned separately. Use existing stubs/helpers only.
- `cfg.ShareSubdir()` already does `os.MkdirAll` via `subdirPath()` — no need for wrapper functions
- `GetProjectIgnoreFile()` was added by the user to the Config interface during this work — resolves ignore file from `projectConfigFile` directory
- `NewFakeConfig` doesn't set `projectConfigFile` (only `NewConfig()` does during file loading), so `GetProjectIgnoreFile()` errors on fake configs — this is fine, test that behavior in config package
- `docker.VolumeLabels` is now a `*Client` method but `docker.VolumeName` remains a free function
- copylocks warnings on `config.Config` are false positives (interface, always pointer receiver underneath)

## TODO Sequence

- [x] 1. Migrate `internal/containerfs/containerfs.go` — add `cfg config.Config` param to tar functions
- [x] 2. Update `internal/containerfs/containerfs_test.go` — pass `config.NewMockConfig()`
- [x] 3. Migrate `internal/workspace/strategy.go` — delete `EnsureShareDir`, fix `VolumeLabels` calls
- [x] 4. Migrate `internal/workspace/setup.go` — delete `resolveIgnoreFile`, replace `Config` field with `Cfg`, use `GetProjectIgnoreFile()` and `ShareSubdir()`
- [x] 5. Fix `internal/workspace/strategy_test.go` — remove tests for deleted functions
- [x] 6. Fix `internal/workspace/setup_test.go` — remove tests for deleted functions
- [ ] 7. **BUILD VERIFICATION** — `go build ./internal/containerfs/...` and `go build ./internal/workspace/...`
- [ ] 8. **TEST VERIFICATION** — `go test ./internal/containerfs/... -v` and `go test ./internal/workspace/... -v`
- [ ] 9. Update `internal/workspace/CLAUDE.md` — remove `EnsureShareDir`, `resolveIgnoreFile`; update `SetupMountsConfig` (Cfg field), `SetupMounts` docs
- [ ] 10. Update `internal/containerfs/CLAUDE.md` — update `PrepareOnboardingTar` and `PreparePostInitTar` signatures
- [ ] 11. Update `configapocalypse-prd` memory — mark items #5 and #6 complete

## IMPERATIVE

**Always check with the user before proceeding with the next TODO item.** If all work is done, ask the user if they want to delete this memory.
