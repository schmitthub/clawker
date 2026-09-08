---
name: dev-checks
description: Use when building clawker, running unit or integration tests, changing dependencies or pins, regenerating docs, mocks, or embeds, installing hooks, or completing work.
---

# dev-checks

Run commands from the repository root unless a command specifies another path.

## Commands

| Purpose | Command |
| --- | --- |
| Build the CLI with existing embeds | `go build -o bin/clawker ./cmd/clawker` |
| Build all required binary embeds | `make clawker` (see below; not routine) |
| Unit suite without Docker | `make test` |
| Focused unit example | `go test ./internal/storage/... -count=1` |
| Daemon and agent unit checks | `make test-clawkerd` |
| Race checks, uncached results, coverage | `make test-ci` |
| Lint with the repository config | `golangci-lint run --config .golangci.yml ./...` |
| Install Git hooks (once after clone) | `bash scripts/install-hooks.sh` |
| Run all hooks | `make pre-commit` |
| Generate CLI docs and config schemas | `go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas` |
| Check generated docs | `make docs-check` |
| Local Mintlify preview | `npx mintlify dev --docs-directory docs` |
| Check Serena references, if its CLI is installed | `serena memories check` |
| Whail golden regeneration | `GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeedRecordedScenarios -v` |

Host checks need the host Docker daemon or kernel capabilities:

| Purpose | Command |
| --- | --- |
| CLI integration tests | `make test-e2e` (`go test ./test/e2e/... -v -timeout 10m`) |
| Docker/BuildKit integration tests | `make test-whail` (`go test ./test/whail/... -v -timeout 5m`) |
| Unit plus both integration suites | `make test-all` |
| Privileged BPF tests on Linux | `make test-bpf` |

- When `CLAWKER_AGENT` is set, never run `go test ./...`: the e2e suite tears down the host CP. Use `make test` or explicit unit package paths. The same applies to targets that pass `./...` to the test runner: `test-coverage`, `clawker-test-coverage`, `clawker-test-short`. Report host checks as pending until results exist.
- Format changed Go files with `gofmt -w` and their paths.
- Generate mocks with `moq` through `//go:generate`; run `go generate ./...` from the owning package. Never edit generated mocks.
- `make pre-commit` can change module and license files. Review its diff.
- Do not bypass Git hooks with flags, environment variables, configuration overrides, or Git plumbing commands.

## `make clawker` — only when embeds are missing

`make clawker` builds the `//go:embed` binaries the prek go-test hook needs. It is slow and fills build caches — **never run it reflexively before a commit**. Check first; build only if a binary is missing:

```bash
ls clawkerd/embed/assets/clawkerd \
   controlplane/manager/assets/clawkercp \
   controlplane/manager/assets/ebpf-manager \
   controlplane/manager/assets/bpffs-delegate \
   controlplane/firewall/assets/coredns-clawker \
   internal/cmd/container/shared/assets/idmap-mount \
  || make clawker
```

(Paths are the Makefile's `CLAWKERD_BINARY`/`CP_BINARY`/`EBPF_BINARY`/`BPFFS_DELEGATE_BINARY`/`COREDNS_BINARY`/`IDMAP_MOUNT_BINARY` vars — check there if this list drifts.)

Embeds persist for the container's lifetime — they are only absent in a fresh container. Editing Go source does not invalidate them for hook purposes.

## Security: Version Pinning

All external dependencies pinned to exact versions with integrity verification. Never use `@latest` or floating tags.

| Context | Pinning requirement | Example |
|---------|-------------------|---------|
| Dockerfile base images | SHA256 digest | `FROM golang:1.26@sha256:abc...` |
| CI workflow actions | SHA commit hash | `uses: actions/checkout@a1b2c3d...` |
| Pre-commit hooks | SHA commit hash | `rev: 83d9cd68...  # frozen: v8.30.1` |
| Container images in code | SHA256 digest | `DefaultGoBuilderImage = "golang:...@sha256:..."` |
| Go tool installs | Exact version or SHA | `go install tool@v2.0.1` |

All `@sha256:` pins must be multi-arch manifest lists (`application/vnd.oci.image.index.v1+json`). Verify with `docker buildx imagetools inspect`. Firewall stack binaries are built fresh from pinned BPF toolchain inputs — `BPF_APT_DEPS` in the Makefile pins clang/llvm/libbpf-dev/linux-libc-dev versions; CI runs `sudo make bpf-deps` on its pinned Ubuntu runner (see `.github/workflows/`), while `Dockerfile.controlplane` provides the same path for macOS devs. Nothing generated is committed.

## Completion checks

Select checks from the changed behavior. Record the commands, results, and any checks that remain unavailable.

Code changes:

1. Add a failing regression test before a bug fix or new behavior. Use the responsible package and observable result. Put integration tests in `test/*/`. Test helpers: `.agents/skills/writing-tests/SKILL.md`.
2. Format changed Go files. Regenerate affected moq mocks from their owner package.
3. Run the affected unit packages with `go test`, explicit package paths, and `-count=1`.
4. Run `make test` and `golangci-lint run --config .golangci.yml ./...`.
5. For Docker behavior, complete the relevant host integration checks. For BPF behavior, include the privileged host checks. Unit success does not prove live container behavior.
6. Before a commit, check embed availability, then run `make pre-commit`. Review any files changed by hooks.

Documentation and records:

- Update the README and the relevant package `AGENTS.md` when their content changes. Keep each `CLAUDE.md` link relative.
- Update the Serena `design` and `architecture` memories if the design changes. Update Mintlify pages in `docs/` for user-facing behavior; see [docs/AGENTS.md](../../../docs/AGENTS.md). Generated CLI reference pages are never edited by hand.
- If the command tree or config schema changes, run the gen-docs command, then `make docs-check`.
- After bug fixes or features, check `clawker-plugin/skills/clawker-support/reference/known-issues.md`. Fixes there are committed in the clawker-plugin submodule, then the submodule pointer is bumped here.
- Keep current rules in module memories and retained plan, research, and history records in their own topics; follow the Serena `memory_maintenance` memory.
- For agent instruction and memory changes, check files and formats only. Do not run application tests, install or run Claude Code, or make model API calls. The commit hook and the PR lint workflow run the agent layout check; do not run it by hand. Run `serena memories check` when available; otherwise check the local graph and report that limit.
- For all changes, run `git diff --check` and inspect the final diff.
