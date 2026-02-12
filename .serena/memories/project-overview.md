# Clawker Project Overview

## Purpose
CLI tool for managing Docker-based development containers, with Claude Code integration. Think "docker run" with opinionated naming, config, and workspace management.

## Tech Stack
- Go (1.22+), Cobra CLI, zerolog, BubbleTea/Lipgloss TUI
- Docker SDK via `pkg/whail` abstraction layer
- Function-field fakes via `dockertest.NewFakeClient()` (no gomock)

## Key Commands
```bash
go build -o bin/clawker ./cmd/clawker
make test                                        # Unit tests (no Docker)
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests (Docker)
go test ./test/commands/... -v -timeout 10m      # Command integration (Docker)
go test ./test/internals/... -v -timeout 10m     # Internal integration (Docker)
go test ./test/agents/... -v -timeout 15m        # Agent E2E (Docker)
make test-all                                    # All test suites
```

## Testing Tiers
1. **Unit** (`*_test.go` co-located): No Docker, uses `runF` test seams and dockertest fakes
2. **CLI** (`test/cli/`): Testscript-based CLI workflow validation
3. **Commands** (`test/commands/`): Command integration tests (container create/exec/run/start)
4. **Internals** (`test/internals/`): Container scripts/services (firewall, SSH, entrypoint)
5. **Agents** (`test/agents/`): Full agent lifecycle, ralph tests

No build tags — directory separation only. All Docker tests use `harness.BuildLightImage` + `harness.RunContainer` (dogfooded on `docker.Client`).

## Recent Changes
- **Label domain migration** (2026-02-12): Migrated Docker label prefix from `com.clawker.*` to `dev.clawker.*` (OCI reverse-DNS convention for `clawker.dev` domain). Added `config.Domain`, `config.LabelDomain` constants in `internal/config/identity.go`. All label key constants (`LabelPrefix`, `LabelManaged`, `LabelProject`, etc.) and `ManagedLabelValue`, `EngineLabelPrefix`, `EngineManagedLabel` now canonical in `config/identity.go` — `docker/labels.go` re-exports them so lightweight packages (e.g. `hostproxy`) can use labels via `config.*` without importing docker's heavy dependency tree. Centralized UID/GID (`config.ContainerUID`/`ContainerGID`) — replaces hardcoded 1001 in bundler, docker/volume, containerfs, test harness.

## Recent Changes (Continued)
- **Agent name validation + orphan volume cleanup** (2026-02-12): `ValidateResourceName` in `docker/names.go` validates agent/project names against Docker container name rules (`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`, max 128). `ContainerName`, `VolumeName`, `ContainerNamesFromAgents` now return `(string, error)` / `([]string, error)` — validation intrinsic to name construction. ~30 callers updated. `ContainerInitializer.runSteps` uses named returns + deferred cleanup to remove orphaned volumes on init failure. `CleanupWarnings()` method surfaces cleanup results to callers for stderr output.

## Recent Features
- **agent.env_file / agent.from_env / agent.env** (2026-02-11): YAML fields on AgentConfig for runtime env var injection. `env_file` loads env files with path expansion (`~`, `$VAR` — strict: unset vars error). `from_env` passes host env vars by name (unset vars skipped with warning via `InitResult.Warnings`; set-but-empty included). `env` for static values. Precedence: env_file < from_env < env. Resolved via `config.ResolveAgentEnv(agent, dir) (map, []string, error)` in `shared/init.go`'s `buildRuntimeEnv`. Validated in `validator.go` (path existence via full os.Stat, empty-string rejection, var name format). Path helpers (`expandPath`, `resolvePath`) return errors. Injectable `userHomeDir` for testing.
- **agent.post_init** (2026-02-11): YAML multiline shell script field on AgentConfig. Injected as `~/.clawker/post-init.sh` via `containerfs.PreparePostInitTar` + `shared.InjectPostInitScript`. Entrypoint runs once on first start (marker: `~/.claude/post-initialized`). Skipped on restart. PR review fixes applied: conditional marker on success, output capture in errors, input validation, doc clarifications.