package controlplane

import _ "embed"

// EBPFManagerBinary is the pre-compiled static Linux binary for the eBPF manager.
// Built by: make ebpf-binary
// Target: GOOS=linux CGO_ENABLED=0 go build ./internal/controlplane/firewall/ebpf/cmd
//
// This binary is embedded into every clawker release binary so the eBPF manager
// container image can be built on-demand without a registry or source tree.
// The binary must match the Docker host's architecture (arm64 or amd64).
//
//go:embed assets/ebpf-manager
var EBPFManagerBinary []byte
