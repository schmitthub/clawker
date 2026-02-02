# Factory Refactor - COMPLETED

All 7 tasks completed on branch `a/factory-defaults-refactor`:

1. Renamed `config.Config` schema struct to `config.Project`
2. Renamed `internal/prompts` package to `internal/prompter`
3. Dissolved `internal/resolver` into `internal/docker`
4. Created `config.Config` gateway type (lazy Project/Settings/Resolution/Registry)
5. Refactored Factory struct from 25 fields to 9
6. Updated root.go and all command consumers
7. Updated documentation and verified (2753 tests pass)

## Image Resolution Refactor - COMPLETED

Additional 7 tasks on same branch (see `docs/plans/2026-02-02-image-resolve-refactor.md`):

1. Added `cfg *config.Config` to `docker.Client`, updated `NewClient(ctx, cfg)` signature
2. Converted `FindProjectImage`/`ResolveImageWithSource`/`ResolveImage` to Client methods
3. Deleted `ResolveAndValidateImage`, `ImageValidationDeps`, `FlavorOption` from docker package
4. Moved interactive rebuild logic (`handleMissingDefaultImage`) to `run.go` and `create.go`
5. Updated integration tests to use Client method API with `SetConfig`
6. Added `FakeClientOption` variadic pattern with `WithConfig` to `dockertest.NewFakeClient`
7. Updated documentation (CLAUDE.md files, rules, memories)
