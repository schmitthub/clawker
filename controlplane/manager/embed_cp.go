package manager

import _ "embed"

// ClawkerCPBinary is the pre-compiled static Linux binary for the
// clawker control plane daemon.
//
// Built by: make cp-binary
// Target:   GOOS=linux CGO_ENABLED=0 go build ./cmd/clawkercp
//
// This binary is embedded into every clawker release binary so the
// clawkercp container image can be built on-demand without a registry
// or source tree. Like EBPFManagerBinary and the firewall CoreDNS binary,
// it must match the Docker host's architecture (arm64 or amd64).
//
// At runtime the CP bootstrapper writes this binary into an on-demand
// distroless/static-debian12 image and starts the clawkercp container
// from that image. The daemon runs inside the container as PID 1.
//
// Lives in the manager subpackage so cmd/clawkercp can import
// internal/controlplane without dragging in this embed directive —
// the daemon would otherwise need to embed itself, which can't resolve
// during its own build.
//
//go:embed assets/clawkercp
var ClawkerCPBinary []byte
