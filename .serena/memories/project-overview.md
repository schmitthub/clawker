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
go test ./test/internals/... -v -timeout 10m     # Internal integration (Docker)
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests (Docker)
go test ./test/agents/... -v -timeout 15m        # Agent E2E (Docker)
make test-all                                    # All test suites
```

## Testing Tiers
1. **Unit** (`*_test.go` co-located): No Docker, uses `runF` test seams and dockertest fakes
2. **Internals** (`test/internals/`): Container scripts/services, real Docker daemon
3. **CLI** (`test/cli/`): Testscript-based CLI workflow validation
4. **Agents** (`test/agents/`): Full clawker images, real agent tests

No build tags — directory separation only.

## Architecture
- Factory pattern: `cmdutil.Factory` struct with closure fields, constructor in `internal/cmd/factory/`
- Commands: `NewCmd(f, runF)` pattern — `runF` is the test seam
- Docker: `docker.Client` wraps `whail.Engine` wraps moby `APIClient`
- Mock chain: `dockertest.FakeClient` → function-field fakes → `docker.Client`
