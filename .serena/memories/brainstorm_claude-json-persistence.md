# Brainstorm: Claude JSON Persistence in Containers

> **Status:** Completed
> **Created:** 2026-02-23
> **Last Updated:** 2026-02-24 16:00

## Problem / Topic
Claude Code's `/resume` slash command couldn't find sessions in containers because it resolves worktree paths from git metadata (host-absolute paths) and scans session directories for those mangled paths. Sessions stored under `-workspace/` (the container cwd) didn't match any host-path-mangled directories.

## Decisions Made
- **Host-path mount approach implemented** — container workspace mounted at host absolute path instead of `/workspace`
- Non-worktree containers: mount at project root (e.g., `/Users/andrew/Code/clawker`)
- Worktree containers: mount at worktree host path (e.g., `/Users/andrew/.local/share/clawker/worktrees/...`)
- `workspace.remote_path` config field removed entirely (dead code — always overridden by `ContainerPath`)
- Dockerfile `WORKDIR /workspace` stays as image default; overridden at runtime by `container.Config.WorkingDir`
- `--workdir` CLI flag still takes precedence if user provides it

## Implementation (Completed)

### Files Changed
- `internal/workspace/setup.go` — Added `ContainerPath` to `SetupMountsConfig` and `SetupMountsResult`; `SetupMounts()` uses `ContainerPath` directly (no fallback)
- `internal/cmd/container/shared/container.go` — `CreateContainer()` passes `wd` as `ContainerPath`, uses `wsResult.ContainerPath` for `InitConfigOpts.ContainerWorkDir`, sets `containerConfig.WorkingDir` when `--workdir` not provided
- `internal/project/worktree_service.go` — Fixed symlink bug: removed `GetProjectRoot()` calls, methods now accept `projectRoot` parameter from handle
- `internal/project/manager.go` — Updated `projectHandle` to pass `p.record.Root` to worktree service
- `docs/container-internals.mdx` — New user-facing docs covering full container lifecycle and session persistence

### Bug Fix: prune_skips_locked_worktrees
Root cause: `worktreeService` re-resolved project root via `GetProjectRoot()` → `storage.ResolveProjectRoot()` which uses `os.Getwd()`. On macOS, `t.TempDir()` returns `/var/...` but `os.Getwd()` after `os.Chdir` returns `/private/var/...`. `filepath.Rel` couldn't match them. Fix: service now uses the handle's known root instead of re-resolving.

### Documentation Updated
- `docs/container-internals.mdx` — New page covering full container lifecycle, path mirroring, session persistence
- `docs/worktrees.mdx` — Updated with multi-layer health checks, lock detection, pruning rules, path mirroring, container mount details
- `docs/docs.json` — Added `container-internals` to navigation
- `internal/workspace/CLAUDE.md` — Updated `SetupMountsConfig`/`SetupMountsResult` with `ContainerPath` field
- `internal/cmd/container/shared/CLAUDE.md` — Updated `ContainerWorkDir` description
- `internal/project/CLAUDE.md` — Updated worktree service API (methods accept `projectRoot` param, root resolution section)

### Verification
- All 3,913 unit tests pass
- User confirmed `/resume` works with the new mount path

## Key Technical Insights

### Claude Code Internals (Reverse-Engineered, v2.1.52)
- Config home: `CLAUDE_CONFIG_DIR ?? ~/.claude`
- Config file: checks `$configHome/.config.json` first (migration), falls back to `~/.claude.json`
- Session dir: `~/.claude/projects/mangle(cwd)/` where `mangle()` replaces non-alphanumeric with `-`
- `/resume` discovers worktrees via `.git` metadata → scans session dirs for mangled worktree paths
- `--continue` reads `lastSessionId` from `.config.json[projects][cwd]` directly (bypasses worktree discovery)
- `--resume <id>` loads from `projects/mangle(cwd)/<id>.jsonl` directly

### Architecture
- Named volumes (config, history) are keyed by project+agent name, always mount at fixed paths — completely independent of workspace mount path
- Workspace is always a bind mount or snapshot volume — never a persistent named volume
- The `.git` directory mount uses `Source == Target` (same absolute path) for worktree reference resolution
