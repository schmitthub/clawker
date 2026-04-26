// Package clawkerd embeds the per-container agent daemon binary into
// the clawker CLI release. The bundler reads this byte slice and
// writes it into every per-project agent build context so each
// generated image carries clawkerd as `/usr/local/bin/clawkerd`.
//
// The binary is produced by `make clawkerd-binary` (see Makefile) and
// gitignored under `assets/`. clawkerd is pure Go with no BPF
// dependencies, so the build is a plain CGO_ENABLED=0 cross-compile to
// the host's target architecture — much simpler than the multi-stage
// Dockerfile.controlplane recipe used for clawker-cp / ebpf-manager /
// coredns-clawker, which need clang + libbpf for the BPF byte code.
package clawkerd

import _ "embed"

// Binary is the pre-compiled static Linux clawkerd binary. The
// architecture matches the Docker host (BUILDX_TARGETARCH); for
// cross-platform clawker releases the bundler regenerates this asset
// per target arch and includes it in each cross-compiled clawker
// output.
//
//go:embed assets/clawkerd
var Binary []byte
