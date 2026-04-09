package firewall

import _ "embed"

// corednsClawkerBinary is the pre-compiled static Linux binary for custom
// CoreDNS with the dnsbpf plugin (real-time BPF dns_cache population).
// Built by: make coredns-binary
// Target: GOOS=linux CGO_ENABLED=0 go build ./cmd/coredns-clawker
//
// This binary is embedded into every clawker release binary so the custom
// CoreDNS container image can be built on-demand without a registry or source tree.
// The binary must match the Docker host's architecture (arm64 or amd64).
//
//go:embed assets/coredns-clawker
var corednsClawkerBinary []byte
