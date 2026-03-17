package firewall

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// Infrastructure container constants.
const (
	envoyContainerName   = "clawker-envoy"
	corednsContainerName = "clawker-coredns"

	envoyImage   = "envoyproxy/envoy:distroless-v1.32-latest"
	corednsImage = "coredns/coredns:1.12.0"
)

// Compile-time interface compliance check.
var _ FirewallManager = (*DockerFirewallManager)(nil)

// DockerFirewallManager implements FirewallManager using the Docker API.
// It manages Envoy and CoreDNS containers on the clawker-net Docker network.
type DockerFirewallManager struct {
	client *docker.Client
	cfg    config.Config
	log    *logger.Logger

	mu        sync.Mutex
	envoyIP   string
	corednsIP string
	netCIDR   string
	networkID string
}

// NewDockerFirewallManager creates a new DockerFirewallManager.
func NewDockerFirewallManager(client *docker.Client, cfg config.Config, log *logger.Logger) *DockerFirewallManager {
	if log == nil {
		log = logger.Nop()
	}
	return &DockerFirewallManager{
		client: client,
		cfg:    cfg,
		log:    log,
	}
}

// EnsureRunning starts the firewall stack if not already running.
// It handles partial state: network exists but no containers, one container
// running but not the other, or everything already running (no-op).
func (m *DockerFirewallManager) EnsureRunning(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Step 1: Ensure the shared Docker network exists.
	networkID, err := m.ensureNetwork(ctx)
	if err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	m.networkID = networkID

	// Step 2: Discover network IPs (gateway -> static IPs).
	if err := m.discoverNetwork(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	m.log.Debug().
		Str("envoy_ip", m.envoyIP).
		Str("coredns_ip", m.corednsIP).
		Str("cidr", m.netCIDR).
		Msg("firewall network discovered")

	// Step 3: Ensure config files exist (envoy.yaml, Corefile, certs).
	dataDir, err := m.ensureConfigs(ctx)
	if err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	// Step 4: Ensure containers are running.
	if err := m.ensureContainer(ctx, envoyContainerName, m.envoyContainerConfig(dataDir)); err != nil {
		return fmt.Errorf("firewall: ensuring envoy: %w", err)
	}

	if err := m.ensureContainer(ctx, corednsContainerName, m.corednsContainerConfig(dataDir)); err != nil {
		return fmt.Errorf("firewall: ensuring coredns: %w", err)
	}

	m.log.Debug().Msg("firewall stack running")
	return nil
}

// Stop tears down the firewall stack (containers only, not the network).
// The network is left in place because monitoring or agent containers may be attached.
func (m *DockerFirewallManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	envoyErr := m.stopAndRemove(ctx, envoyContainerName)
	corednsErr := m.stopAndRemove(ctx, corednsContainerName)

	// Clear cached network state.
	m.envoyIP = ""
	m.corednsIP = ""
	m.netCIDR = ""
	m.networkID = ""

	if err := errors.Join(envoyErr, corednsErr); err != nil {
		return fmt.Errorf("firewall stop: %w", err)
	}
	return nil
}

// IsRunning reports whether both firewall containers are running.
func (m *DockerFirewallManager) IsRunning(ctx context.Context) bool {
	return m.isContainerRunning(ctx, envoyContainerName) &&
		m.isContainerRunning(ctx, corednsContainerName)
}

// Update adds or updates egress rules, regenerates configs, and restarts Envoy if changed.
func (m *DockerFirewallManager) Update(ctx context.Context, rules []config.EgressRule) error {
	changed, err := UpdateRules(m.cfg, rules)
	if err != nil {
		return fmt.Errorf("firewall update: %w", err)
	}

	if !changed {
		return nil
	}

	return m.regenerateAndRestart(ctx)
}

// Remove deletes egress rules, regenerates configs, and restarts Envoy.
func (m *DockerFirewallManager) Remove(ctx context.Context, rules []config.EgressRule) error {
	if err := RemoveRules(m.cfg, rules); err != nil {
		return fmt.Errorf("firewall remove: %w", err)
	}

	return m.regenerateAndRestart(ctx)
}

// Reload force-regenerates envoy.yaml and Corefile from current rules
// and restarts the Envoy container.
func (m *DockerFirewallManager) Reload(ctx context.Context) error {
	return m.regenerateAndRestart(ctx)
}

// List returns all currently active egress rules.
func (m *DockerFirewallManager) List(_ context.Context) ([]config.EgressRule, error) {
	return ReadRules(m.cfg)
}

// Disable disconnects a container from the firewall network, blocking all egress
// through the firewall stack.
func (m *DockerFirewallManager) Disable(_ context.Context, _ string) error {
	// Requires docker exec iptables flush — depends on init-firewall.sh (Task 8).
	return fmt.Errorf("firewall disable: not yet implemented")
}

// Enable re-applies DNAT + firewall DNS in an agent container via docker exec.
func (m *DockerFirewallManager) Enable(_ context.Context, _ string) error {
	// Requires docker exec iptables restore — depends on init-firewall.sh (Task 8).
	return fmt.Errorf("firewall enable: not yet implemented")
}

// Bypass is a placeholder. Full implementation is in Task 10 (Dante bypass).
func (m *DockerFirewallManager) Bypass(_ context.Context, _ string, _ time.Duration) error {
	return fmt.Errorf("firewall bypass: not yet implemented")
}

// StopBypass is a placeholder. Full implementation is in Task 10 (Dante bypass).
func (m *DockerFirewallManager) StopBypass(_ context.Context, _ string) error {
	return fmt.Errorf("firewall stop bypass: not yet implemented")
}

// Status returns a health snapshot of the firewall stack.
func (m *DockerFirewallManager) Status(ctx context.Context) (*FirewallStatus, error) {
	rules, err := ReadRules(m.cfg)
	if err != nil {
		return nil, fmt.Errorf("firewall status: %w", err)
	}

	envoyRunning := m.isContainerRunning(ctx, envoyContainerName)
	corednsRunning := m.isContainerRunning(ctx, corednsContainerName)

	return &FirewallStatus{
		Running:       envoyRunning && corednsRunning,
		EnvoyHealth:   envoyRunning,
		CoreDNSHealth: corednsRunning,
		RuleCount:     len(rules),
		EnvoyIP:       m.envoyIP,
		CoreDNSIP:     m.corednsIP,
		NetworkID:     m.networkID,
	}, nil
}

// EnvoyIP returns the static IP assigned to the Envoy proxy container.
func (m *DockerFirewallManager) EnvoyIP() string { return m.envoyIP }

// CoreDNSIP returns the static IP assigned to the CoreDNS container.
func (m *DockerFirewallManager) CoreDNSIP() string { return m.corednsIP }

// NetCIDR returns the CIDR block of the isolated Docker firewall network.
func (m *DockerFirewallManager) NetCIDR() string { return m.netCIDR }

// --- Internal helpers ---

// ensureConfigs writes envoy.yaml, Corefile, and certs to the data directory
// if they don't already exist. On first run this bootstraps the config files;
// on subsequent runs it's a no-op (Reload handles regeneration).
// Returns the resolved data directory path for reuse by callers.
func (m *DockerFirewallManager) ensureConfigs(_ context.Context) (string, error) {
	dataDir, err := m.cfg.FirewallDataSubdir()
	if err != nil {
		return "", fmt.Errorf("resolving firewall data dir: %w", err)
	}

	// Ensure certs exist (CA + domain certs for MITM rules).
	caCert, caKey, err := EnsureCA(dataDir)
	if err != nil {
		return "", fmt.Errorf("ensuring CA: %w", err)
	}

	// Read current rules (may be empty on first run).
	rules, err := ReadRules(m.cfg)
	if err != nil {
		return "", fmt.Errorf("reading rules: %w", err)
	}

	// Merge required firewall rules.
	allRules := mergeRequiredRules(m.cfg, rules)

	// Regenerate domain certs for any MITM rules.
	if err := RegenerateDomainCerts(allRules, dataDir, caCert, caKey); err != nil {
		return "", fmt.Errorf("regenerating domain certs: %w", err)
	}

	// Generate Envoy config.
	envoyYAML, warnings, err := GenerateEnvoyConfig(allRules)
	if err != nil {
		return "", fmt.Errorf("generating envoy config: %w", err)
	}
	for _, w := range warnings {
		m.log.Warn().Str("component", "envoy").Msg(w)
	}
	envoyPath := filepath.Join(dataDir, "envoy.yaml")
	if err := os.WriteFile(envoyPath, envoyYAML, 0o644); err != nil {
		return "", fmt.Errorf("writing envoy.yaml: %w", err)
	}

	// Generate Corefile.
	corefile, err := GenerateCorefile(allRules)
	if err != nil {
		return "", fmt.Errorf("generating Corefile: %w", err)
	}
	corefilePath := filepath.Join(dataDir, "Corefile")
	if err := os.WriteFile(corefilePath, corefile, 0o644); err != nil {
		return "", fmt.Errorf("writing Corefile: %w", err)
	}

	return dataDir, nil
}

// regenerateAndRestart regenerates config files and restarts Envoy.
// CoreDNS picks up changes via the reload plugin automatically.
func (m *DockerFirewallManager) regenerateAndRestart(ctx context.Context) error {
	if _, err := m.ensureConfigs(ctx); err != nil {
		return fmt.Errorf("regenerating configs: %w", err)
	}

	// Restart Envoy to pick up config changes.
	// CoreDNS auto-reloads via the reload plugin in Corefile.
	return m.restartContainer(ctx, envoyContainerName)
}

// mergeRequiredRules prepends required firewall rules to user rules,
// deduplicating by destination.
func mergeRequiredRules(cfg config.Config, userRules []config.EgressRule) []config.EgressRule {
	required := cfg.RequiredFirewallRules()

	// Build a set of user-provided destinations to avoid duplicating required rules.
	userDsts := make(map[string]struct{}, len(userRules))
	for _, r := range userRules {
		userDsts[strings.ToLower(r.Dst)] = struct{}{}
	}

	merged := make([]config.EgressRule, 0, len(required)+len(userRules))
	for _, r := range required {
		if _, exists := userDsts[strings.ToLower(r.Dst)]; !exists {
			merged = append(merged, r)
		}
	}
	merged = append(merged, userRules...)

	return merged
}

// envoyContainerConfig returns the container creation config for the Envoy proxy.
// dataDir must be pre-validated (ensureConfigs checks FirewallDataSubdir before this is called).
func (m *DockerFirewallManager) envoyContainerConfig(dataDir string) containerSpec {
	return containerSpec{
		image:       envoyImage,
		networkName: m.cfg.ClawkerNetwork(),
		networkID:   m.networkID,
		staticIP:    m.envoyIP,
		labels: map[string]string{
			m.cfg.LabelManaged(): "true",
			m.cfg.LabelPurpose(): "firewall-envoy",
		},
		mounts: []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   filepath.Join(dataDir, "envoy.yaml"),
				Target:   "/etc/envoy/envoy.yaml",
				ReadOnly: true,
			},
			{
				Type:     mount.TypeBind,
				Source:   filepath.Join(dataDir, "certs"),
				Target:   "/etc/envoy/certs",
				ReadOnly: true,
			},
		},
	}
}

