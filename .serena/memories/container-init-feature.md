# Container Initialize Feature

## Overview

New containers with Claude Code require authentication and plugin installation each time. This is an exhausting
exercise for clawker users. This feature will allow support of copying a golden claude files to copy into the container's
home dir for initial claude code settings. There is plenty of existing infra to support this. Part of this feature
work will involve refactoring / auditing the existing code for workspace and setup mounts

reference documents, remember these exist but don't read them unless you need to:

- `@.serena/claude-code-authentication.md`: covers claude code authentication
- `@.serena/claude-code-settings.md`: covers claude code settings

### Claude Code Internals

* Claude code instances require a shared, hard coded ~/.claude.json file. This file tracks session information across system wide claude code instances using atomic writes "temp rename strategy, with writefs fallback". each claude code instance relies on this file and writes to it constantly with overall state like usage metrics, project registration, etc
* Claude code uses a ~/.claude/ directory to store plugins, user settings, statusline scripts, skill files, commmand files, plan files, task files. All claude code instances also share read/write this directory
* Claude stores authentication tokens in the system keyring if one exists or falls back to ~/.claude/.credentials.json. the schema is as follows (we already have a feature for this using "shared-globals" but it is brittle)
```json
{
  "claudeAiOauth": {
    "accessToken": "",
    "refreshToken": "",
    "expiresAt": 1770658802316,
    "scopes": [
      "",
      "",
      "",
      ""
    ],
    "subscriptionType": "",
    "rateLimitTier": ""
  },
  "organizationUuid": ""
}
```

## Implementation Status — ALL TASKS COMPLETE

### Task 1: Config Schema Types - COMPLETED
- Added `ClaudeCodeConfigOptions` struct with `Strategy` field
- Added `ClaudeCodeConfig` struct with `Config` and `UseHostAuth` fields
- Added `ClaudeCode *ClaudeCodeConfig` and `EnableSharedDir *bool` fields to `AgentConfig`
- Added helper methods: `UseHostAuthEnabled()` (default: true), `ConfigStrategy()` (default: "fresh"), `SharedDirEnabled()` (default: false)
- Added `validateClaudeCode` validator method (strategy must be "copy", "fresh", or empty)
- Updated `DefaultConfigYAML` with commented-out claude_code section
- All tests passing

### Task 2: ContainerFS Package - COMPLETED
- Created `internal/containerfs/` package with `ResolveHostConfigDir`, `PrepareClaudeConfig`, `PrepareCredentials`, `PrepareOnboardingTar`
- Full test coverage in `containerfs_test.go`

### Task 3: Refactor globals to share volume - COMPLETED
- Renamed clawker-globals to clawker-share
- Share volume conditional via `cfg.Config.Agent.SharedDirEnabled()`
- `EnsureConfigVolumes` returns `*ConfigVolumeResult`

### Task 4: Container Init Orchestration - DENIED (see review comments)
- `SetupMounts` now returns `*SetupMountsResult` with `Mounts` and `ConfigVolumeResult`
- Updated callers in `run/run.go` and `create/create.go`
- Created `internal/cmd/container/opts/init.go` with:
  - `InitContainerConfig(ctx, InitConfigOpts)` — orchestrates config copy + credential injection
  - `InjectOnboardingFile(ctx, InjectOnboardingOpts)` — writes onboarding marker to container
  - Function-field DI via `CopyToVolumeFn` and `CopyToContainerFn` for testability
- 10 unit tests in `init_test.go` covering all strategy/auth combinations
- All 3510 unit tests passing

### Task 5: Wire init into run and create commands - COMPLETED
- `run/run.go`: Added `InitContainerConfig` after SetupMounts (guarded by ConfigCreated), `InjectOnboardingFile` after ContainerCreate (guarded by UseHostAuthEnabled)
- `create/create.go`: Same two init blocks added
- Added `SetupCopyToContainer()` helper to `dockertest/helpers.go`
- Updated 3 test cases in `run_test.go` to set up CopyToContainer fake
- All 3510 unit tests passing

### Task 6: Integration Test Refactoring & CopyToVolume Ownership Fix - COMPLETED
- **CopyToVolume ownership bug**: Docker's `CopyToContainer` extracts tars as root regardless of tar header UID/GID.
  Fixed in `internal/docker/volume.go` by adding `chown -R 1001:1001` via busybox temp container after CopyToContainer.
  Defense-in-depth: `createTarArchive` still sets UID/GID 1001 in tar headers.
