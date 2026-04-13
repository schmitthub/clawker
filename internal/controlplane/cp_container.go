package controlplane

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

const (
	// cpLogsContainerPath is the container-side directory for CP logs.
	// Bind-mounted from the host's state/logs directory.
	cpLogsContainerPath = "/var/log/clawker"

	// dockerSockPath is the host-side Docker socket path.
	dockerSockPath = "/var/run/docker.sock"
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
	// Env are environment variables for the container.
	Env []string
	// Cmd is the command to run inside the container.
	Cmd []string
	// NetworkName is the Docker network to attach to.
	NetworkName string
}

// localhost is the 127.0.0.1 address used for all published port bindings.
var localhost = netip.MustParseAddr("127.0.0.1")

// BuildCPContainerConfig constructs the CPContainerConfig for the control
// plane container. Reads all ports from cfg.Settings().ControlPlane —
// defaults come from struct tags via the storage layer.
//
// Bind mounts:
//   - Config dir (read-only) → CP loads config.NewConfig() inside the container
//   - CLI signing JWK (public key) → for Hydra client registration (read-only)
//   - Server TLS cert + key → for gRPC TLS (read-only)
//   - /sys/fs/cgroup, /sys/fs/bpf → for eBPF programs
//   - /var/run/docker.sock (read-only) → Docker API access for container state verification
//
// The CLI's private signing key NEVER enters the container.
func BuildCPContainerConfig(cfg config.Config) (*CPContainerConfig, error) {
	cp := cfg.Settings().ControlPlane

	portBinding := func(port int) (network.Port, []network.PortBinding) {
		return network.MustParsePort(fmt.Sprintf("%d/tcp", port)),
			[]network.PortBinding{{HostIP: localhost, HostPort: strconv.Itoa(port)}}
	}

	adminPort, adminBindings := portBinding(cp.AdminPort)
	hydraPort, hydraBindings := portBinding(cp.HydraPublicPort)
	oathkeeperPort, oathkeeperBindings := portBinding(cp.OathkeeperPort)
	healthPort, healthBindings := portBinding(cp.HealthPort)

	portBindings := network.PortMap{
		adminPort:      adminBindings,
		hydraPort:      hydraBindings,
		oathkeeperPort: oathkeeperBindings,
		healthPort:     healthBindings,
	}

	// Ensure host-side logs directory exists. ControlPlaneLogFilePath uses
	// the state dir (via consts.StateDir → XDG), which respects test isolation.
	cpLogPath, err := consts.ControlPlaneLogFilePath()
	if err != nil {
		return nil, fmt.Errorf("ensure CP log path: %w", err)
	}
	// The log file path is <stateDir>/logs/clawker-cp.log — mount the parent dir.
	hostLogsDir := filepath.Dir(cpLogPath)

	caCertPath, err := consts.AuthCACertPath()
	if err != nil {
		return nil, fmt.Errorf("resolve CA cert path: %w", err)
	}
	jwkPath, err := consts.AuthCLISigningJWKPath()
	if err != nil {
		return nil, fmt.Errorf("resolve signing JWK path: %w", err)
	}
	serverCertPath, err := consts.AuthServerCertPath()
	if err != nil {
		return nil, fmt.Errorf("resolve server cert path: %w", err)
	}
	serverKeyPath, err := consts.AuthServerKeyPath()
	if err != nil {
		return nil, fmt.Errorf("resolve server key path: %w", err)
	}

	// Verify Docker socket exists before adding it as a mount.
	if _, err := os.Stat(dockerSockPath); err != nil {
		return nil, fmt.Errorf("Docker socket not found at %s: %w (required for container state verification)", dockerSockPath, err)
	}

	// Config dir — CP loads config.NewConfig() from this mount.
	configDir := config.ConfigDir()

	mounts := []mount.Mount{
		// Clawker config directory — the CP reads settings (ports, etc.)
		// from the same config the host CLI uses.
		{
			Type:     mount.TypeBind,
			Source:   configDir,
			Target:   "/etc/clawker/config",
			ReadOnly: true,
		},
		// CLI CA cert — CP uses this to verify its own server cert chain
		// and for internal health check TLS trust.
		{
			Type:     mount.TypeBind,
			Source:   caCertPath,
			Target:   "/etc/clawker/tls/ca.pem",
			ReadOnly: true,
		},
		// CLI's public signing key (JWK) for Hydra client registration.
		{
			Type:     mount.TypeBind,
			Source:   jwkPath,
			Target:   "/etc/clawker/cli/signing-jwk.json",
			ReadOnly: true,
		},
		// Server TLS certificate.
		{
			Type:     mount.TypeBind,
			Source:   serverCertPath,
			Target:   "/etc/clawker/tls/server.pem",
			ReadOnly: true,
		},
		// Server TLS private key (CP needs it to serve TLS).
		{
			Type:     mount.TypeBind,
			Source:   serverKeyPath,
			Target:   "/etc/clawker/tls/server.key",
			ReadOnly: true,
		},
		// CP logs — persisted to host for auditing.
		{
			Type:   mount.TypeBind,
			Source: hostLogsDir,
			Target: cpLogsContainerPath,
		},
		// cgroup filesystem for eBPF program attachment.
		{
			Type:     mount.TypeBind,
			Source:   "/sys/fs/cgroup",
			Target:   "/sys/fs/cgroup",
			ReadOnly: true,
		},
		// BPF filesystem for pinned maps.
		{
			Type:   mount.TypeBind,
			Source: "/sys/fs/bpf",
			Target: "/sys/fs/bpf",
		},
		// Docker socket — CP needs Docker API access to verify container
		// existence (bypass timer dead-man switch, future lifecycle ops).
		{
			Type:     mount.TypeBind,
			Source:   dockerSockPath,
			Target:   "/var/run/docker.sock",
			ReadOnly: true,
		},
	}

	labels := map[string]string{
		consts.LabelManaged: consts.ManagedLabelValue,
		consts.LabelPurpose: consts.PurposeControlPlane,
	}

	return &CPContainerConfig{
		Image:        consts.CPImageTag,
		Labels:       labels,
		Mounts:       mounts,
		PortBindings: portBindings,
		CapAdd:       []string{"BPF", "SYS_ADMIN"},
		Env:          []string{cfg.ConfigDirEnvVar() + "=/etc/clawker/config"},
		Cmd:          []string{"/usr/local/bin/clawker-cp"},
		NetworkName:  consts.Network,
	}, nil
}