// corednsContainerConfig returns the container creation config for the CoreDNS resolver.
// dataDir must be pre-validated (ensureConfigs checks FirewallDataSubdir before this is called).
func (m *DockerFirewallManager) corednsContainerConfig(dataDir string) containerSpec {
	return containerSpec{
		image:       corednsImage,
		networkName: m.cfg.ClawkerNetwork(),
		networkID:   m.networkID,
		staticIP:    m.corednsIP,
		labels: map[string]string{
			m.cfg.LabelManaged(): "true",
			m.cfg.LabelPurpose(): "firewall-coredns",
		},
		cmd: []string{"-conf", "/etc/coredns/Corefile"},
		mounts: []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   filepath.Join(dataDir, "Corefile"),
				Target:   "/etc/coredns/Corefile",
				ReadOnly: true,
			},
		},
	}
}

// containerSpec captures the configuration for a firewall infrastructure container.
type containerSpec struct {
	image       string
	networkName string
	networkID   string
	staticIP    string
	labels      map[string]string
	cmd         []string
	mounts      []mount.Mount
}

// ensureContainer ensures a named container exists and is running.
// If the container exists but is stopped, it is started.
// If it doesn't exist, it is created and started.
func (m *DockerFirewallManager) ensureContainer(ctx context.Context, name string, spec containerSpec) error {
	ctr, err := m.client.FindContainerByName(ctx, name)
	if err == nil && ctr != nil {
		// Container exists — check if it's running.
		if ctr.State == container.StateRunning {
			m.log.Debug().Str("container", name).Msg("firewall container already running")
			return nil
		}

		// Container exists but not running — start it.
		m.log.Debug().Str("container", name).Str("state", string(ctr.State)).Msg("starting existing firewall container")
		_, startErr := m.client.ContainerStart(ctx, docker.ContainerStartOptions{
			ContainerID: ctr.ID,
		})
		return startErr
	}

	// Container does not exist — create and start it.
	m.log.Debug().Str("container", name).Str("image", spec.image).Msg("creating firewall container")

	ip, _ := netip.ParseAddr(spec.staticIP)
	endpointSettings := &network.EndpointSettings{
		NetworkID: spec.networkID,
		IPAMConfig: &network.EndpointIPAMConfig{
			IPv4Address: ip,
		},
	}

	containerConfig := &container.Config{
		Image:  spec.image,
		Labels: spec.labels,
	}
	if len(spec.cmd) > 0 {
		containerConfig.Cmd = spec.cmd
	}

	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
		Mounts: spec.mounts,
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			spec.networkName: endpointSettings,
		},
	}

	createResult, err := m.client.ContainerCreate(ctx, docker.ContainerCreateOptions{
		Config:           containerConfig,
		HostConfig:       hostConfig,
		NetworkingConfig: networkingConfig,
		Name:             name,
	})
	if err != nil {
		return fmt.Errorf("creating container %s: %w", name, err)
	}

	_, err = m.client.ContainerStart(ctx, docker.ContainerStartOptions{
		ContainerID: createResult.ID,
	})
	if err != nil {
		return fmt.Errorf("starting container %s: %w", name, err)
	}

	return nil
}

