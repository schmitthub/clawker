package firewall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/storage"
)

// Infrastructure container constants.
const (
	envoyContainerName   = "clawker-envoy"
	corednsContainerName = "clawker-coredns"

	envoyImage   = "envoyproxy/envoy:distroless-v1.37.1@sha256:4d9226b9fd4d1449887de7cde785beb24b12e47d6e79021dec3c79e362609432"
	corednsImage = "coredns/coredns:1.14.2@sha256:e7e6440cfd1e919280958f5b5a6ab2b184d385bba774c12ad2a9e1e4183f90d9"

	// Health check settings.
	// Published to localhost so the host-side CLI and daemon can probe health.
	// TODO: move to config/consts.go behind interface accessors.
	envoyHealthHostPort = 18901 // Envoy TLS port published to host
	corednsHealthPort   = 18902 // CoreDNS health port (inside container + published)
	corednsHealthPath   = "/health"
	healthCheckTimeout  = 30 * time.Second
	healthCheckInterval = 500 * time.Millisecond
)

// Dante SOCKS proxy paths for firewall bypass.
const (
	danteConfPath       = "/run/firewall-bypass-danted.conf"
	proxychainsConfPath = "/run/firewall-bypass-proxychains.conf"
	dantePIDPath        = "/run/firewall-bypass-danted.pid"
)

// Compile-time interface compliance check.
var _ FirewallManager = (*Manager)(nil)

// Manager implements FirewallManager using the Docker API.
// It manages Envoy and CoreDNS containers on the clawker-net Docker network.
type Manager struct {
	client *docker.Client
	cfg    config.Config
	log    *logger.Logger
	store  *storage.Store[EgressRulesFile]
}

// NewManager creates a new DockerFirewallManager.
func NewManager(client *docker.Client, cfg config.Config, log *logger.Logger) (*Manager, error) {
	if log == nil {
		log = logger.Nop()
	}
	store, err := NewRulesStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("firewall: creating rules store: %w", err)
	}
	return &Manager{
		client: client,
		cfg:    cfg,
		log:    log,
		store:  store,
	}, nil
}

// EnsureRunning starts the firewall stack if not already running.
// It syncs project rules from config (additive merge with existing state),
// regenerates Envoy/CoreDNS configs, and ensures containers are running.
// Idempotent — safe to call on every container startup.
func (m *Manager) EnsureRunning(ctx context.Context) error {
	// Step 1: Ensure the shared Docker network exists.
	if _, err := m.ensureNetwork(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	// Step 2: Discover network IPs (gateway -> static IPs).
	netInfo, err := m.discoverNetwork(ctx)
	if err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	m.log.Debug().
		Str("envoy_ip", netInfo.EnvoyIP).
		Str("coredns_ip", netInfo.CoreDNSIP).
		Str("cidr", netInfo.CIDR).
		Msg("firewall network discovered")

	// Step 3: Sync project rules from config into state (additive).
	if err := m.syncProjectRules(); err != nil {
		return fmt.Errorf("firewall: syncing project rules: %w", err)
	}

	// Step 4: Ensure config files exist (envoy.yaml, Corefile, certs).
	dataDir, err := m.ensureConfigs(ctx)
	if err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	// Step 5: Ensure containers are running.
	if err := m.ensureContainer(ctx, envoyContainerName, m.envoyContainerConfig(netInfo, dataDir)); err != nil {
		return fmt.Errorf("firewall: ensuring envoy: %w", err)
	}

	if err := m.ensureContainer(ctx, corednsContainerName, m.corednsContainerConfig(netInfo, dataDir)); err != nil {
		return fmt.Errorf("firewall: ensuring coredns: %w", err)
	}

	// Step 6: Wait for services to be healthy before declaring ready.
	if err := m.WaitForHealthy(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	m.log.Debug().Msg("firewall stack running")
	return nil
}

// Stop tears down the firewall stack (containers only, not the network).
// The network is left in place because monitoring or agent containers may be attached.
func (m *Manager) Stop(ctx context.Context) error {
	envoyErr := m.stopAndRemove(ctx, envoyContainerName)
	corednsErr := m.stopAndRemove(ctx, corednsContainerName)

	if err := errors.Join(envoyErr, corednsErr); err != nil {
		return fmt.Errorf("firewall stop: %w", err)
	}
	return nil
}

// IsRunning reports whether both firewall containers are running.
func (m *Manager) IsRunning(ctx context.Context) bool {
	return m.isContainerRunning(ctx, envoyContainerName) &&
		m.isContainerRunning(ctx, corednsContainerName)
}

// WaitForHealthy polls until both firewall services pass health probes (TCP+HTTP)
// or the context expires. Probes go through published localhost ports so they work
// from the host (macOS Docker Desktop doesn't route to container IPs).
//
//   - Envoy: TCP connect to localhost:18901 (published TLS listener).
//   - CoreDNS: HTTP GET to localhost:18902/health (published health endpoint).
func (m *Manager) WaitForHealthy(ctx context.Context) error {
	deadline := time.Now().Add(healthCheckTimeout)
	httpClient := &http.Client{Timeout: 2 * time.Second}

	envoyAddr := fmt.Sprintf("localhost:%d", envoyHealthHostPort)
	corednsURL := fmt.Sprintf("http://localhost:%d%s", corednsHealthPort, corednsHealthPath)

	var envoyReady, corednsReady bool

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if !envoyReady {
			conn, err := net.DialTimeout("tcp", envoyAddr, 2*time.Second)
			if err == nil {
				conn.Close()
				envoyReady = true
				m.log.Debug().Msg("envoy health check passed (TCP connect)")
			}
		}

		if !corednsReady {
			if resp, err := httpClient.Get(corednsURL); err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					corednsReady = true
					m.log.Debug().Msg("coredns health check passed (HTTP 200)")
				}
			}
		}

		if envoyReady && corednsReady {
			return nil
		}

		time.Sleep(healthCheckInterval)
	}

	var unhealthy error
	if !envoyReady {
		unhealthy = errors.Join(unhealthy, ErrEnvoyUnhealthy)
	}
	if !corednsReady {
		unhealthy = errors.Join(unhealthy, ErrCoreDNSUnhealthy)
	}
	return &HealthTimeoutError{Timeout: healthCheckTimeout, Err: unhealthy}
}

