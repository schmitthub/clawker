# BPF Bytecode Reproducibility

Clawker's firewall stack contains two Linux binaries that are `go:embed`'d
into the clawker CLI and built into runtime Docker images at clawker-run time:

- **`ebpf-manager`** — compiled from `./internal/ebpf/cmd`; has the BPF
  bytecode (`clawker.o`) from `internal/ebpf/bpf/clawker.c` + `common.h`
  baked in via `bpf2go` + `go:embed`.
- **`coredns-clawker`** — compiled from `./cmd/coredns-clawker`; has the
  `dnsbpf` plugin from `internal/dnsbpf` baked in.

Neither binary is committed to the repo. Neither are their intermediate
artifacts (`clawker_*_bpfel.{go,o}`). Every `make` invocation that depends
on them regenerates them inside a pinned Docker build environment. This
document is the provenance chain for that regeneration.

## Why this works

Committing generated binaries is trust-on-first-use — a reviewer has no
realistic way to verify a 20 MB binary blob actually came from the committed
sources. The fix is not a gate that checks "did the committed `.o` match a
fresh regeneration?" (which is policing-after-the-fact). The fix is to make
regeneration the *only* way the binary can come into existence, and pin
every input to that regeneration. Under that model the committed sources
plus the pinned recipe *are* the binary — there is no separate artifact to
audit.

The clawker CLI binary itself is then signed/attested at release time
(SLSA attestation via the release workflow, tracked as a follow-up in
`.serena/memories/outstanding-features.md`), which transitively covers both
embedded Linux binaries and the BPF bytecode inside them.

## What's pinned, and where

| Input | Pin location | What it anchors |
|---|---|---|
| Base OS image (BPF stage) | `Dockerfile.firewall` — `debian:bookworm-slim@sha256:...` | The apt archive contents the BPF compile resolves against |
| `clang` version | `Dockerfile.firewall` — `CLANG_VERSION` build arg | BPF codegen output |
| `llvm` version | `Dockerfile.firewall` — `LLVM_VERSION` build arg | `llvm-strip` used by `bpf2go` to strip debug symbols from the emitted `.o` |
| `libbpf-dev` version | `Dockerfile.firewall` — `LIBBPF_DEV_VERSION` build arg | `bpf/bpf_helpers.h`, `bpf/bpf_endian.h` |
| `linux-libc-dev` version | `Dockerfile.firewall` — `LINUX_LIBC_DEV_VERSION` build arg | `linux/bpf.h`, `linux/types.h`, `asm/types.h` — the kernel UAPI types clawker consumes (clawker is non-CO-RE; UAPI is sufficient) |
| Go toolchain | `Dockerfile.firewall` — `golang:1.25.9-alpine@sha256:...` used in three places (`COPY --from=` in the `bpf-builder` stage for `go generate`, and `FROM` lines of `ebpf-manager-builder` and `coredns-builder` stages); all three must be the same digest, and the digest MUST be a multi-arch manifest list (OCI image index) so cross-platform builds can select the right per-arch manifest | All Go compilation including the `go:embed` of BPF bytecode |
| `bpf2go` | `internal/ebpf/gen.go` — `go run github.com/cilium/ebpf/cmd/bpf2go@v0.21.0` | BPF → Go code generation shape, loader compatibility with the `cilium/ebpf` runtime |
| BPF C source | `internal/ebpf/bpf/clawker.c`, `common.h` | The program logic itself |
| ebpf-manager Go source | `internal/ebpf/manager.go`, `types.go`, `cmd/main.go`, `gen.go` | The host-side BPF loader and RPC surface |
| coredns-clawker Go source | `cmd/coredns-clawker/main.go`, `internal/dnsbpf/*.go` | The custom CoreDNS entry point + dnsbpf plugin |

Note: clawker does **not** use CO-RE (`BPF_CORE_READ`, preserve-access-index).
Every field access in `clawker.c` targets stable kernel UAPI
(`struct bpf_sock_addr`, `struct bpf_sock`), so the pin chain terminates at
`linux-libc-dev` instead of a committed `vmlinux.h` BTF dump.

