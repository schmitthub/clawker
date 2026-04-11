# Clawker Project Overview

## Purpose
CLI tool for managing Docker-based development containers, with Claude Code integration. Think "docker run" with opinionated naming, config, and workspace management.

## Tech Stack
- Go (1.22+), Cobra CLI, zerolog, BubbleTea/Lipgloss TUI
- Docker SDK via `pkg/whail` abstraction layer
- Config via `internal/storage.Store[T]` (replaced Viper 2026-02-22)
- Function-field fakes via `mocks.NewFakeClient` (no gomock)

## Key Commands
```bash
go build -o bin/clawker ./cmd/clawker
make test                                        # Unit tests (no Docker)
make test-all                                    # All test suites
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests (Docker)
go test ./test/commands/... -v -timeout 10m      # Command integration (Docker)
go test ./test/internals/... -v -timeout 10m     # Internal integration (Docker)
go test ./test/agents/... -v -timeout 15m        # Agent E2E (Docker)
```

## Testing Tiers
1. **Unit** (`*_test.go` co-located): No Docker, uses `runF` test seams and docker/mocks fakes
2. **CLI** (`test/cli/`): Testscript-based CLI workflow validation
3. **Commands** (`test/commands/`): Command integration tests
4. **Internals** (`test/internals/`): Container scripts/services (firewall, SSH, entrypoint)
5. **Agents** (`test/agents/`): Full agent lifecycle, loop tests

No build tags — directory separation only.

## Architecture Highlights
- **Config**: `storage.Store[T]` typed layered YAML engine. `internal/config` composes `Store[Project]` + `Store[Settings]` with direct store accessors (`cfg.ProjectStore()`, `cfg.SettingsStore()`). `internal/project` owns `Store[ProjectRegistry]`. Old wrapper methods (`SetProject`, `WriteSettings`, etc.) deprecated.
- **Labels**: `dev.clawker.*` prefix (OCI reverse-DNS). All label keys via `config.Config` methods.
- **Container creation**: `shared.CreateContainer()` single entry point with events channel for progress.
- **Home dir safety**: `shared.IsOutsideHome(".")` guards `run`/`create` (prompt) and `loop` (hard error) when CWD is at or above `$HOME`.
- **Image resolution**: 3-source chain — Docker labels (`ImageSourceProject`) → config `build.image` (`ImageSourceConfig`) → nil. `clawker init` persists built image to user-level `clawker.yaml`.
- **Loop system**: `loop iterate` (repeated prompt) + `loop tasks` (task-file-driven) with TUI dashboard.
- **Test doubles**: `configmocks.NewBlankConfig()`, `configmocks.NewFromString()`, `configmocks.NewIsolatedTestConfig(t)`.
