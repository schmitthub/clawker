package firewall

import _ "embed"

// clawkerCPBinary is the pre-compiled static Linux binary for the
// clawker control plane daemon.
//
// Built by: make cp-binary
// Target:   GOOS=linux CGO_ENABLED=0 go build ./cmd/clawker-cp
//
// This binary is embedded into every clawker release binary so the
// clawker-cp container image can be built on-demand without a registry
// or source tree. Like ebpfManagerBinary and corednsClawkerBinary, the
// binary must match the Docker host's architecture (arm64 or amd64).
//
// At runtime the firewall manager writes this binary into an on-demand
// Alpine image (see cpImageSpec in manager.go) and starts the clawker-cp
// container from that image. The daemon runs inside the container and
// replaces the historical "sleep infinity" entrypoint.
//
//go:embed assets/clawker-cp
var clawkerCPBinary []byte
