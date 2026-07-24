// Package bpftest runs the production BPF decision helpers under
// BPF_PROG_TEST_RUN (the cilium bpf/tests pattern): SYSCALL-type wrapper
// programs in bpf/tests/decision_tests.c #include the production common.h,
// the harness loads them with real (unpinned) kernel maps, seeds the maps,
// runs the wrappers via prog.Run, and asserts on the results.
//
// The suite is privilege-gated: it needs CAP_BPF (root in practice) and a
// kernel ≥ 5.14 for BPF_PROG_TYPE_SYSCALL. Tests skip unless
// PRIVILEGED_TESTS=1 is set — run via `make test-bpf` on a Linux host or the
// CI privileged job. The clawker dev container has zero capabilities, so the
// suite always skips there.
//
// Toolchain prerequisites and pinning are identical to the production
// bindings — see controlplane/firewall/ebpf/gen.go. Generated files
// (testprogs_*_bpfel.go/.o) are gitignored and produced by `make ebpf`.
package bpftest

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go@v0.21.0 -cc clang -cflags "-O2 -g -Wall -Werror -I/usr/include/x86_64-linux-gnu -I/usr/include/aarch64-linux-gnu" -target amd64,arm64 -type test_scratch testprogs ../bpf/tests/decision_tests.c -- -I../bpf
