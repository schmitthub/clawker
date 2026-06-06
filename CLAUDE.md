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
- **Firewall is one optional subsystem CP manages.** Envoy + custom CoreDNS + eBPF egress enforcement. Toggled by `firewall.enable` in `settings.yaml` (NOT `clawker.yaml`). When disabled, CP/mTLS/registry/agent.Dialer/ListAgents continue to operate.

Do **NOT** gate non-firewall behavior on `firewall.enable`.

</critical_clarification>

<critical_clarification>

## CP crashing is a SECURITY incident, not an availability one

This is the single most important invariant in the codebase. Read it before adding any failure path to CP code.

**What happens when CP crashes (panic, log.Fatal, unrecovered goroutine):**

1. PID 1 exits. CP container goes down. `on-failure` restart policy retries 3×; if the bug is deterministic (most are), CP stays dead.
2. **eBPF programs stay attached to cgroups.** They're pinned under `/sys/fs/bpf` and survive the CP container's death. Agent containers' egress traffic continues to be filtered by whatever rule set was loaded at the moment CP died.
3. **The clean drain-to-zero path is skipped.** `firewall.Stack.Stop()` and `ebpfMgr.FlushAll()` only run on intentional shutdown via the orchestrator. A panic skips both. eBPF state is now frozen and unsupervised.
4. **Agent containers keep running.** They have no awareness that their supervisor died. They keep serving their workloads.
5. **The user has no idea.** They see agents running. They assume the firewall is enforcing — and it technically is, against the rules that happened to be loaded. They assume CP is observing — it isn't. They assume CP can dispatch containment — it can't.

**The result:**

- No new firewall rules can be applied (`clawker firewall add` writes to the rules file but Envoy/CoreDNS need CP to reload).
- No bypass can be expired (`clawker firewall bypass <duration>` schedules a CP-side timer; if CP died during a bypass, the bypass is now permanent until the user manually intervenes).
- No CP→clawkerd Session means no command dispatch, no observation of agent behavior, no containment commands available even if compromise is detected.
- Agents are vulnerable to prompt injection, exfiltration, and lateral-movement attempts that CP would otherwise observe and contain. The user's mental model ("CP has them covered") is silently false.

The stack trace from a CP panic lands on `os.Stderr` → `docker logs <cp>`. It is NOT in the rotating `ControlPlaneLogFile` operators are wired to grep. It is NOT surfaced by `clawker controlplane status` (which only knows up/down). The user has to know to dig into raw docker logs to find it.

**Hard rules for code on the CP boot/serve path** (`cmd/clawker-cp/`, `internal/controlplane/`, anything imported by them):

1. **No `panic()`. No `log.Fatal()`. No `os.Exit()`** outside the orchestrator's intentional shutdown sequence. Constructors return `(nil, error)` (see `agent.New`, `agent.NewExecutor`); main logs structurally and degrades. The only hard-exits permitted are: drain-to-zero clean exit (code 0), and the orchestrator's pre-`SetReady` startup-gate failures (code 1) where no agents are running yet so eBPF flush isn't load-bearing.
2. **Every long-lived goroutine recovers.** Heartbeats, watchers, event handlers, RPC handlers — wrap with `defer func() { if r := recover(); r != nil { log.Error().Interface("panic", r)... } }()`. The overseer stats heartbeat in `cmd/clawker-cp/main.go` is the canonical template. One bad event must not take down the daemon and silently strand eBPF.
3. **Subsystem failures degrade, never cascade.** A broken init Executor → `initExec = nil`; CP never dispatches `AgentReady`, clawkerd-as-PID-1 never spawns the user CMD, and the container exits non-zero on `docker stop`; the firewall, registry, AdminService, dialer all stay up. A broken dialer → `dialer = nil`; CP→clawkerd dispatch disabled; everything else stays up. The patterns in `cmd/clawker-cp/main.go` — `wireInitExecutor` (initExec; emits `event=agent_init_executor_unavailable`) and the `agent.New(...)` block that degrades on error to `event=agent_dialer_unavailable` — are the templates; copy either for any new subsystem.
4. **Every degraded path emits a structured log line.** `event=<subsystem>_unavailable` with component, error, downstream impact. Operator must be able to determine root cause AND blast radius from the structured log surface alone — they will not see panic stacks.
5. **Treat CP shutdown as a privileged operation.** If you find yourself thinking "this should never happen, just panic," stop. In CP that line of reasoning compromises the security boundary the user trusts to be intact. Return an error and let the orchestrator decide.

If you're tempted to write `panic()` in CP code, ask: "would this leave eBPF programs pinned with no supervisor?" If yes — you've just turned a logic bug into a silent firewall failure. Return an error instead.

</critical_clarification>

<critical_clarification>

## Asymmetric trust: dialer permissive, listener strict

- **clawkerd-side listener (server):** STRICT. `cmd/clawkerd/listener.go` enforces CP CN pin + Client-Auth EKU + CA chain at TLS layer.
- **CP-side dialer (client):** PERMISSIVE. `internal/controlplane/agent.Dialer` never aborts on cert/identity grounds. Outcomes emitted as typed fields on `SessionConnected` overseer events. Dial only fails on connectivity.

**Why permissive:** CP must reach clawkerd to issue containment commands even when certs are bad. Subscribers to `SessionConnected` enact policy; the dialer holds none.

**Trust attestation:** CLI mints agent cert + writes sqlite registry row at create time. Dialer cross-checks peer cert thumbprint against the row and emits result on the bus.

</critical_clarification>

## Repository Structure

Full directory tree with per-package purpose: `.claude/docs/REPO-STRUCTURE.md`. Key roots: `cmd/` binaries, `internal/` packages, `pkg/whail/` reusable Docker client, `test/{e2e,whail}/` Docker-required suites, `api/` protobuf.

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

**Top-level shortcuts**: `init`, `build`, `run`, `start`, `monitor *`, `generate`, `version`
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
agent: { env_file: [], from_env: [], env: {}, post_init: "", pre_run: "" }
workspace: { default_mode: "bind" }
security: { firewall: { add_domains: [], rules: [] }, docker_socket: false, git_credentials: { forward_https: true, forward_ssh: true, forward_gpg: true, copy_git_config: true } }
```

## Design Decisions

1. Firewall enabled, Docker socket disabled by default
2. `run`/`start` are aliases for `container run` (Docker CLI pattern)
3. Hierarchical naming: `clawker.project.agent`; labels (`dev.clawker.*`) authoritative for filtering
4. stdout for data/status/success/next-steps; stderr for warnings/errors only; `--format` for machine-readable output
5. Project registry replaces directory walking for resolution
6. Global-scope agents (no project) → 2-segment names (`clawker.agent`); the `dev.clawker.project` label is intentionally absent (not present as an empty string), matching the 2-segment name shape
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

All `@sha256:` pins must be multi-arch manifest lists (`application/vnd.oci.image.index.v1+json`). Verify with `docker buildx imagetools inspect`. Firewall stack binaries are built fresh from pinned BPF toolchain inputs — `BPF_APT_DEPS` in the Makefile pins clang/llvm/libbpf-dev/linux-libc-dev versions; CI runs `sudo make bpf-deps` on `ubuntu-latest`, while `Dockerfile.controlplane` provides the same path for macOS devs. Nothing generated is committed.

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