// AddRules adds individual egress rules to the running firewall (CLI "firewall add").
// Deduplicates against existing rules, persists, regenerates configs, and hot-reloads.
func (m *Manager) AddRules(ctx context.Context, rules []config.EgressRule) error {
	changed, err := m.addRulesToStore(rules)
	if err != nil {
		return fmt.Errorf("firewall add: %w", err)
	}
	if !changed {
		return nil
	}
	return m.regenerateAndRestart(ctx)
}

// RemoveRules deletes egress rules from the running firewall (CLI "firewall remove").
// Persists, regenerates configs, and hot-reloads.
func (m *Manager) RemoveRules(ctx context.Context, rules []config.EgressRule) error {
	if err := m.removeRulesFromStore(rules); err != nil {
		return fmt.Errorf("firewall remove: %w", err)
	}
	return m.regenerateAndRestart(ctx)
}

// syncProjectRules reads project rules (AddDomains + Rules) and required rules from
// config, normalizes them, and additively merges into the rules store.
func (m *Manager) syncProjectRules() error {
	projectFw := m.cfg.Project().Security.Firewall
	required := m.cfg.RequiredFirewallRules()

	var incoming []config.EgressRule

	// Required rules first.
	incoming = append(incoming, required...)

	// Project rules.
	if projectFw != nil {
		incoming = append(incoming, projectFw.Rules...)
		// Convert AddDomains to EgressRules.
		for _, d := range projectFw.AddDomains {
			incoming = append(incoming, config.EgressRule{Dst: d, Proto: "tls", Action: "allow"})
		}
	}

	_, err := m.addRulesToStore(incoming)
	return err
}

// addRulesToStore deduplicates and writes rules to the store. Returns true if any new rules were added.
func (m *Manager) addRulesToStore(rules []config.EgressRule) (bool, error) {
	current := m.store.Read()

	known := make(map[string]struct{}, len(current.Rules))
	for _, r := range current.Rules {
		known[ruleKey(r)] = struct{}{}
	}

	var newRules []config.EgressRule
	for _, r := range rules {
		if r.Proto == "" {
			r.Proto = "tls"
		}
		if r.Action == "" {
			r.Action = "allow"
		}
		key := ruleKey(r)
		if _, exists := known[key]; exists {
			continue
		}
		known[key] = struct{}{}
		newRules = append(newRules, r)
	}

	if len(newRules) == 0 {
		return false, nil
	}

	if err := m.store.Set(func(f *EgressRulesFile) {
		f.Rules = append(f.Rules, newRules...)
	}); err != nil {
		return false, fmt.Errorf("updating rules: %w", err)
	}

	if err := m.store.Write(); err != nil {
		return false, fmt.Errorf("writing rules: %w", err)
	}

	return true, nil
}

