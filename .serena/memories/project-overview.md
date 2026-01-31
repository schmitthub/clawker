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
go test ./...
go test -tags=integration ./internal/cmd/... -v -timeout 10m
go test -tags=e2e ./internal/cmd/... -v -timeout 15m
go test -tags=acceptance ./acceptance -v -timeout 15m
```

## Testing Tiers
1. **Unit** (`*_test.go`): No Docker, uses `runF` test seams and dockertest fakes
2. **Integration** (`*_integration_test.go`, tag `integration`): Real Docker daemon
3. **E2E** (`*_e2e_test.go`, tag `e2e`): Full binary + Docker + PTY
4. **Acceptance** (`acceptance/testdata/*.txtar`, tag `acceptance`): testscript-based CLI workflows

## Architecture
- Factory pattern: `cmdutil.Factory` struct with closure fields, constructor in `internal/cmd/factory/`
- Commands: `NewCmd(f, runF)` pattern — `runF` is the test seam
- Docker: `docker.Client` wraps `whail.Engine` wraps moby `APIClient`
- Mock chain: `dockertest.FakeClient` → function-field fakes → `docker.Client`
