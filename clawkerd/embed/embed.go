// Package clawkerdembed embeds the per-container agent daemon binary
// into the clawker CLI release. The bundler reads this byte slice and
// writes it into every per-project agent build context so each
// generated image carries clawkerd as `/usr/local/bin/clawkerd`.
//
// The binary is produced by `make clawkerd-binary` (see Makefile) and
// gitignored under `assets/`. clawkerd is pure Go with no BPF
// dependencies, so the build is a plain CGO_ENABLED=0 cross-compile to
// the target architecture — unlike ebpf-manager, which requires clang +
// libbpf (via bpf2go) to compile the BPF byte code before the Go build.
package clawkerdembed

import _ "embed"

// Binary is the pre-compiled static Linux clawkerd binary. The
// architecture matches BUILDX_TARGETARCH. For cross-platform releases
// the goreleaser pipeline stages the correct arch binary here via
// `make stage-embeds-<arch>` before each `go build` of ./cmd/clawker.
//
//go:embed assets/clawkerd
var Binary []byte
