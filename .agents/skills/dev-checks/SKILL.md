---
name: dev-checks
description: Use when building clawker, running unit or integration tests, changing dependencies or pins, regenerating docs or embeds, installing hooks, or completing work.
---

# dev-checks

Run commands from the repository root unless a command specifies another path.

Write tests before production code. Put integration tests in `test/*/`.
Use the [writing-tests skill](../writing-tests/SKILL.md) for fixtures and test helpers.
Generate mocks with `moq` through `//go:generate`; never edit generated mocks.
Run `go generate ./...` from the owning `internal/<package>` directory.

When `CLAWKER_AGENT` is set, never run `go test ./...`: the e2e suite tears
down the host CP. Use targeted packages or `make test`. Run Docker integration
suites only when the session permits host integration tests.

## Build Commands

```bash
go build -o bin/clawker ./cmd/clawker                        # Build CLI
make test                                                     # Unit tests (no Docker)
make test-all                                                 # All suites (unit + e2e + whail)
go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas    # Regenerate CLI docs for Mintlify + config JSON schemas
npx mintlify dev --docs-directory docs                        # Local Mintlify preview

# Golden file tests
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeedRecordedScenarios -v

# Docker-required tests
go test ./test/e2e/... -v -timeout 10m
go test ./test/whail/... -v -timeout 5m

# Git hooks (prek)
bash scripts/install-hooks.sh          # Install (once after clone)
make pre-commit                        # Run all hooks (prek run --all-files)
```

### `make clawker` — only when embeds are missing

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

### Completion Gate

After bug fixes or feature changes:
- Check if fix addresses an issue in `clawker-plugin/skills/clawker-support/reference/known-issues.md` (git submodule — fixes there are committed in the clawker-plugin repo, then the submodule pointer is bumped here)
- Update relevant Mintlify docs in `docs/` if user-facing behavior changed

### Mintlify (docs.clawker.dev)

Generate CLI reference pages; do not edit generated pages by hand: `go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas`
Local preview: `npx mintlify dev --docs-directory docs`
See [docs/AGENTS.md](../../../docs/AGENTS.md) for conventions.

Do not bypass Git hooks with flags, environment variables, configuration
overrides, or Git plumbing commands.

For agent instruction and memory changes, check files and formats. Do not
run application tests, install or run Claude Code, or make model API calls
for these checks. The commit hook and the PR lint workflow run the agent
layout check; do not run it by hand.

```bash
bash scripts/check-agents-freshness.sh --no-color
git diff --check
```
