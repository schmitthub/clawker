# Project map

- Clawker manages AI agent containers through Docker. Resource labels determine ownership; names alone do not.
- Project resolution uses the registry. Config receives a resolved project root; it does not resolve project identity.
- The control plane (CP) runs whenever managed agent containers exist. The firewall is an optional CP subsystem.
- Follow `AGENTS.md`, the nearest package `AGENTS.md`, and applicable `.claude/rules/` files. Package source defines the current API.
- Design references: `.claude/docs/DESIGN.md`, `.claude/docs/ARCHITECTURE.md`. Full directory map: `.claude/docs/REPO-STRUCTURE.md`.

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
