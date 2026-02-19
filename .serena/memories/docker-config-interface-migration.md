# Docker Package Migration to config.Config Interface

> **Status:** In Progress — Test file migration phase
> **Branch:** `refactor/configapocalypse`
> **Last updated:** 2026-02-19

## Goal

Migrate `internal/docker` to use the `config.Config` interface instead of the old `*config.Config` struct pointer and removed package-level constants.

## Key Design Decisions Made With User

1. **No changes to `internal/config` package** — all migration work stays in docker.
2. **Label functions became Client pointer methods** — `ContainerLabels()`, `VolumeLabels()`, `ImageLabels()`, `NetworkLabels()`, `GlobalVolumeLabels()`, `ClawkerFilter()`, `ProjectFilter()`, `AgentFilter()` all moved from standalone functions to `(*Client)` methods that read label keys from `c.cfg`.
3. **No self-defined label constants** — the user explicitly rejected hardcoding label strings. All label values come from `config.Config` interface methods.
4. **`SetConfig()` method deleted** — user called it "stupid". Tests construct clients with config at creation time.
5. **Test configs use `config.ReadFromString(yaml)` pattern** — same as bundler's `testConfig` helper. No `NewConfigForTest` bridge needed.
6. **Nil config tests dropped** — since `Client.cfg` is now `config.Config` (interface, always set), nil config tests are no longer meaningful.
7. **`TestLabelConfig()` takes `cfg config.Config` as first param** — it's a standalone function in defaults.go that needs label values.
8. **`NewFakeClient` in dockertest takes `cfg config.Config` as first param** — replaces the old `WithConfig(*config.Config)` option.
9. **`parseContainers` became a Client method** — it needs label keys for parsing container summaries.
10. **`FakeClient.Cfg` field added** — stores the config passed to `NewFakeClient` so Setup helpers and callers can read label keys.
11. **`defaultCfg = config.NewMockConfig()` in dockertest/helpers.go** — standalone fixture functions (`ContainerFixture`, `RunningContainerFixture`) use this package-level config to avoid cascading cfg params to 27+ caller files.
12. **`managedImageInspect` in helpers.go takes `cfg config.Config` param** — called only from FakeClient methods which pass `f.Cfg`.

## Lessons Learned

- **LSP false positives** — gopls reports stale "no field or method" errors on `config.Config` and false "copylocks" warnings. The real compiler (`go build`) is authoritative. Ignore LSP diagnostics during this migration.
- **Unexported `cfg` field** — `Client.cfg` is private, so cross-package test code (like `dockertest`) can't set it via struct literal. Solution: `NewClientFromEngine(engine, cfg)` constructor.
- **Viper dotted-key expansion** — Viper's mapstructure treats dotted keys in `map[string]string` YAML fields (e.g. `dev.clawker.project` under `labels:`) as nested paths, creating `map[string]interface{}` instead of flat strings. The user patched this in `config.go` with a `DecoderConfigOption` that preserves flat map keys. Documented in config/CLAUDE.md gotchas.
- **Build scope** — `go build ./...` fails due to unrelated `configtest` removal. Always scope to `go build ./internal/docker/...` during docker migration.
- **External cascade pattern** — When `NewFakeClient` signature changes, ~150+ callers across `internal/cmd/` break. The `defaultCfg` package-level var in `dockertest/helpers.go` limits cascade for fixture functions, but `NewFakeClient()` callers must all be updated.

## Step-by-Step Progress

### DONE — Production code (all compiles: `go build ./internal/docker/...` passes)
- [x] **labels.go** — All label functions converted to `(*Client)` methods using `c.cfg.LabelManaged()` etc.
- [x] **client.go** — `cfg` field changed to `config.Config`, `SetConfig()` deleted, `parseContainers` converted to Client method.
- [x] **volume.go** — `config.ContainerUID`/`config.ContainerGID` → `c.cfg.ContainerUID()`/`c.cfg.ContainerGID()`.
- [x] **defaults.go** — `config.BuildDir()` → `c.cfg.BuildSubdir()`, `TestLabelConfig` takes `cfg config.Config`.
- [x] **image_resolve.go** — `ResolveDefaultImage` takes `config.Settings` value type.
- [x] **builder.go** — `mergeImageLabels` calls `b.client.ImageLabels(...)`.
- [x] **builder_test.go** — Full rewrite with `testConfig` helper, YAML fixtures.
- [x] **image_resolve_test.go** — Full rewrite with YAML fixtures.
- [x] **dockertest/fake_client.go** — `NewFakeClient(cfg, opts...)`, `FakeClient.Cfg` field, `WithConfig` removed.

