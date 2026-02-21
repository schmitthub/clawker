# Config Namespace Refactor

## End Goal
Namespace all Viper config keys by their owning scope so that keys from different config files (settings.yaml, clawker.yaml, projects.yaml) never collide. Target: `project.build.image`, `settings.logging.max_size_mb`, `registry.projects`.

## Background & Context

### What Was Completed (Previous Sessions)
1. Replaced hand-coded validators with yaml.v3 strict decode.
2. Rewrote TestDefaults_YAMLDrift to use production paths.
3. Deleted legacy testdata/ directory.
4. Renamed `Project.Project` → `Project.Name` with yaml tag `name:`.
5. Fixed 10 test failures, added 15 comprehensive validation tests.

### Key Design Decisions
- **Set/Get take namespaced keys directly** — e.g. `Set("project.build.image", val)`. No flat-key translation in Set/Get. Callers adapt separately.
- **Dirty tracking uses namespaced keys** — `markDirtyPath("project.build.image")` creates tree: `project` → `build` → `image`. Top-level dirty nodes = scopes.
- **Scope derived from first key segment** — no explicit scope parameter needed for routing writes.
- **writeRootsToFile** reads namespaced keys from Viper (`scope.root`), strips prefix when writing to file (flat `root:`).
- **Project writes always target repo-level file** when it exists (user-level + repo-level merge, repo takes precedence).
- **Env vars keep original names** — `v.BindEnv("project.build.image", "CLAWKER_BUILD_IMAGE")` explicitly binds namespaced Viper key to flat env var.
- **Viper handles dotted key validation internally** — no need for a separate key path library.
- Confirmed via deepwiki: `MergeConfigMap({"project": {"build": {"image": "alpine"}}})` supports `v.Get("project.build.image")` and `v.Get("project.build")` returns full subtree.

## Current State of Code (Latest Session's Changes)

### Production Code Changes

**config.go — `Project()` method (line ~561):**
- CHANGED: Replaced `c.v.UnmarshalKey("project", p)` with `AllSettings()` + sub-Viper approach
- ROOT CAUSE: Viper's `UnmarshalKey("project")` calls `v.Get("project")` which returns config-layer subtree WITHOUT merging defaults. When `MergeConfigMap` adds a "project" entry to config layer, `v.Get("project")` returns that map without default values for missing nested keys (e.g. `workspace.remote_path`). Same issue with env var overrides — they're not included in the subtree.
- FIX: `AllSettings()` properly merges all layers (config, defaults, env). Extract `all["project"]`, create a sub-Viper, and `Unmarshal` from it.
- This fix resolves: TestReadFromString_OverridesDefault, TestNewConfig_AppliesEnvOverride, TestNewConfig_LeafEnvVarOverridesConfigValue, TestLoad_Testdata_DefaultsPreservedAfterMerge

### Test Changes (config_test.go)

**`mustConfigFromFile` helper (line ~38):**
- CHANGED: Replaced `v.ReadInConfig()` (loads flat keys) with yaml.v3 parse + `NamespaceMapForTest(flat, ScopeProject)` + `v.MergeConfigMap(wrapped)`
- Added `"gopkg.in/yaml.v3"` import to config_test.go
- Keeps `v.SetConfigFile(path)` for Watch test support

### Pre-existing Compiler Issues
- `configImpl` doesn't implement `Config` interface — missing `ProjectConfigFileName()` method
- This is NOT caused by namespace refactor, it's from a previous rename session
- `go build ./internal/config/...` compiles clean (the interface mismatch only shows in test files that cast to Config)

### Test Status After This Session
- Production `Project()` fix: DONE
- `mustConfigFromFile` fix: DONE
- Remaining test key updates: NOT STARTED (see Phase 4 below)
- 2 pre-existing failures: `TestReadFromString_UnknownKey`, `TestReadFromString_UnknownRootKey` (Phase 5)

## Key Viper Limitation Discovered

**`v.Get(subtreeKey)` does NOT deep-merge across layers.** When Viper has:
- Config layer: `project.workspace.default_mode = "snapshot"` (from MergeConfigMap)
- Default layer: `project.workspace.remote_path = "/workspace"`

Calling `v.Get("project")` returns ONLY the config-layer map `{workspace: {default_mode: "snapshot"}}`, missing the default `remote_path`. Individual leaf key access `v.Get("project.workspace.remote_path")` correctly falls through to defaults.

**Impact:** `UnmarshalKey("project")` uses `v.Get("project")` internally, so it gets the incomplete map. Fix: use `AllSettings()` which iterates `AllKeys()` and calls `v.Get(leaf)` for each, properly merging all layers.

## TODO Sequence

### Phase 1: Namespace Viper Keys ✅
- [x] 1-5. All helpers, defaults, load, mergeProjectConfig, ReadFromString

### Phase 2: Update Getters ✅
- [x] 6-8. Project, Settings, LoggingConfig, HostProxyConfig, MonitoringConfig

### Phase 3: Update Set/Get/Write ✅
- [x] 9-14. Set, Get, Write, writeRootsToFile, writeDirtyRootsForScope, bindSupportedEnvKeys, projectRootFromCurrentDir

### Phase 3.5: Fix Viper Layer Merge Bug ✅
- [x] Fix `Project()` to use `AllSettings()` + sub-Viper instead of `UnmarshalKey`

### Phase 4: Fix Remaining Tests ✅
All test key updates completed. Also fixed:
- export_test.go cleanup: removed redundant DefaultXYAMLForTest re-exports, standardized naming to ForTest suffix, converted methods-on-types to standalone passthrough functions
- config_test.go compilation: NewConfigWithViperForTest→NewConfigForTest, method calls→standalone function calls
- All 13 namespace key updates done (Set/Get/Write calls + file assertions)
- stubs_test.go: "projects"→"registry.projects"

### Phase 5: Re-add ReadFromString Strict Validation ✅
- [x] 33. Added `schemaForScope()` helper + per-scope `validateYAMLStrict` calls in `ReadFromString` (marshal grouped map back to YAML, validate against Project/Settings/ProjectRegistry schema)
- [x] 34. Both `TestReadFromString_UnknownKey` and `TestReadFromString_UnknownRootKey` pass