// removeRulesFromStore removes matching rules from the store.
func (m *Manager) removeRulesFromStore(toRemove []config.EgressRule) error {
	if len(toRemove) == 0 {
		return nil
	}

	removeSet := make(map[string]struct{}, len(toRemove))
	for _, r := range toRemove {
		removeSet[ruleKey(r)] = struct{}{}
	}

	current := m.store.Read()
	filtered := make([]config.EgressRule, 0, len(current.Rules))
	for _, r := range current.Rules {
		if _, remove := removeSet[ruleKey(r)]; !remove {
			filtered = append(filtered, r)
		}
	}

	if len(filtered) == len(current.Rules) {
		return nil
	}

	if err := m.store.Set(func(f *EgressRulesFile) {
		f.Rules = filtered
	}); err != nil {
		return fmt.Errorf("removing rules: %w", err)
	}

	return m.store.Write()
}

// Reload force-regenerates envoy.yaml and Corefile from current rules
// and restarts the Envoy container.
func (m *Manager) Reload(ctx context.Context) error {
	return m.regenerateAndRestart(ctx)
}

// List returns all currently active egress rules.
func (m *Manager) List(_ context.Context) ([]config.EgressRule, error) {
	return m.store.Read().Rules, nil
}

// Disable disconnects a container from the firewall network, blocking all egress
// through the firewall stack.
func (m *Manager) Disable(_ context.Context, _ string) error {
	// Requires docker exec iptables flush — depends on init-firewall.sh (Task 8).
	return fmt.Errorf("firewall disable: not yet implemented")
}

// Enable re-applies DNAT + firewall DNS in an agent container via docker exec.
func (m *Manager) Enable(_ context.Context, _ string) error {
	// Requires docker exec iptables restore — depends on init-firewall.sh (Task 8).
	return fmt.Errorf("firewall enable: not yet implemented")
}

// Bypass starts a Dante SOCKS proxy inside the container, giving it unrestricted
// egress for the specified duration. Root (uid 0) bypasses iptables RETURN rules
// installed by init-firewall.sh, so danted traffic flows directly.
func (m *Manager) Bypass(ctx context.Context, containerID string, timeout time.Duration) error {
	const (
		danteConf = `logoutput: stderr
internal: 127.0.0.1 port = 9100
external: eth0
socksmethod: none
client pass {
    from: 127.0.0.0/8 to: 0.0.0.0/0
}
socks pass {
    from: 127.0.0.0/8 to: 0.0.0.0/0
}
`
		proxychainsConf = `strict_chain
proxy_dns
[ProxyList]
socks5 127.0.0.1 9100
`
	)

	timeoutSecs := int(timeout.Seconds())
	if timeoutSecs <= 0 {
		timeoutSecs = 300 // default 5 minutes
	}

	// Single shell script: write configs, start danted, schedule timeout cleanup.
	// Runs as root (uid 0 bypasses iptables RETURN rule installed by init-firewall.sh).
	script := fmt.Sprintf(
		`printf '%%s' %s > %s && `+
			`printf '%%s' %s > %s && `+
			`danted -f %s -p %s && `+
			`(sleep %d && kill $(cat %s) 2>/dev/null && rm -f %s %s %s) &`,
		shellQuote(danteConf), danteConfPath,
		shellQuote(proxychainsConf), proxychainsConfPath,
		danteConfPath, dantePIDPath,
		timeoutSecs, dantePIDPath,
		danteConfPath, proxychainsConfPath, dantePIDPath,
	)

	execResp, err := m.client.ExecCreate(ctx, containerID, docker.ExecCreateOptions{
		User: "root",
		Cmd:  []string{"sh", "-c", script},
	})
	if err != nil {
		return fmt.Errorf("firewall bypass: creating exec: %w", err)
	}

	_, err = m.client.ExecStart(ctx, execResp.ID, docker.ExecStartOptions{
		Detach: true,
	})
	if err != nil {
		return fmt.Errorf("firewall bypass: starting danted: %w", err)
	}

	m.log.Debug().
		Str("container", containerID).
		Int("timeout_secs", timeoutSecs).
		Msg("firewall bypass started")

	return nil
}

