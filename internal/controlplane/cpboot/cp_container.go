package cpboot

import (
	"fmt"
	"net/netip"
	"strconv"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// HostDirs carries the host-FS XDG-shaped directory roots the CP needs to
// compute sibling container bind mount sources when it creates Envoy /
// CoreDNS / future containers via Docker-outside-of-Docker. All four
// fields are REQUIRED — host-side callers resolve them via
// consts.ConfigDir() / DataDir() / StateDir() / CacheDir(). Missing any
// field fails fast at BuildCPContainerConfig and EnsureRunning rather
// than silently producing a bad bind source that only surfaces when
// Docker rejects the container-create request.
type HostDirs struct {
	Config string
	Data   string
	State  string
	Cache  string
}

// Validate returns an error naming the first empty field, or nil when
// all four are populated.
func (h HostDirs) Validate() error {
	switch {
	case h.Config == "":
		return fmt.Errorf("HostDirs.Config is required")
	case h.Data == "":
		return fmt.Errorf("HostDirs.Data is required")
	case h.State == "":
		return fmt.Errorf("HostDirs.State is required")
	case h.Cache == "":
		return fmt.Errorf("HostDirs.Cache is required")
	}
	return nil
}

// CPContainerOpts bundles the host-side inputs BuildCPContainerConfig
// needs that cannot be derived from config.Config alone.
type CPContainerOpts struct {
	HostDirs HostDirs
}

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
	// RestartPolicy is the Docker restart policy for the container.
	RestartPolicy container.RestartPolicy
	// ExtraHosts adds /etc/hosts entries inside the CP container.
	// Used to map host.docker.internal → host-gateway so the daemon
	// can reach host-loopback-bound services (currently the OTEL
	// collector OTLP HTTP receiver). Agent containers cannot reach the
	// same address because the BPF firewall redirects gateway traffic
	// for non-hostproxy ports to Envoy; the CP is exempt because it
	// owns container_map and is never enrolled.
	ExtraHosts []string
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
func BuildCPContainerConfig(cfg config.Config, opts CPContainerOpts) (*CPContainerConfig, error) {
	if err := opts.HostDirs.Validate(); err != nil {
		return nil, fmt.Errorf("cp container config: %w", err)
	}

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

	// The log file path is <stateDir>/logs/clawker-cp.log — mount the parent dir.
	hostLogsDir, err := consts.LogsSubdir()
	if err != nil {
		return nil, fmt.Errorf("resolve logs subdir: %w", err)
	}

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
	cpOtelClientCertPath, err := consts.AuthCPOtelClientCertPath()
	if err != nil {
		return nil, fmt.Errorf("resolve cp otel client cert path: %w", err)
	}
	cpOtelClientKeyPath, err := consts.AuthCPOtelClientKeyPath()
	if err != nil {
		return nil, fmt.Errorf("resolve cp otel client key path: %w", err)
	}

	// Config dir — CP loads config.NewConfig() from this mount.
	configDir := config.ConfigDir()

	// Firewall data dir — CP is sole writer of egress-rules.yaml, MITM CA,
	// and per-domain certs under this dir. Envoy/CoreDNS mount subpaths RO.
	firewallDataDir, err := consts.FirewallDataSubdir()
	if err != nil {
		return nil, fmt.Errorf("resolve firewall data dir: %w", err)
	}

	mounts := []mount.Mount{
		// Clawker config directory — the CP reads settings (ports, etc.)
		// from the same config the host CLI uses.
		{
			Type:     mount.TypeBind,
			Source:   configDir,
			Target:   consts.CPClawkerConfigDir,
			ReadOnly: true,
		},
		// CA cert — CP uses this to verify its own server cert chain
		// and for internal health check TLS trust.
		{
			Type:     mount.TypeBind,
			Source:   caCertPath,
			Target:   consts.CPCACertPath,
			ReadOnly: true,
		},
		// CLI's public signing key (JWK) for Hydra client registration.
		{
			Type:     mount.TypeBind,
			Source:   jwkPath,
			Target:   consts.CPCLIPubKeyPath,
			ReadOnly: true,
		},
		// Server TLS certificate.
		{
			Type:     mount.TypeBind,
			Source:   serverCertPath,
			Target:   consts.CPTLSCertPath,
			ReadOnly: true,
		},
		// Server TLS private key (CP needs it to serve TLS).
		{
			Type:     mount.TypeBind,
			Source:   serverKeyPath,
			Target:   consts.CPTLSKeyPath,
			ReadOnly: true,
		},
		// CP-side mTLS client cert + key for OTLP push to the
		// monitoring stack's CP-only receiver. Issued + signed by the
		// CLI CA at `clawker monitor init` (or any earlier CLI command
		// that calls auth.EnsureAuthMaterial). The receiver gates
		// `require_client_certificate: true` so this is the only way
		// for the daemon to push there.
		{
			Type:     mount.TypeBind,
			Source:   cpOtelClientCertPath,
			Target:   consts.CPOtelClientCertPath,
			ReadOnly: true,
		},
		{
			Type:     mount.TypeBind,
			Source:   cpOtelClientKeyPath,
			Target:   consts.CPOtelClientKeyPath,
			ReadOnly: true,
		},
		// CP logs — persisted to host for auditing.
		{
			Type:   mount.TypeBind,
			Source: hostLogsDir,
			Target: consts.CPLogsPath,
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
			Source:   consts.CPDockerSockPath,
			Target:   consts.CPDockerSockPath,
			ReadOnly: true,
		},
		// Firewall state dir — CP is the sole writer (INV-B2-001). Envoy
		// and CoreDNS mount subpaths RO; only the CP mounts RW.
		{
			Type:     mount.TypeBind,
			Source:   firewallDataDir,
			Target:   consts.CPFirewallDataDir,
			ReadOnly: false,
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
		Env: append([]string{
			consts.EnvConfigDir + "=" + consts.CPClawkerConfigDir,
			consts.EnvDataDir + "=" + consts.CPClawkerDataDir,
			consts.EnvHostConfigDir + "=" + opts.HostDirs.Config,
			consts.EnvHostDataDir + "=" + opts.HostDirs.Data,
			consts.EnvHostStateDir + "=" + opts.HostDirs.State,
			consts.EnvHostCacheDir + "=" + opts.HostDirs.Cache,
		}, otelLogsEnv(cfg)...),
		ExtraHosts:  []string{"host.docker.internal:host-gateway"},
		Cmd:         []string{"/usr/local/bin/clawker-cp"},
		NetworkName: consts.Network,
		// on-failure (not unless-stopped/always) so the CP's graceful
		// drain-to-zero exit from AgentWatcher is not undone by Docker.
		// Bounded retries keep a persistently crashing CP from thrashing.
		RestartPolicy: container.RestartPolicy{
			Name:              container.RestartPolicyOnFailure,
			MaximumRetryCount: consts.CPMaxRestartRetries,
		},
	}, nil
}

// otelLogsEnv injects the OTLP env vars the CP daemon reads to enable
// the OTEL bridge against the CP-only mTLS-gated receiver on the
// monitoring stack.
//
// Endpoint: https://host.docker.internal:<OtelCPPort>. CP is exempt
// from the BPF firewall (not enrolled in container_map) and has
// host.docker.internal mapped via ExtraHosts, so the dial reaches the
// host-loopback-bound docker port forwarder. Agents on clawker-net
// cannot present a CLI-signed client cert and so the receiver rejects
// their TLS handshake — the gate is crypto, not network.
//
// When OtelCPPort is zero (user hasn't run `clawker monitor init`)
// no env vars are emitted and the CP logger falls back to file-only.
// Logger construction also treats OTEL provider failure as non-fatal
// so the daemon survives a collector that's down at startup.
func otelLogsEnv(cfg config.Config) []string {
	mon := cfg.SettingsStore().Read().Monitoring
	if mon.OtelCPPort <= 0 {
		return nil
	}
	endpoint := fmt.Sprintf("https://host.docker.internal:%d", mon.OtelCPPort)
	logsEndpoint := fmt.Sprintf("%s%s", endpoint, telemetryPath(mon.Telemetry.LogsPath, "/v1/logs"))
	return []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + endpoint,
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=" + logsEndpoint,
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		// Client cert + key (mounted RO into the container). CA bundle
		// is the same trust root used for the AdminService server cert
		// — already mounted at consts.CPCACertPath.
		"OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE=" + consts.CPOtelClientCertPath,
		"OTEL_EXPORTER_OTLP_CLIENT_KEY=" + consts.CPOtelClientKeyPath,
		"OTEL_EXPORTER_OTLP_CERTIFICATE=" + consts.CPCACertPath,
		// service.name will be force-set on the receiver pipeline by a
		// resource processor (see otel-config.yaml.tmpl) — this value
		// is advisory and would be overwritten anyway. Setting it for
		// debug parity with file logs.
		"OTEL_RESOURCE_ATTRIBUTES=service.name=clawker-cp,component=clawker-cp",
	}
}

func telemetryPath(configured, fallback string) string {
	if configured != "" {
		return configured
	}
	return fallback
}