## How `make` drives the build

```
make clawker
  └── make clawker-build
        ├── bpf-bindings  → docker buildx build -f Dockerfile.firewall
        │                     --target=bpf-bindings-extract
        │                     --output=type=local,dest=internal/ebpf/...
        │   produces: internal/ebpf/clawker_{x86,arm64}_bpfel.{go,o}
        │   (gitignored; exists so host-side go/gopls can compile
        │    internal/ebpf/manager.go, which references bpf2go types)
        │
        ├── ebpf-binary   → docker buildx build -f Dockerfile.firewall
        │                     --target=ebpf-manager-extract
        │                     --output=type=local,dest=internal/firewall/assets
        │   produces: internal/firewall/assets/ebpf-manager
        │
        ├── coredns-binary → docker buildx build -f Dockerfile.firewall
        │                     --target=coredns-extract
        │                     --output=type=local,dest=internal/firewall/assets
        │   produces: internal/firewall/assets/coredns-clawker
        │
        └── go build ./cmd/clawker
              produces: bin/clawker
              (go:embed's both Linux binaries into the CLI)
```

All three buildx invocations hit the same Dockerfile and share BuildKit
layer cache — the expensive `bpf-builder` stage runs once per source change,
then serves its outputs to all three downstream extract stages.

Make's dep graph ensures the pinned Docker builds only run when their source
inputs change. First build on a clean clone pulls the base images (~2 min).
Subsequent builds hit BuildKit layer cache and are seconds.

Targets that transitively trigger `ebpf-binary` + `coredns-binary`: every
`make clawker*` target, every `make test*` target, `make docs*`, `make lint*`,
`make staticcheck*`. There is no supported code path that builds clawker
without the pinned Docker stage in the chain.

## Regenerating by hand

Rarely needed — Make normally drives this — but for debugging:

```bash
# Force-rebuild the ebpf-manager binary from scratch:
rm -f internal/firewall/assets/ebpf-manager
make ebpf-binary

# Same for coredns-clawker:
rm -f internal/firewall/assets/coredns-clawker
make coredns-binary
```

For a full clean rebuild with BuildKit cache bypass:

```bash
docker buildx prune -f                      # clear buildx cache
rm -f internal/firewall/assets/*
make clawker
```

## Updating pinned inputs

Updating any pin shifts the produced binaries, so all pinned inputs must
move together in a single coordinated PR.

### 1. Base image digest

Refresh to the current `debian:bookworm-slim`:

```bash
docker pull debian:bookworm-slim
docker inspect --format '{{index .RepoDigests 0}}' debian:bookworm-slim
```

Paste the full `debian@sha256:...` reference into the `FROM` line of the
`bpf-builder` stage in `Dockerfile.firewall`.

### 2. apt package versions

With the new digest in place, resolve the versions shipped in that image:

```bash
docker run --rm debian@sha256:<new-digest> bash -c '
    apt-get update >/dev/null 2>&1 &&
    apt-cache policy clang llvm libbpf-dev linux-libc-dev | grep -E "^  Candidate:"
'
```

Paste each `Candidate:` version into the matching `ARG` in
`Dockerfile.firewall` (`CLANG_VERSION`, `LLVM_VERSION`, `LIBBPF_DEV_VERSION`,
`LINUX_LIBC_DEV_VERSION`).

### 3. Go toolchain digest

Match the Go version in `go.mod`'s `go` directive:

```bash
GO_VER=$(awk '/^go /{print $2}' go.mod)
docker pull golang:${GO_VER}-alpine
docker inspect --format '{{index .RepoDigests 0}}' golang:${GO_VER}-alpine
```

The resulting `golang@sha256:...` reference appears **three times** in
`Dockerfile.firewall` and all three must match:

1. `COPY --from=golang:1.25.9-alpine@sha256:...` in the `bpf-builder` stage
   (used to copy the Go toolchain in for `go generate`)
