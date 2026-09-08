# Project map

- Clawker manages AI agent containers through Docker. Resource labels determine ownership; names alone do not.
- Project resolution uses the registry. Config receives a resolved project root; it does not resolve project identity.
- The control plane (CP) runs whenever managed agent containers exist. The firewall is an optional CP subsystem.
- Follow `AGENTS.md`, the nearest package `AGENTS.md`, and applicable `.agents/rules/` files. Package source defines the current API.
- Root `AGENTS.md` requires every rule in `.agents/rules/` (repository-wide only; each rule's `paths:` sets its scope) and the package `AGENTS.md` chain for each target file, including files outside the initial working directory. There is no separate rule or task index. Shared procedures are in `.agents/skills/`; `.claude/skills` links to that directory. Authored references are in `.agents/docs/`. Native entry paths and the source rationale are in `.agents/README.md`. GitHub Copilot files serve pull request code review.
- `.agents/` contains harness-independent content. Keep native settings, reviewer definitions, and hooks specific to one tool as regular files in `.claude/` or `.codex/`. Do not move native configuration into shared directories or use links to hide its ownership.
- The named `test-hunter` reviewer uses one shared skill. Claude's `.claude/agents/test-hunter.md` preloads it. Codex's `.codex/agents/test-hunter.toml` requires a read of the same skill; `config_file = "agents/test-hunter.toml"` in `.codex/config.toml` selects the native role.
- Both tools use the same project instruction files, `.agents/skills`, `.agents/rules`, and Serena memory graph. `.claude/rules` and `.claude/skills` are directory links. Both native configurations register the same Git and Go guards. Keep required knowledge in shared files.
- The `agent-compat` commit hook and the PR lint workflow (`agent-compat` job) run `scripts/check-agent-compatibility.py`. Never run it by hand. It validates what exists: `AGENTS.md` + `CLAUDE.md -> AGENTS.md` pairs, relative in-repo symlinks under `.agents`/`.claude`/`.codex` (shared links never resolve into a harness dir), Codex `config_file` values, and navigation links. It forces no directory links, sidecars, or named files. Subagents: a skill's `agents/claude.md` is linked from `.claude/agents/<name>.md`; its `agents/codex.toml` is named by `[agents.<name>] config_file` in `.codex/config.toml`. Use `.agents/docs/development.md` for commands and dependency pins; `controlplane/AGENTS.md` (Control-plane safety section) for CP safety. `.agents/rules/` holds only repository-wide rules; package rules live in package `AGENTS.md` files.
- For context and memory file changes, use canonical documentation, prior art, source inspection, and file checks. Do not run application tests. Do not install or run Claude Code for validation.
- Design references: `.agents/docs/DESIGN.md`, `.agents/docs/ARCHITECTURE.md`. Full directory map: `.agents/docs/REPO-STRUCTURE.md`.

- The docsweep workflow is retired by user request. Do not recreate it as a portable skill; it required its original Claude workflow runtime.

- The audit-memory skill requires an explicit user request in its shared description and procedure. This is an instruction to both agents, not a native loading restriction. It has no vendor-specific metadata or per-tool invocation override.

## Work rules

- For memory placement, naming, and links: `mem:memory_maintenance`.
- For dependency versions, generated assets, and build inputs: `mem:tech_stack`.
- For development and host test commands: `mem:suggested_commands`.
- For dependency injection, errors, output, and instruction files: `mem:conventions`.
- For the checks required before completion: `mem:task_completion`.

## Module map

| Work area | Memory and source |
| --- | --- |
| CLI commands and Docker client boundaries | `mem:cli/core`; `internal/cmd/`, `internal/docker/`, `pkg/whail/` |
| Config, project identity, and file locations | `mem:config/core`; `internal/config/`, `internal/project/`, `internal/consts/` |
| YAML merge, mutation, and persistence | `mem:storage/core`; `internal/storage/` |
| CP startup, shutdown, and event ownership | `mem:controlplane/core`; `internal/controlplane/`, `controlplane/` |
| Container startup, workspaces, and host services | `mem:runtime/core`; `clawkerd/`, `internal/workspace/`, `internal/hostproxy/` |
| Harness, stack, and bundle resolution | `mem:bundle/core`; `internal/bundle/`, `internal/bundler/` |
| Collector, metrics, logs, and monitoring extensions | `mem:monitoring/core`; `internal/monitor/` |
| Test helpers, test limits, and integration checks | `mem:testing/core`; `internal/testenv/`, `test/` |

## Retained records

These topic indexes contain dated records. Check current source before reusing their claims.

- For proposals and unresolved implementation phases: `mem:plans/core`.
- For bug, feature, and plugin reports that need a status check: `mem:tracking/core`.
- For provider comparisons and source methodology: `mem:research/core`.
- For prior architecture, migrations, and limited approvals: `mem:history/core`.
- For operator-directed tests and prior incidents: `mem:security/core`.
