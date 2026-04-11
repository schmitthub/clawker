# BPF Bytecode Reproducibility

Clawker's BPF programs (`internal/ebpf/bpf/clawker.c`, `common.h`) are
compiled to `internal/ebpf/clawker_*_bpfel.{go,o}` and checked into the
repository. Those `.o` files are embedded into the `clawker` binary via
`go:embed`, so the committed bytecode is what runs in every user's kernel.

This document is the provenance chain for that bytecode: every input needed
to reproduce the committed `.o` files byte-for-byte, how they're pinned, and
how to update the pins safely.

## Why this matters

Committing a binary blob without a reproducible recipe is trust-on-first-use
— a reviewer has no way to verify the committed `.o` faithfully came from
the committed `.c`. Anyone with write access could swap in a tampered `.o`
and the only signal would be a binary diff that's impossible to audit by
eye.

The fix is a fully pinned build environment plus a CI job that regenerates
the bytecode on every PR and fails on any drift. Under that model the
committed `.o` isn't "trust me" — it's "this exact output, reproducible from
these exact pinned inputs." Any PR that modifies the committed bytecode
without also modifying the pinned sources (or vice versa) fails CI.

The `clawker` binary itself is separately signed/attested at release time
(SLSA attestation via the release workflow), which transitively covers the
embedded BPF bytecode.

## What's pinned, and where

| Input | Pin location | What it anchors |
|---|---|---|
| Base OS image | `Dockerfile.bpf-builder` — `debian:bookworm-slim@sha256:...` | `clang`, `libbpf-dev`, `linux-libc-dev` apt archive contents |
| `clang` version | `Dockerfile.bpf-builder` — `CLANG_VERSION` build arg | BPF codegen output |
| `libbpf-dev` version | `Dockerfile.bpf-builder` — `LIBBPF_DEV_VERSION` build arg | `bpf/bpf_helpers.h`, `bpf/bpf_endian.h` |
| `linux-libc-dev` version | `Dockerfile.bpf-builder` — `LINUX_LIBC_DEV_VERSION` build arg | `linux/bpf.h`, `linux/types.h`, `asm/types.h` — the kernel UAPI types clawker consumes |
| Go toolchain | `Dockerfile.bpf-builder` — `golang:...@sha256:...` `COPY --from=` | Go wrapper generation by `bpf2go` |
| `bpf2go` | `internal/ebpf/gen.go` — `go run ...@v0.21.0` | Go code generation shape, loader compatibility |
| BPF C source | Committed in `internal/ebpf/bpf/` | The program logic itself |
| Generated artifacts | Committed in `internal/ebpf/clawker_*_bpfel.{go,o}` | The authoritative outputs — what `go build` embeds |

Note: clawker does **not** use CO-RE (`BPF_CORE_READ`, preserve-access-index).
Every field access in `clawker.c` targets stable kernel UAPI types
(`struct bpf_sock_addr`, `struct bpf_sock`). That's why the pin chain
terminates at `linux-libc-dev` (UAPI headers) rather than a committed
`vmlinux.h` BTF dump.

## Reproduction commands

Regenerate the committed bytecode (writes into the working tree):

```bash
make bpf-regenerate
```

Verify the committed bytecode still matches a fresh regeneration in the
pinned environment (fails on any drift — the CI gate):

```bash
make bpf-verify
```

Both targets build `Dockerfile.bpf-builder` if needed and run `go generate`
inside the resulting container. The repository root is mounted read-write
at `/src`.

## When the committed bytecode changes

Any PR that modifies `internal/ebpf/bpf/clawker.c` or `internal/ebpf/bpf/common.h`
**must also** regenerate and re-commit `internal/ebpf/clawker_*_bpfel.{go,o}`:

```bash
# edit internal/ebpf/bpf/clawker.c or common.h
make bpf-regenerate          # regenerate committed bytecode
git add internal/ebpf/clawker_*_bpfel.{go,o}
make bpf-verify              # sanity check before commit
git commit
```

CI runs `make bpf-verify` on every PR. A mismatch fails CI with a clear
"run `make bpf-regenerate` and commit the result" message.

## Updating pinned inputs

Updating any pin (base image, apt version, Go toolchain, `bpf2go`) shifts
the BPF bytecode output, so all pinned inputs must move together in a
single coordinated PR. Procedure:

### 1. Base image digest

Refresh to the current `debian:bookworm-slim`:

```bash
docker pull debian:bookworm-slim
docker inspect --format '{{index .RepoDigests 0}}' debian:bookworm-slim
```

Paste the full `debian@sha256:...` reference into `Dockerfile.bpf-builder`.

### 2. apt package versions

With the new digest in place, resolve the versions shipped in that image:

```bash
docker run --rm debian:bookworm-slim@sha256:<new-digest> bash -c '
    apt-get update >/dev/null 2>&1 &&
    apt-cache policy clang libbpf-dev linux-libc-dev | grep -E "^ |Installed|Candidate"
'
```

Paste each `Candidate:` version into the matching `ARG` in
`Dockerfile.bpf-builder`.

### 3. Go toolchain digest

Match the Go version in `go.mod`'s `go` directive:

```bash
docker pull golang:$(awk '/^go /{print $2}' go.mod)-alpine
docker inspect --format '{{index .RepoDigests 0}}' golang:$(awk '/^go /{print $2}' go.mod)-alpine
```

Paste the resulting `golang@sha256:...` reference into the `COPY --from=`
line of `Dockerfile.bpf-builder`.

### 4. bpf2go version

If cilium/ebpf is bumped in `go.mod`, the `@vX.Y.Z` in `internal/ebpf/gen.go`'s
`//go:generate` line must bump in lockstep. Check
`https://github.com/cilium/ebpf/releases` for the version that matches the
library version in `go.mod`.

### 5. Regenerate + verify

```bash
make bpf-regenerate
make bpf-verify            # must pass
go test ./internal/ebpf/...
# plus a local e2e run: go test ./test/e2e/... -run TestFirewall -v
```

Commit the pin updates **and** the regenerated `clawker_*_bpfel.{go,o}`
files in one commit.

## Failure modes and what they mean

| Failure | Likely cause | Fix |
|---|---|---|
| `bpf-builder-image` errors on `PIN_ME_BEFORE_MERGE` | Placeholder digest was never filled in | Follow step 1 above before merging |
| `docker build` fails at apt install | Pinned package version rotated out of the archive | Refresh digest + apt versions together (steps 1–2) |
| `bpf-verify` reports drift | Committed `.c` edited without regenerating, **or** committed `.o` tampered with | Run `make bpf-regenerate`, inspect the diff, re-commit |
| Verifier rejects bytecode at runtime | Pinned `clang`/`libbpf-dev` now emits relocations the target kernel can't handle | Roll the pinned versions forward in lockstep with the runtime kernel support matrix |

## Out of scope for this document

- **Release binary signing** — handled by `.github/workflows/release.yml` via
  SLSA provenance generation. The BPF `.o` files are covered transitively
  because they're `go:embed`'d into the Go binary.
- **Multi-kernel CO-RE support** — clawker targets modern cgroup v2 kernels
  where the UAPI types used are stable. If future clawker features need to
  read in-kernel types (`struct task_struct`, etc.), the strategy changes —
  at that point we'd reintroduce `vmlinux.h`, but pinned via a repeatable
  source (BTFHub or libbpf-bootstrap minimal), not committed blind.
