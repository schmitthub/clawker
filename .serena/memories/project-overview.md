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

## Recent Features
- **agent.post_init** (2026-02-11): YAML multiline shell script field on AgentConfig. Injected as `~/.clawker/post-init.sh` via `containerfs.PreparePostInitTar` + `shared.InjectPostInitScript`. Entrypoint runs once on first start (marker: `~/.claude/post-initialized`). Skipped on restart. PR review fixes applied: conditional marker on success, output capture in errors, input validation, doc clarifications.