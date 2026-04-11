package ebpf

// Generate Go bindings from BPF C programs using bpf2go.
//
// Prerequisites:
//   1. clang + llvm-strip
//   2. libbpf headers (libbpf-dev on Debian/Ubuntu) providing bpf/bpf_helpers.h
//      and bpf/bpf_endian.h
//   3. Kernel UAPI headers (linux-libc-dev on Debian/Ubuntu) providing
//      linux/bpf.h and asm/types.h. No vmlinux.h is required — clawker's
//      BPF programs touch only stable kernel UAPI (struct bpf_sock_addr,
//      struct bpf_sock, BPF_MAP_TYPE_*, LIBBPF_PIN_BY_NAME) and use no CO-RE
//      relocations, so vmlinux.h is unnecessary and has been removed.
//
// The generated files (clawker_*_bpfel.go, clawker_*_bpfel.o) are NOT
// committed to the repo — they are gitignored and produced fresh on every
// build by `make ebpf-binary`, which runs `go generate` inside the pinned
// bpf-builder stage of Dockerfile.firewall. Reproducibility is structural:
// the pinned multi-stage Dockerfile is the source of truth; there is no
// separate committed artifact to drift from. See
// internal/ebpf/REPRODUCIBILITY.md for the full provenance chain.

// bpf2go is pinned to match the cilium/ebpf library version in go.mod so the
// generator and the runtime agree on the loader shape. Update both together.
//
// The -I/usr/include/<triple>-linux-gnu paths resolve <asm/types.h>, which
// is pulled in transitively by <linux/bpf.h>. Debian/Ubuntu ships it under
// the multiarch triple; both entries are listed so generation works on
// either arch host. Only the matching one exists on any given host — the
// other is silently ignored.
//
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go@v0.21.0 -cc clang -cflags "-O2 -g -Wall -Werror -I/usr/include/x86_64-linux-gnu -I/usr/include/aarch64-linux-gnu" -target amd64,arm64 clawker ./bpf/clawker.c -- -I./bpf
