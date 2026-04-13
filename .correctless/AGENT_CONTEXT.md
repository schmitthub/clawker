# Agent Context — Clawker

> Last updated: 2026-04-12

## What This Project Does

CLI tool for managing Docker-based development containers with security-focused egress firewall. Think "docker run" with opinionated naming, config, workspace management, and network security. Currently in alpha, working toward first stable release via control plane architecture.

## Key Components

See `.claude/docs/KEY-CONCEPTS.md` for the full type/abstraction index. Critical packages:

| Component | Location | Purpose |
|-----------|----------|---------|
| CLI entry | `cmd/clawker/` | Main binary, Cobra root |
| Control plane | `internal/controlplane/` | CP daemon, auth (TLS+OAuth2 via Hydra), gRPC AdminService |
| Auth | `internal/auth/` | CLI-side key material (CA, signing key, server cert), CP dial helper |
| Auth CLI | `internal/cmd/auth/` | `clawker auth rotate` — check/rotate auth material |
| CP binary | `cmd/clawker-cp/` | Containerized CP daemon entry point |
| Firewall | `internal/firewall/` | Envoy+CoreDNS+eBPF stack, rules store, MITM certs |
| eBPF | `internal/controlplane/ebpf/` | Cgroup BPF programs, manager |
| Config | `internal/config/` | Layered YAML config engine |
| Docker | `internal/docker/` | Clawker Docker middleware |
| Whail | `pkg/whail/` | Reusable Docker engine with label isolation |

## Quick Reference

| Need to... | Do this |
|------------|---------|
| Run unit tests | `make test` |
| Build | `make clawker` |
| Lint | `golangci-lint run ./...` |
| Regen protos | `make proto` |
| Regen CLI docs | `go run ./cmd/gen-docs --doc-path docs --markdown` |
| Find a spec | `.correctless/specs/{feature}.md` |
| Check architecture | `.claude/docs/ARCHITECTURE.md` |
| See known bugs | `.serena/memories/bug-tracker` |
| See outstanding features | `.serena/memories/outstanding-features` |
| CP brainstorm | `.serena/memories/brainstorm_the-controlplane-and-clawkerd` |