2. `FROM golang:1.25.9-alpine@sha256:... AS ebpf-manager-builder`
3. `FROM golang:1.25.9-alpine@sha256:... AS coredns-builder`

**Multi-arch constraint**: the digest MUST be a manifest list (OCI image
index), not a per-platform image digest. Verify with:

```bash
docker buildx imagetools inspect golang:<version>-alpine@sha256:<digest>
```

`MediaType` must be `application/vnd.oci.image.index.v1+json`. Per-platform
digests (like the output of `docker manifest inspect --verbose` showing a
single architecture) break cross-platform builds because buildx can't
pick a matching per-arch manifest from them. Same constraint applies to
the `debian:bookworm-slim` and `alpine:3.21` pins below and to the
`DefaultGoBuilderImage` constant in `internal/bundler/dockerfile.go` —
every image pin in the project should be a manifest list.

### 4. `bpf2go` version

If `cilium/ebpf` is bumped in `go.mod`, the `@vX.Y.Z` in `internal/ebpf/gen.go`'s
`//go:generate` line must bump in lockstep. Check
`https://github.com/cilium/ebpf/releases` for the version that matches the
library version in `go.mod`.

### 5. Rebuild + verify

```bash
rm -f internal/firewall/assets/*
make clawker
go test ./internal/ebpf/...
# plus a local e2e run:
go test ./test/e2e/... -run TestFirewall -v
```

Commit the pin updates together. No generated artifacts are committed — only
the pinned recipe files.

## Failure modes and what they mean

| Failure | Likely cause | Fix |
|---|---|---|
| `docker build` fails at `apt install` with "Unable to locate package" | Pinned apt version rotated out of the archive | Refresh digest + apt versions together (steps 1–2 above) |
| `docker buildx build` not found | buildx not installed | Docker Desktop ships buildx; on Linux install `docker-buildx-plugin` |
| Long first-build on clean checkout | Expected — pulling base images and running the BPF + Go compilation in the pinned containers is a one-time cost per cache state. Subsequent builds hit BuildKit cache and are fast | N/A |
| Compile error inside the bpf-builder stage | Usually a `clawker.c` / `common.h` syntax issue, or a header that moved. Check the build log — clang emits line-accurate errors | Fix the C source; `make` will re-run the pinned build |
| IDE red squiggles in `internal/ebpf/manager.go` referencing `clawkerObjects`, `clawkerPrograms`, etc. on a fresh clone | The bpf2go-generated Go wrappers (`clawker_*_bpfel.go`) haven't been produced yet — they're gitignored and only materialize inside the Docker build context | Run `make ebpf-binary` once. The generated wrappers never leave the build context, so gopls will always show red until you've built at least once on this checkout. This is by design to keep the repo free of generated artifacts |
| Verifier rejects bytecode at runtime on a specific kernel | The pinned `clang`/`libbpf-dev` may be emitting BPF the target kernel can't load | Roll the pinned versions forward and test on the oldest supported kernel |

## Out of scope for this document

- **Release binary signing (SLSA attestation).** Tracked in
  `.serena/memories/outstanding-features.md`. The release workflow should
  emit a SLSA provenance attestation for the clawker CLI binary; the
  embedded `ebpf-manager` and `coredns-clawker` binaries (and the BPF
  bytecode inside them) are covered transitively because they're `go:embed`'d.
- **Multi-kernel CO-RE support.** Clawker targets modern cgroup v2 kernels
  where the UAPI types it consumes are stable. If future clawker features
  need to read in-kernel types (`struct task_struct`, etc.), the strategy
  changes — at that point we'd reintroduce a `vmlinux.h` source, but pinned
  via BTFHub or libbpf-bootstrap minimal rather than committed blind.
- **Removing the clawker-ebpf long-running container.** The current design
  keeps `clawker-ebpf` resident purely as an RPC endpoint for `docker exec`
  subcommand invocations; the BPF programs themselves live in kernel state
  and don't need a running container. Tracked as a follow-up along with
  the upcoming clawker control plane.
