# Build inputs

- Module: `github.com/schmitthub/clawker`. Use the Go version in `go.mod`; it replaces the old Go 1.22+ note.
- `go.mod` and `go.sum` are the version and integrity records. Do not copy the full dependency list into memory.
- CLI: Cobra and pflag. Logging: zerolog through `internal/logger`. Terminal display: Bubble Tea, Bubbles, and Lip Gloss behind project packages.
- Docker: Moby client/API and BuildKit. `pkg/whail` wraps the client; `internal/docker` adds clawker ownership rules.
- Config: `yaml.v3` node trees through `internal/storage`. Agent registry: SQLite. These have separate owners and persistence models.
- CP protocols: protobuf/gRPC, mTLS, and Ory authentication. Firewall: Envoy, custom CoreDNS, and cilium/ebpf.
- Monitoring: OpenTelemetry Collector, Prometheus, OpenSearch, and OpenSearch Dashboards.
- Tests: Go testing, testify, generated moq mocks, package fakes, and golden files.
- `Makefile` defines binary embeds, BPF toolchain inputs, protobuf generation, docs, and tests. `prek.toml` defines local hooks; `.golangci.yml` defines lint checks.
- Pin external inputs to exact versions or commit hashes. Container image pins must identify multi-architecture manifest lists. Generated binaries and BPF outputs are not committed.
- `clawker-plugin/` is a Git submodule with its own history. Its source changes and the parent pointer change are separate commits.
- For exact build and generation commands: `mem:suggested_commands`.
