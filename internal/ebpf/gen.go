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
// Regeneration is intended to run inside the pinned clawker BPF builder
// image for byte-reproducible output. Developers iterating locally can run
// `make bpf-regenerate`, which builds that image and runs `go generate`
// inside it. See internal/ebpf/REPRODUCIBILITY.md.
//
// The generated files (clawker_*_bpfel.go, clawker_*_bpfel.o) are committed
// to the repo so `go build` does NOT require clang. Every PR runs
// `make bpf-verify` in CI to confirm the committed bytecode matches a fresh
// regeneration in the pinned image — so the commit is always anchored to a
// reproducible recipe, never trust-on-first-use.

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