### DONE — Test file migration (this session)
- [x] **labels_test.go** — Full rewrite: `testClient()` helper creates `&Client{cfg: cfg}`, all tests call client methods, all assertions use `cfg.Label*()`. Uses `testConfig` from builder_test.go (same package).
- [x] **client_test.go** — Full rewrite: `TestParseContainers` uses `c.parseContainers()` with cfg, `clawkerEngine` now takes `(cfg, fake)`, all `LabelProject`/etc constants replaced with `cfg.LabelProject()`. Added `require` import.
- [x] **dockertest/helpers.go** — Added `config` import, `defaultCfg = config.NewMockConfig()`, `ContainerFixture` uses `defaultCfg.Label*()`, FakeClient methods use `f.Cfg.Label*()`, `managedImageInspect` takes `cfg config.Config`.

### DONE — Remaining test files within docker package
- [x] **client_progress_test.go** — Added `config` import + `progressCfg = config.NewMockConfig()`, updated 7 `clawkerEngine()` calls.
- [x] **fake_client_test.go** — Added `config` import + `cfg = config.NewMockConfig()`, updated all `NewFakeClient()` → `NewFakeClient(cfg)`, replaced `docker.Label*` → `cfg.Label*()`.
- [x] **wirebuildkit_test.go** — Added `config` import, `NewFakeClient()` → `NewFakeClient(config.NewMockConfig())`.
- [x] **client.go** — Added `NewClientFromEngine(engine, cfg)` test constructor for cross-package test creation.
- [x] **fake_client.go** — Changed `&docker.Client{Engine: engine}` → `docker.NewClientFromEngine(engine, cfg)`.
- [x] **builder_test.go** — Viper dotted-key bug in `TestMergeImageLabels_InternalLabelsOverrideUser` resolved by config package fix (not test workaround).

### TODO — External caller cascade (massive — ~150+ call sites)
- [ ] **All `dockertest.NewFakeClient()` callers** — Signature changed to `NewFakeClient(cfg, opts...)`. Every caller needs `config.NewMockConfig()` (or their own config) as first arg. Key files:
  - `cmd/fawker/factory.go` (also uses deleted `dockertest.WithConfig`)
  - `internal/cmd/container/*/` test files (~80+ calls)
  - `internal/cmd/loop/shared/` test files (~15+ calls)
  - `internal/cmd/image/build/` test files (use `WithConfig`)
  - `internal/cmd/image/list/` test files
  - `internal/cmd/init/init_test.go`
  - `internal/docker/wirebuildkit_test.go`
  - See full list via: `grep -rn 'dockertest.NewFakeClient(' --include='*.go'`
- [ ] **External callers of label functions** — `ContainerLabels`, `VolumeLabels`, etc. are now Client methods. Search `internal/cmd/` and `internal/workspace/` for `docker.ContainerLabels(`, `docker.VolumeLabels(` etc.
- [ ] **External callers of `docker.TestLabelConfig`** — signature changed to `TestLabelConfig(cfg, testName...)`.
- [ ] **External callers of `docker.LabelManaged` etc.** — Constants no longer exist. Any external package referencing them needs migration.
- [ ] **External callers using `dockertest.WithConfig(cfg)`** — Option deleted. Change to `dockertest.NewFakeClient(cfg, ...)`.

### TODO — Verification & docs
- [ ] **Verify build**: `go build ./internal/docker/...`
- [ ] **Verify tests**: `go test ./internal/docker/... -v -count=1`
- [ ] **Update docker/CLAUDE.md** — Document new method signatures, removed constants, changed test patterns.
- [ ] **Update memories** — configapocalypse-prd migration status table.
- [ ] **Update docs references** — Several `.claude/` docs reference old `NewFakeClient()` pattern.

## Files Modified (on disk, uncommitted)
- `internal/docker/labels.go`
- `internal/docker/client.go` *(added NewClientFromEngine)*
- `internal/docker/volume.go`
- `internal/docker/defaults.go`
- `internal/docker/image_resolve.go`
- `internal/docker/builder.go`
- `internal/docker/builder_test.go`
- `internal/docker/image_resolve_test.go`
- `internal/docker/dockertest/fake_client.go` *(uses NewClientFromEngine)*
- `internal/docker/labels_test.go`
- `internal/docker/client_test.go`
- `internal/docker/client_progress_test.go` *(config threading)*
- `internal/docker/dockertest/fake_client_test.go` *(config + label migration)*
- `internal/docker/dockertest/helpers.go`
- `internal/docker/wirebuildkit_test.go` *(config threading)*
- `internal/config/config.go` *(Viper dotted-key fix for map[string]string)*
- `internal/config/config_test.go` *(dotted-key test)*
- `internal/config/CLAUDE.md` *(dotted-key gotcha documented)*

## Current Build State
- `go build ./internal/docker/...` — **PASSES**
- `go test ./internal/docker/...` — **PASSES** (all docker package tests green)

## IMPERATIVE
**Always check with the user before proceeding with the next TODO item.** Show proposed code changes and get approval. The user is hands-on and wants to review each change. If all work is done, ask the user if they want to delete this memory.