// stopAndRemove stops and removes a container by name.
// Not-found errors are silently ignored.
func (m *DockerFirewallManager) stopAndRemove(ctx context.Context, name string) error {
	ctr, err := m.client.FindContainerByName(ctx, name)
	if err != nil {
		m.log.Debug().Str("container", name).Err(err).Msg("container not found or lookup failed, skipping stop")
		return nil
	}

	if ctr.State == container.StateRunning {
		timeout := 10
		if _, err := m.client.ContainerStop(ctx, ctr.ID, &timeout); err != nil {
			m.log.Warn().Err(err).Str("container", name).Msg("failed to stop container gracefully")
		}
	}

	_, err = m.client.ContainerRemove(ctx, ctr.ID, true)
	if err != nil {
		return fmt.Errorf("removing container %s: %w", name, err)
	}

	m.log.Debug().Str("container", name).Msg("container removed")
	return nil
}

// isContainerRunning checks whether a named container exists and is running.
func (m *DockerFirewallManager) isContainerRunning(ctx context.Context, name string) bool {
	ctr, err := m.client.FindContainerByName(ctx, name)
	if err != nil {
		return false
	}
	return ctr.State == container.StateRunning
}

// restartContainer restarts a running container by name.
func (m *DockerFirewallManager) restartContainer(ctx context.Context, name string) error {
	ctr, err := m.client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("finding container %s: %w", name, err)
	}

	timeout := 10
	_, err = m.client.ContainerRestart(ctx, ctr.ID, &timeout)
	if err != nil {
		return fmt.Errorf("restarting container %s: %w", name, err)
	}

	return nil
}
