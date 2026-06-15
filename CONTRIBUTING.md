# Contributing to Clawker

Thanks for your interest in contributing to Clawker! This project is currently in alpha, maintained by a solo developer opening up for community contributions. All skill levels welcome.

## Getting Started

### Prerequisites

- **Go 1.25+**
- **Docker** running locally
- **Git**

### Development Setup

```bash
git clone https://github.com/schmitthub/clawker.git
cd clawker
make clawker
export PATH="$PWD/bin:$PATH"
```

> `go install ./cmd/clawker` and bare `go build ./cmd/clawker` are unsupported — they fail at compile time because the four Linux assets (`clawkerd`, `clawker-cp`, `ebpf-manager`, `coredns-clawker`) referenced by `//go:embed` are gitignored. `make clawker` produces them via the per-target chain (`clawkerd-binary`, `cp-binary`, `ebpf-binary`, `coredns-binary`) and then builds the host CLI. All four are plain `CGO_ENABLED=0` Go cross-compiles to `linux/$GOARCH`; the only Docker hop is the `bpf-bindings` extraction needed on macOS (where clang cannot emit BPF object files) — Linux contributors install the pinned BPF toolchain once with `sudo make bpf-deps` and everything builds natively. The `make release-embeds` / `make stage-embeds-{amd64,arm64}` targets are for the goreleaser pipeline; contributors do not invoke them for local development.

## Running Tests

Clawker has multiple test tiers. **All relevant tests must pass before submitting a PR.**

```bash
# Unit tests (no Docker required) — run these first
make test

# Integration tests (Docker required)
go test ./test/e2e/... -v -timeout 10m        # E2E integration
go test ./test/whail/... -v -timeout 5m       # Whail BuildKit integration

# All test suites
make test-all
```

### Golden File Tests

Some tests use golden files for output comparison. To update golden files after intentional changes:

```bash
GOLDEN_UPDATE=1 go test ./path/to/package/... -run TestName -v
```

## Code Style

### Key Rules

- **zerolog is for file logging only** — user-visible output uses `fmt.Fprintf` to IOStreams
- **Import boundaries are enforced**:
  - Only `internal/iostreams` imports `lipgloss`
  - Only `internal/tui` imports `bubbletea`/`bubbles`
  - Only `internal/term` imports `golang.org/x/term`
  - Only `pkg/whail` wraps the Docker SDK; only `internal/docker` imports `pkg/whail`
- **Cobra commands** use `PersistentPreRunE` (never `PersistentPreRun`)
- **Output conventions**: stdout for data, stderr for status/warnings/errors
- **Error handling**: Return typed errors to Main() — never print errors directly

### Command Pattern

Every CLI command follows the Factory/Options/runF pattern:

1. `NewCmd(f *cmdutil.Factory, runF func(*Options) error)` constructor
2. Options struct declares only what the command needs
3. Run function receives `*Options`, never `*Factory`

See `docs/architecture.mdx` for the full pattern with examples.

## Making Changes

### Branch Naming

Use descriptive branch names:

- `feat/description` — New features
- `fix/description` — Bug fixes
- `refactor/description` — Code improvements
- `docs/description` — Documentation changes

### What to Include in a PR

1. **Code changes** with tests
2. **Updated documentation** — if you change a package's public API, update its `CLAUDE.md` and relevant docs
3. **Passing tests** — run `make test` at minimum before submitting

### PR Process

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes with tests
4. Ensure `make test` passes
5. Open a PR against `main`
6. Describe what changed and why in the PR description

PRs are reviewed by the maintainer. Expect feedback within a few days. For larger changes, open an issue first to discuss the approach.

## Architecture

Before making significant changes, familiarize yourself with the codebase:

- **[docs/architecture.mdx](docs/architecture.mdx)** — System layers, package DAG, key abstractions
- **[docs/design.mdx](docs/design.mdx)** — Design philosophy, security model, core concepts
- **[docs/testing.md](docs/testing.md)** — Test strategy, patterns, and how to write tests
- **[docs/cli-reference/](docs/cli-reference/)** — Auto-generated CLI command docs

Package-specific docs live in `internal/*/CLAUDE.md` files.

## Issue Labels

| Label | Description |
|-------|-------------|
| `bug` | Bug reports |
| `enhancement` | Feature requests |
| `good first issue` | Beginner-friendly tasks |
| `known-issue` | Known bugs or limitations |
| `roadmap` | Planned features |

## Code of Conduct

Please read and follow our [Code of Conduct](CODE_OF_CONDUCT.md). Be kind, be constructive, be welcoming.

## License & Contributor Agreement

Clawker is dual-licensed: AGPL-3.0-or-later for everyone, with commercial
licenses available from the maintainer. So that contributions can ship under
both, this project uses a Contributor License Agreement: you keep copyright of
your work, your contribution is published under the AGPL-3.0-or-later, and you
grant the maintainer the right to also license it under commercial terms.

Read the full text in [CLA.md](CLA.md). On your first pull request, a CLA
assistant asks you to sign by commenting a short confirmation phrase; merging is
gated until you do.
