# Clawker

<critical_instructions>

## MANTRA

Alpha project — architecture and design change often. Legacy code that no longer fits gets encountered regularly.

* Don't write hacky code to get a task done. Think about the big picture.
* When gaps or bad patterns are found, pivot and address them before continuing.
* Consider impact on architecture, design, testing, documentation, user and developer experience.
* Ask: "will this make future work easier or harder? does this decision serve the project or just my task?" If harder, rethink.
* If a package is missing a test subpackage, interface, mock, or fake, add it to fit standard patterns so every caller benefits.

Prioritize fixing technical debt and improving architecture over completing the immediate task.

### Workflow Requirements

**Planning**: Adhere to `.claude/docs/DESIGN.md` and `.claude/docs/ARCHITECTURE.md`. Update those docs if changes are needed.
**Testing**: TDD — write tests before code. All tests must pass. Add fixtures, golden files, interfaces, mocks, fakes, and test helpers as needed. Integration tests go in `test/*/`.
**Documentation**: Update README.md, relevant CLAUDE.md files, and memories after completing changes.

</critical_instructions>

<critical_clarification>

## CP ≠ firewall (common LLM confusion)

- **CP is unconditional infrastructure.** Auth (Hydra/Kratos/Oathkeeper), AdminService gRPC on `AdminPort`, AgentService gRPC on `AgentPort`, agent registry, mTLS, OAuth2 — all running whenever any clawker container exists. CP boots via `cpboot.EnsureRunning`. No "disable CP" flag. CP owns clawker-net.
- **Firewall is one optional subsystem CP manages.** Envoy + custom CoreDNS + eBPF egress enforcement. Toggled by `firewall.enable` in `settings.yaml` (NOT `clawker.yaml`). When disabled, CP/mTLS/registry/agentdial/ListAgents continue to operate.

Do **NOT** gate non-firewall behavior on `firewall.enable`.

</critical_clarification>

<critical_clarification>

## Asymmetric trust: dialer permissive, listener strict

- **clawkerd-side listener (server):** STRICT. `cmd/clawkerd/listener.go` enforces CP CN pin + Client-Auth EKU + CA chain at TLS layer.
- **CP-side dialer (client):** PERMISSIVE. `internal/controlplane/agentdial.Dialer` never aborts on cert/identity grounds. Outcomes emitted as typed `Provenance` fields on `SessionConnected` overseer events. Dial only fails on connectivity.

**Why permissive:** CP must reach clawkerd to issue containment commands even when certs are bad. Subscribers to `SessionConnected` enact policy; the dialer holds none.

**Trust attestation:** CLI mints agent cert + writes sqlite registry row at create time. Dialer cross-checks peer cert thumbprint against the row and emits result on the bus.

</critical_clarification>

## Repository Structure

