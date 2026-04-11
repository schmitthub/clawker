package ebpf

// Generate Go bindings from BPF C programs using bpf2go.
//
// Prerequisites (one-time setup):
//   1. Install clang and llvm-strip
//   2. Generate vmlinux.h from the target kernel's BTF:
//      bpftool btf dump file /sys/kernel/btf/vmlinux format c > internal/ebpf/bpf/vmlinux.h
//   3. Run go generate:
//      go generate ./internal/ebpf/...
//
// The generated files (clawker_bpfel.go, clawker_bpfeb.go, *.o) are checked
// into the repo so that building clawker does NOT require clang.

// bpf2go is pinned to match the cilium/ebpf library version in go.mod so the
// generator and the runtime agree on the loader shape. Update both together.
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go@v0.21.0 -cc clang -cflags "-O2 -g -Wall -Werror" -target amd64,arm64 clawker ./bpf/clawker.c -- -I./bpf
