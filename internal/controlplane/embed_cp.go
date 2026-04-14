package controlplane

import _ "embed"

// ClawkerCPBinary is the pre-compiled static Linux binary for the
// clawker control plane daemon.
//
// Built by: make cp-binary
// Target:   GOOS=linux CGO_ENABLED=0 go build ./cmd/clawker-cp
//
// This binary is embedded into every clawker release binary so the
// clawker-cp container image can be built on-demand without a registry
// or source tree. Like EBPFManagerBinary and the firewall CoreDNS binary,
// it must match the Docker host's architecture (arm64 or amd64).
//
// At runtime the CP bootstrapper (B2 Task 3) writes this binary into an
// on-demand Alpine image and starts the clawker-cp container from that
// image. The daemon runs inside the container as PID 1.
//
//go:embed assets/clawker-cp
var ClawkerCPBinary []byte