```
├── api/
│   ├── admin/v1/              # AdminService protobuf (CLI → CP gRPC)
│   └── agent/v1/              # AgentService protobuf (Register RPC for clawkerd→CP identity binding)
├── cmd/
│   ├── clawker/               # Main CLI binary
│   ├── clawker-cp/            # Control plane daemon (PID 1 in CP container)
│   ├── clawker-generate/      # Code generation helper
│   ├── clawkerd/              # Per-container agent daemon (Linux)
│   ├── coredns-clawker/       # Custom CoreDNS with dnsbpf plugin (Linux)
│   └── gen-docs/              # CLI doc generator
├── internal/
│   ├── auth/                  # CLI-side auth material + CP dial helpers
│   ├── build/                 # Build-time metadata (leaf, stdlib only)
│   ├── bundler/               # Dockerfile generation, content hashing, semver, npm registry
│   ├── clawker/               # Main application lifecycle
│   ├── clawkerd/              # Embedded clawkerd binary (go:embed)
│   ├── cmd/                   # Cobra commands
│   │   ├── factory/           # Factory constructor
│   │   ├── settings/          # Settings commands
│   │   ├── skill/             # Skill plugin management
│   │   └── project/edit/      # Project edit subcommand
│   ├── cmdutil/               # Factory struct, error types, arg validators
│   ├── config/                # Store[T] config engine (see internal/config/CLAUDE.md)
│   │   └── storeui/           # Domain adapters for storeui
│   ├── consts/                # Cross-package constants
│   ├── containerfs/           # Host Claude config preparation
│   ├── controlplane/          # CP daemon: Ory auth, AdminService, agent watcher
│   │   ├── agent/             # AgentService identity interceptor
│   │   ├── agentdial/         # CP→clawkerd dialer (permissive trust)
│   │   ├── agentregistry/     # SQLite identity store
│   │   ├── cpboot/            # Host-side CP lifecycle (EnsureRunning/Stop)
│   │   ├── firewall/          # Firewall: Handler (13 RPCs), Stack, Envoy+CoreDNS, eBPF
│   │   │   └── ebpf/          # eBPF loader + Manager
│   │   ├── overseer/          # Typed event bus + worldview state
│   │   ├── dockerevents/      # Docker events feeder + typed envelope
│   │   └── mocks/
│   ├── dnsbpf/                # CoreDNS plugin for BPF dns_cache
│   ├── docker/                # Docker middleware (wraps pkg/whail + bundler)
│   ├── docs/                  # CLI doc generation
│   ├── git/                   # Git operations, worktree management (leaf)
│   ├── hostproxy/             # Host proxy for container-to-host communication
│   ├── iostreams/             # I/O streams, colors, styles, spinners, layout
│   ├── keyring/               # Credential storage
│   ├── logger/                # Struct-based zerolog; Factory noun
│   ├── monitor/               # Monitoring stack templates
│   ├── project/               # Project registration
│   ├── prompter/              # Interactive prompts
│   ├── signals/               # OS signal utilities (leaf)
│   ├── socketbridge/          # SSH/GPG agent forwarding via muxrpc
│   ├── storage/               # Multi-file YAML store
│   ├── storeui/               # Generic TUI for Store[T] editing
│   ├── term/                  # Terminal capabilities (sole x/term gateway)
│   ├── testenv/               # Unified test environment (test-only)
│   ├── text/                  # Pure text utilities (leaf)
│   ├── tui/                   # BubbleTea TUI layer
│   ├── update/                # Background update checker
│   └── workspace/             # Bind vs Snapshot strategies
├── pkg/whail/                 # Reusable Docker engine with label-based isolation
├── test/
│   ├── e2e/                   # E2E integration tests
│   └── whail/                 # Whail BuildKit integration tests
├── scripts/                   # install.sh, install-hooks.sh, check-claude-freshness.sh, etc.
└── templates/                 # clawker.yaml scaffolding
```

## Build Commands

```bash
go build -o bin/clawker ./cmd/clawker                        # Build CLI
make test                                                     # Unit tests (no Docker)
make test-all                                                 # All suites (unit + e2e + whail)
go run ./cmd/gen-docs --doc-path docs --markdown --website    # Regenerate CLI docs for Mintlify
npx mintlify dev --docs-directory docs                        # Local Mintlify preview

# Golden file tests
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeedRecordedScenarios -v

# Docker-required tests
go test ./test/e2e/... -v -timeout 10m
go test ./test/whail/... -v -timeout 5m

# Pre-commit hooks
bash scripts/install-hooks.sh          # Install (once after clone)
make pre-commit                        # Run all hooks
```

## Key Concepts

See `.claude/docs/KEY-CONCEPTS.md` for the full type/abstraction index. Package-specific `internal/*/CLAUDE.md` files are the source of truth for API surface.

## CLI Commands

See `docs/cli-reference/` for auto-generated command reference.

**Top-level shortcuts**: `init`, `build`, `run`, `start`, `monitor *`, `generate`, `loop`, `version`
**Management**: `auth *`, `container *`, `volume *`, `network *`, `image *`, `project *`, `worktree *`, `firewall *`, `controlplane *`, `settings *`, `skill *`

## Configuration

> Always use `Config` interface accessors — never hardcode filenames or env var names. See `internal/config/CLAUDE.md`.

### Project Config (`clawker.yaml`)

```yaml
build:
  image: "buildpack-deps:bookworm-scm"
  packages: ["git", "ripgrep"]
  instructions: { env: {}, copy: [], root_run: [], user_run: [] }
  inject: { after_from: [], after_packages: [] }
agent: { env_file: [], from_env: [], env: {}, post_init: "" }
workspace: { default_mode: "bind" }
security: { firewall: { add_domains: [], rules: [] }, docker_socket: false, git_credentials: { forward_https: true, forward_ssh: true, forward_gpg: true, copy_git_config: true } }
loop: { max_loops: 50, stagnation_threshold: 3, timeout_minutes: 15, skip_permissions: false, hooks_file: "", append_system_prompt: "" }
```