// StopBypass kills the Dante SOCKS proxy and removes config files.
func (m *Manager) StopBypass(ctx context.Context, containerID string) error {
	// Kill danted via PID file (ignore errors if already dead) and remove config files.
	script := fmt.Sprintf(
		`kill $(cat %s 2>/dev/null) 2>/dev/null; rm -f %s %s %s`,
		dantePIDPath, danteConfPath, proxychainsConfPath, dantePIDPath,
	)

	execResp, err := m.client.ExecCreate(ctx, containerID, docker.ExecCreateOptions{
		User: "root",
		Cmd:  []string{"sh", "-c", script},
	})
	if err != nil {
		return fmt.Errorf("firewall stop bypass: creating exec: %w", err)
	}

	_, err = m.client.ExecStart(ctx, execResp.ID, docker.ExecStartOptions{
		Detach: true,
	})
	if err != nil {
		return fmt.Errorf("firewall stop bypass: killing danted: %w", err)
	}

	m.log.Debug().
		Str("container", containerID).
		Msg("firewall bypass stopped")

	return nil
}

// Status returns a health snapshot of the firewall stack.
func (m *Manager) Status(ctx context.Context) (*FirewallStatus, error) {
	rules := m.store.Read()

	// Discover network state from Docker (best-effort; fields stay empty if network doesn't exist).
	netInfo, _ := m.discoverNetwork(ctx)

	envoyRunning := m.isContainerRunning(ctx, envoyContainerName)
	corednsRunning := m.isContainerRunning(ctx, corednsContainerName)

	status := &FirewallStatus{
		Running:       envoyRunning && corednsRunning,
		EnvoyHealth:   envoyRunning,
		CoreDNSHealth: corednsRunning,
		RuleCount:     len(rules.Rules),
	}
	if netInfo != nil {
		status.EnvoyIP = netInfo.EnvoyIP
		status.CoreDNSIP = netInfo.CoreDNSIP
		status.NetworkID = netInfo.NetworkID
	}
	return status, nil
}

// EnvoyIP returns the static IP assigned to the Envoy proxy container.
func (m *Manager) EnvoyIP() string {
	netInfo, err := m.discoverNetwork(context.Background())
	if err != nil {
		return ""
	}
	return netInfo.EnvoyIP
}

// CoreDNSIP returns the static IP assigned to the CoreDNS container.
func (m *Manager) CoreDNSIP() string {
	netInfo, err := m.discoverNetwork(context.Background())
	if err != nil {
		return ""
	}
	return netInfo.CoreDNSIP
}

// NetCIDR returns the CIDR block of the isolated Docker firewall network.
func (m *Manager) NetCIDR() string {
	netInfo, err := m.discoverNetwork(context.Background())
	if err != nil {
		return ""
	}
	return netInfo.CIDR
}

// --- Internal helpers ---

