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
go test ./test/e2e/... -v -timeout 10m           # E2E integration tests (Docker)
go test ./test/whail/... -v -timeout 5m          # Whail BuildKit integration (Docker)
```

## Testing Tiers
1. **Unit** (`*_test.go` co-located): No Docker, uses `runF` test seams and docker/mocks fakes
2. **E2E** (`test/e2e/`): Full CLI harness — firewall, mounts, migrations, presets, workflows
3. **Whail** (`test/whail/`): BuildKit integration tests against real Docker
4. **Adversarial** (`test/adversarial/`): Live red-team C2 harness (not an automated suite)

No build tags — directory separation only.

## Architecture Highlights
- **Config**: `storage.Store[T]` typed layered YAML engine. `internal/config` composes `Store[Project]` + `Store[Settings]` with direct store accessors (`cfg.ProjectStore()`, `cfg.SettingsStore()`). `internal/project` owns `Store[ProjectRegistry]`. Old wrapper methods (`SetProject`, `WriteSettings`, etc.) deprecated.
- **Labels**: `dev.clawker.*` prefix (OCI reverse-DNS). All label keys via `config.Config` methods.
- **Container creation**: `shared.CreateContainer()` single entry point; diagnostics go to zerolog and the caller owns terminal output.
- **Home dir safety**: `shared.IsOutsideHome(".")` guards `run`/`create` (prompt) when CWD is at or above `$HOME`.
- **Image resolution**: 3-source chain — Docker labels (`ImageSourceProject`) → config `build.image` (`ImageSourceConfig`) → nil. `clawker init` persists built image to user-level `clawker.yaml`.
- **Control Plane (landed 2026-04)**: `clawker-controlplane` container runs `clawkercp` daemon (PID 1) + Ory stack (Hydra/Oathkeeper/Kratos). CLI dials CP via mTLS TCP + OAuth2 JWT (Hydra introspection). CP owns `ebpf.Manager.Load()` in-process. See `internal/controlplane/CLAUDE.md`.
- **CP ≠ firewall**: CP is unconditional infrastructure (auth, gRPC AdminService + AgentService, agent registry, agent.Dialer→clawkerd Session, overseer event bus, owns clawker-net). The firewall is one optional subsystem CP manages, toggled by `firewall.enable` in `settings.yaml` (the master switch is global). The project schema's `security.firewall` field holds per-project `add_domains`/`rules` ONLY — not the enable flag. Disabling firewall does NOT disable CP, clawkerd Register, ListAgents, or any non-firewall AdminService RPC. CP owns firewall, not vice versa.
- **Agent subsystem (landed 2026-05)**: `internal/controlplane/agent/` consolidates the prior `agentdial/` + `agentregistry/` packages into one axis-keyed package. `agent.Start` is the single entry point wiring registry reap, dockerevents evict, and container-start dial. `overseer.Overseer` is the typed event bus + worldview state. `adminclient.Dial` (moved from `auth/cp_dial.go`) handles CLI→CP gRPC with mTLS + auto-refreshing OAuth2 bearer. `firewall.ActionQueue` serializes all mutation.
- **Test doubles**: `configmocks.NewBlankConfig()`, `configmocks.NewFromString()`, `configmocks.NewIsolatedTestConfig(t)`.
