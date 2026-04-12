package controlplane

import (
	"fmt"
	"net/netip"
	"strconv"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/schmitthub/clawker/internal/consts"
)

// CPContainerConfig holds the configuration for creating the control plane
// container. It is a structured representation that can be inspected and
// tested without requiring a Docker daemon.
type CPContainerConfig struct {
	// Image is the container image to use.
	Image string
	// Labels are the Docker labels applied to the container.
	Labels map[string]string
	// Mounts are the bind mounts for the container.
	Mounts []mount.Mount
	// PortBindings are the published port mappings.
	PortBindings network.PortMap
	// CapAdd are the Linux capabilities added to the container.
	CapAdd []string
	// Cmd is the command to run inside the container.
	Cmd []string
	// NetworkName is the Docker network to attach to.
	NetworkName string
}

// localhost is the 127.0.0.1 address used for all published port bindings.
var localhost = netip.MustParseAddr("127.0.0.1")

// BuildCPContainerConfig constructs the CPContainerConfig for the control
// plane container. The config includes all bind mounts, port bindings,
// labels, and capabilities needed to run the CP with the Ory auth stack.
//
// The dataDir is the clawker data directory containing auth material.
// The adminPort is the gRPC admin API port from Settings.
func BuildCPContainerConfig(dataDir string, adminPort int) (*CPContainerConfig, error) {
	portBindings := network.PortMap{
		// gRPC admin API — configurable port from Settings.
		network.MustParsePort(fmt.Sprintf("%d/tcp", adminPort)): {
			{HostIP: localhost, HostPort: strconv.Itoa(adminPort)},
		},
		// Hydra public API (token endpoint) — NOT the admin API (4445).
		network.MustParsePort(fmt.Sprintf("%d/tcp", consts.HydraPublicPort)): {
			{HostIP: localhost, HostPort: strconv.Itoa(consts.HydraPublicPort)},
		},
		// Oathkeeper HTTP proxy.
		network.MustParsePort(fmt.Sprintf("%d/tcp", consts.OathkeeperHTTPPort)): {
			{HostIP: localhost, HostPort: strconv.Itoa(consts.OathkeeperHTTPPort)},
		},
		// Healthz endpoint.
		network.MustParsePort(fmt.Sprintf("%d/tcp", consts.CPHealthPort)): {
			{HostIP: localhost, HostPort: strconv.Itoa(consts.CPHealthPort)},
		},
	}

	// Bind mounts: only public material is mounted into the container.
	// Private keys (CA key, CLI signing key, CLI mTLS key) NEVER enter containers.
	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   consts.AuthCACertPath(dataDir),
			Target:   "/etc/clawker/auth/ca/ca.pem",
			ReadOnly: true,
		},
		{
			Type:     mount.TypeBind,
			Source:   consts.AuthCLISigningJWKPath(dataDir),
			Target:   "/etc/clawker/auth/cli/signing-jwk.json",
			ReadOnly: true,
		},
		{
			Type:     mount.TypeBind,
			Source:   consts.AuthServerCertDir(dataDir),
			Target:   "/etc/clawker/auth/certs/server",
			ReadOnly: true,
		},
		{
			Type:     mount.TypeBind,
			Source:   "/sys/fs/cgroup",
			Target:   "/sys/fs/cgroup",
			ReadOnly: true,
		},
		{
			Type:   mount.TypeBind,
			Source: "/sys/fs/bpf",
			Target: "/sys/fs/bpf",
		},
	}

	labels := map[string]string{
		consts.LabelManaged: consts.ManagedLabelValue,
		consts.LabelPurpose: consts.PurposeControlPlane,
	}

	return &CPContainerConfig{
		Image:        consts.CPBaseImage,
		Labels:       labels,
		Mounts:       mounts,
		PortBindings: portBindings,
		CapAdd:       []string{"BPF", "SYS_ADMIN"},
		Cmd:          []string{"/usr/local/bin/clawker-cp"},
		NetworkName:  consts.Network,
	}, nil
}