- **Test image UID mismatch**: Light test image (`test/harness/client.go`) now creates claude user with `-u 1001`
  to match production's `bundler.DefaultUID = 1001`. Previously Alpine's default UID 1000 caused permission denied
  on 0600-mode files owned by 1001.
- **Integration tests refactored**: `test/internals/containerfs_test.go` rewritten to exercise production code paths
  (`InitContainerConfig`, `InjectOnboardingFile`, `CopyToVolume`) instead of reimplementing injection manually.
  All test functions prefixed with `TestContainerFs_`.
- **Harness additions**: `WithVolumeMount`, `RunningContainer.FileExists/DirExists/ReadFile`, `UniqueAgentName`
  added to `test/harness/client.go` for reuse across all integration test suites.
- New whail type aliases: `SDKContainerStartOptions`, `SDKContainerWaitOptions` in `pkg/whail/types.go`
- All 3510 unit tests + all containerfs integration tests passing

### Task 7: Documentation updates - COMPLETED
- Updated serena memories: container-init-feature, claude-code-authentication
- Updated internal/docker/dockertest CLAUDE.md with SetupCopyToContainer


### Open Issues
The original agreed upon plan is stored in `@.serena/memories/container-init-original-plan.md`. Do not modify this file
#### Task 4:
- [x] Moved init orchestration from `opts/init.go` to `internal/cmd/container/shared/containerfs.go`. Updated all callers (run.go, create.go, test/internals/containerfs_test.go). Created `shared/CLAUDE.md`. `opts/` now contains only flag types as intended.

#### UAT Test Findings
- [x] `~/.claude.json` permissions and timestamp fixed: Mode 0644→0600, ModTime epoch→time.Now() in `PrepareOnboardingTar`. Test assertions added.
- [x] `installPath` properties in file `.claude/plugins/installed_plugins.json` — rewritten with container prefix via `rewriteJSONFile`
- [x] `installLocation` properties in file `.claude/plugins/known_marketplaces.json` — rewritten with container prefix via `rewriteJSONFile`
- [x] `projectPath` properties in file `.claude/plugins/installed_plugins.json` — replaced with `containerWorkDir` via `rewriteJSONFile`
- [x] `plugins/cache/` — now included in copy (cache exclusion removed from `stagePlugins`)
- [x] $CLAWKER_HOME/.clawker-share bind mount: Converted from Docker named volume (`mount.TypeVolume`) to host bind mount (`mount.TypeBind`). `EnsureShareDir()` resolves path via `config.ShareDir()` and creates with `config.EnsureDir()`. Created during `clawker init` (`performSetup`), re-created if missing during mount setup. Host directory never deleted by clawker.

##### Host absolute path locations
```shell
./plugins/known_marketplaces.json:    "installLocation": "/Users/andrew/.claude/plugins/marketplaces/ast-grep-marketplace",
./plugins/known_marketplaces.json:    "installLocation": "/Users/andrew/.claude/plugins/marketplaces/awesome-claude-skills",
./plugins/known_marketplaces.json:    "installLocation": "/Users/andrew/.claude/plugins/marketplaces/claude-code-personas-marketplace",
./plugins/known_marketplaces.json:    "installLocation": "/Users/andrew/.claude/plugins/marketplaces/claude-plugins-official",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/ast-grep-marketplace/ast-grep/1.0.0",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/feature-dev/2cd88e7947b7",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/security-guidance/2cd88e7947b7",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/code-review/2cd88e7947b7",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/pr-review-toolkit/2cd88e7947b7",
./plugins/installed_plugins.json:        "projectPath": "/Users/andrew/Code/clawker",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/gopls-lsp/1.0.0",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/code-simplifier/1.0.0",
./plugins/installed_plugins.json:        "projectPath": "/Users/andrew/Code/claude/claude-code-personas",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-code-personas-marketplace/personas/1.0.0",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/ralph-loop/2cd88e7947b7",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/claude-md-management/1.0.0",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/claude-code-setup/1.0.0",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/commit-commands/2cd88e7947b7",
./plugins/installed_plugins.json:        "installPath": "/Users/andrew/.claude/plugins/cache/claude-plugins-official/superpowers/4.2.0",
```