// ensureConfigs writes envoy.yaml, Corefile, and certs to the data directory.
// Reads rules from the store (already merged by syncProjectRules/AddRules).
// Returns the resolved data directory path for reuse by callers.
func (m *Manager) ensureConfigs(_ context.Context) (string, error) {
	dataDir, err := m.cfg.FirewallDataSubdir()
	if err != nil {
		return "", fmt.Errorf("resolving firewall data dir: %w", err)
	}

	// Ensure certs exist (CA + domain certs for MITM rules).
	caCert, caKey, err := EnsureCA(dataDir)
	if err != nil {
		return "", fmt.Errorf("ensuring CA: %w", err)
	}

	// Read current rules from store.
	allRules := m.store.Read().Rules

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

// regenerateAndRestart regenerates config files, restarts both Envoy and CoreDNS,
// and waits for both services to be healthy before returning.
// Both containers are restarted to guarantee they pick up the new configs
// immediately — CoreDNS's reload plugin has a polling delay that would otherwise
// create a window where the old rules are still active.
func (m *Manager) regenerateAndRestart(ctx context.Context) error {
	if _, err := m.ensureConfigs(ctx); err != nil {
		return fmt.Errorf("regenerating configs: %w", err)
	}

	if err := m.restartContainer(ctx, envoyContainerName); err != nil {
		return err
	}
	if err := m.restartContainer(ctx, corednsContainerName); err != nil {
		return err
	}

	return m.WaitForHealthy(ctx)
}

// envoyContainerConfig returns the container creation config for the Envoy proxy.
// dataDir must be pre-validated (ensureConfigs checks FirewallDataSubdir before this is called).
func (m *Manager) envoyContainerConfig(net *NetworkInfo, dataDir string) containerSpec {
	return containerSpec{
		image:       envoyImage,
		networkName: m.cfg.ClawkerNetwork(),
		networkID:   net.NetworkID,
		staticIP:    net.EnvoyIP,
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
		// Publish TLS listener for host-side health probes (TCP connect).
		portBindings: network.PortMap{
			network.MustParsePort(fmt.Sprintf("%d/tcp", EnvoyTLSPort)): {
				{HostPort: strconv.Itoa(envoyHealthHostPort)},
			},
		},
	}
}

// corednsContainerConfig returns the container creation config for the CoreDNS resolver.
// dataDir must be pre-validated (ensureConfigs checks FirewallDataSubdir before this is called).
func (m *Manager) corednsContainerConfig(net *NetworkInfo, dataDir string) containerSpec {
	return containerSpec{
		image:       corednsImage,
		networkName: m.cfg.ClawkerNetwork(),
		networkID:   net.NetworkID,
		staticIP:    net.CoreDNSIP,
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
		// Publish health endpoint for host-side health probes.
		portBindings: network.PortMap{
			network.MustParsePort(fmt.Sprintf("%d/tcp", corednsHealthPort)): {
				{HostPort: strconv.Itoa(corednsHealthPort)},
			},
		},
	}
}

// containerSpec captures the configuration for a firewall infrastructure container.
type containerSpec struct {
	image        string
	networkName  string
	networkID    string
	staticIP     string
	labels       map[string]string
	cmd          []string
	mounts       []mount.Mount
	portBindings network.PortMap
}

// ensureContainer ensures a named container exists and is running.
// Cross-process safe: handles name conflicts (daemon vs CLI race) and
// stale stopped containers (bind mounts may reference deleted paths).
func (m *Manager) ensureContainer(ctx context.Context, name string, spec containerSpec) error {
	ctr, err := m.client.FindContainerByName(ctx, name)
	if err == nil && ctr != nil {
		if ctr.State == container.StateRunning {
			m.log.Debug().Str("container", name).Msg("firewall container already running")
			return nil
		}

		// Stopped container — remove and recreate. Bind mounts may reference
		// deleted host paths, so restarting is not safe.
		m.log.Debug().Str("container", name).Str("state", string(ctr.State)).Msg("removing stale firewall container")
		if _, rmErr := m.client.ContainerRemove(ctx, ctr.ID, true); rmErr != nil {
			m.log.Warn().Err(rmErr).Str("container", name).Msg("failed to remove stale container, will attempt create anyway")
		}
	}

	return m.runContainer(ctx, name, spec)
}

// runContainer pulls the image if needed, creates the container, and starts it.
// On "name already in use" conflict (another process created it concurrently),
// it looks up the existing container and uses it if running.
func (m *Manager) runContainer(ctx context.Context, name string, spec containerSpec) error {
	m.log.Debug().Str("container", name).Str("image", spec.image).Msg("creating firewall container")

	if err := m.ensureImage(ctx, spec.image); err != nil {
		return fmt.Errorf("pulling image for %s: %w", name, err)
	}

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
	if len(spec.portBindings) > 0 {
		hostConfig.PortBindings = spec.portBindings
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
		// Name conflict — another process (daemon or CLI) created it concurrently.
		if strings.Contains(err.Error(), "is already in use") {
			m.log.Debug().Str("container", name).Msg("container created by another process, looking up")
			ctr, lookupErr := m.client.FindContainerByName(ctx, name)
			if lookupErr != nil {
				return fmt.Errorf("name conflict for %s but lookup failed: %w", name, lookupErr)
			}
			if ctr.State == container.StateRunning {
				return nil
			}
			_, startErr := m.client.ContainerStart(ctx, docker.ContainerStartOptions{
				ContainerID: ctr.ID,
			})
			return startErr
		}
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

// ensureImage pulls the image if it doesn't exist locally.
func (m *Manager) ensureImage(ctx context.Context, image string) error {
	if exists, _ := m.client.ImageExists(ctx, image); exists {
		return nil
	}
	m.log.Debug().Str("image", image).Msg("pulling firewall image")
	reader, err := m.client.ImagePull(ctx, image, docker.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()
	// Drain the pull output to completion.
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

// stopAndRemove stops and removes a container by name.
// Not-found errors are silently ignored.
func (m *Manager) stopAndRemove(ctx context.Context, name string) error {
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
func (m *Manager) isContainerRunning(ctx context.Context, name string) bool {
	ctr, err := m.client.FindContainerByName(ctx, name)
	if err != nil {
		return false
	}
	return ctr.State == container.StateRunning
}

// restartContainer restarts a running container by name.
func (m *Manager) restartContainer(ctx context.Context, name string) error {
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

// shellQuote wraps s in single quotes, escaping any embedded single quotes
// so the result is safe to embed in a sh -c script.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