## Design Decisions

1. Firewall enabled, Docker socket disabled by default
2. `run`/`start` are aliases for `container run` (Docker CLI pattern)
3. Hierarchical naming: `clawker.project.agent`; labels (`dev.clawker.*`) authoritative for filtering
4. stdout for data/status/success/next-steps; stderr for warnings/errors only; `--format` for machine-readable output
5. Project registry replaces directory walking for resolution
6. Empty project → 2-segment names (`clawker.agent`), labels omit `dev.clawker.project`
7. Factory is a pure struct with closure fields; constructor in `internal/cmd/factory/`. Commands use `NewCmd(f, runF)` pattern
8. Factory noun principle: fields return nouns, not verbs (`f.HostProxy().EnsureRunning()` not `f.EnsureHostProxy()`)
9. Package boundary: path resolution + config I/O → `internal/config`; project identity/CRUD → `internal/project`

## Mock Generation

Mocks generated by [moq](https://github.com/matryer/moq) via `//go:generate`. Never hand-edit. Regenerate: `cd internal/<package> && go generate ./...`

## Important Gotchas

* `os.Exit()` does NOT run deferred functions — restore terminal state explicitly
* Raw terminal mode: Ctrl+C goes to container, not as SIGINT
* Don't wait for stdin goroutine on container exit (may block on Read)
* Docker hijacked connections need cleanup of both read and write sides
* Terminal visual state must be reset separately from termios mode — `term.Restore()` sends escape sequences before restoring raw/cooked mode
* Docker Desktop SDK `HostConfig.Mounts` behaves differently from `Binds` for Unix sockets on macOS
* `.clawkerlocal/` may exist during local development — check before defaults (see: `make localenv`)

## Context Management

**NEVER** store `context.Context` in struct fields. Pass as first parameter. Use `context.Background()` for cleanup in deferred functions.

## Security: Version Pinning

All external dependencies pinned to exact versions with integrity verification. Never use `@latest` or floating tags.

| Context | Pinning requirement | Example |
|---------|-------------------|---------|
| Dockerfile base images | SHA256 digest | `FROM golang:1.25@sha256:abc...` |
| CI workflow actions | SHA commit hash | `uses: actions/checkout@a1b2c3d...` |
| Pre-commit hooks | SHA commit hash | `rev: 83d9cd68...  # frozen: v8.30.1` |
| Container images in code | SHA256 digest | `DefaultGoBuilderImage = "golang:...@sha256:..."` |
| Go tool installs | Exact version or SHA | `go install tool@v2.0.1` |

All `@sha256:` pins must be multi-arch manifest lists (`application/vnd.oci.image.index.v1+json`). Verify with `docker buildx imagetools inspect`. Firewall stack binaries built fresh via pinned multi-stage Docker builds — nothing generated is committed. See `internal/controlplane/firewall/ebpf/REPRODUCIBILITY.md`.

## Testing

All tests must pass before any change is complete. See `.claude/rules/testing.md` for conventions.

> **CRITICAL — IF RUNNING IN A CLAWKER CONTAINER (`$CLAWKER_AGENT` set):** Do NOT run `go test ./...`. The e2e suite tears down the host CP. Use targeted tests or `make test`.

## Documentation

* `.claude/rules/` — Auto-loaded guidelines (code style, testing, package rules)
* `.claude/docs/` — On-demand reference (architecture, design, key concepts)
* `internal/*/CLAUDE.md` — Package-specific API references (lazy-loaded)

### Completion Gate

After bug fixes or feature changes:
- Check if fix addresses an issue in `claude-plugin/clawker-support/skills/clawker-support/reference/known-issues.md`
- Update relevant Mintlify docs in `docs/` if user-facing behavior changed

### Mintlify (docs.clawker.dev)

Regenerate CLI reference: `go run ./cmd/gen-docs --doc-path docs --markdown --website`
Local preview: `npx mintlify dev --docs-directory docs`
See `.claude/rules/mintlify-docs.md` for conventions.
