# Testing

- Co-located unit tests use package fakes/mocks and do not need Docker.
- `test/e2e/` runs CLI integration tests. `test/whail/` runs Docker/BuildKit integration tests.
- `test/adversarial/` is an operator-directed live test environment; do not treat its scripts as the unit suite.
- `controlplane/firewall/ebpf/bpftest/` needs privileged host execution for BPF program tests. A skipped unprivileged run does not prove BPF behavior.
- In a clawker container, use `make test` or explicit unit package paths. Do not run `go test ./...`; E2E setup can remove the host CP.

## Test helpers

| Need | Existing helper |
| --- | --- |
| Isolated config/data/state paths | `internal/testenv.New` |
| Config defaults without file discovery | `configmocks.NewBlankConfig` |
| Exact YAML values without defaults | `configmocks.NewFromString` |
| Config persistence or environment overrides | `configmocks.NewIsolatedTestConfig` |
| Docker command execution with a fake | `internal/docker/mocks.NewFakeClient` |
| Whail transport and recorded scenarios | `pkg/whail/whailtest` |
| Captured terminal streams | `iostreams.Test` |
| Logging without output | `logger.Nop` |

- The two lightweight config helpers expose stores without write destinations. Use the isolated real config for `Set`+`Write` tests.
- Prefer the smallest existing helper that proves the requested behavior. Add shared test infrastructure at the package that owns the dependency.
- Generate mocks with the package's moq directive. Do not hand-edit them.
- Command tests cover option parsing through `runF` and execution through injected dependencies.
- Add a failing regression test before behavior changes. Do not add assertions that only repeat implementation details.
- Golden files are package-specific. Update them only for an intended output change and review the diff.
- Check actual container interactions for mounts, certificates, startup, and networking. File modes alone do not prove access by another process/container.
- Rules: root `AGENTS.md`, Testing rules section; helper reference: `.agents/skills/writing-tests/SKILL.md`; integration contracts: `test/AGENTS.md`.
- For exact commands and completion gates: `.agents/skills/dev-checks/SKILL.md`.
