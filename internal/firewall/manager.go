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
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/storage"
)

// firewallContainer is a typed constant restricting container name arguments
// to the known firewall infrastructure containers.
type firewallContainer string

const (
	envoyContainer   firewallContainer = "clawker-envoy"
	corednsContainer firewallContainer = "clawker-coredns"
)

// Infrastructure container constants.
const (
	envoyImage   = "envoyproxy/envoy:distroless-v1.37.1@sha256:4d9226b9fd4d1449887de7cde785beb24b12e47d6e79021dec3c79e362609432"
	corednsImage = "coredns/coredns:1.14.2@sha256:e7e6440cfd1e919280958f5b5a6ab2b184d385bba774c12ad2a9e1e4183f90d9"

	// Health check timing.
	healthCheckTimeout  = 30 * time.Second
	healthCheckInterval = 500 * time.Millisecond
)

// Compile-time interface compliance check.
var _ FirewallManager = (*Manager)(nil)

// Manager implements FirewallManager using the Docker API.
// It manages Envoy and CoreDNS containers on the clawker-net Docker network.
type Manager struct {
	client client.APIClient
	cfg    config.Config
	log    *logger.Logger
	store  *storage.Store[EgressRulesFile]
}

// NewManager creates a new DockerFirewallManager.
func NewManager(client client.APIClient, cfg config.Config, log *logger.Logger) (*Manager, error) {
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
// Generates Envoy/CoreDNS configs from whatever rules are in the store,
// and ensures containers are running. Rule syncing is a CLI responsibility
// (call SyncRules before EnsureDaemon).
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

	// Step 3: Generate config files (envoy.yaml, Corefile, certs) from the rules store.
	dataDir, err := m.ensureConfigs(ctx)
	if err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	// Step 4: Ensure containers are running.
	if err := m.ensureContainer(ctx, envoyContainer, m.envoyContainerConfig(netInfo, dataDir)); err != nil {
		return fmt.Errorf("firewall: ensuring envoy: %w", err)
	}

	if err := m.ensureContainer(ctx, corednsContainer, m.corednsContainerConfig(netInfo, dataDir)); err != nil {
		return fmt.Errorf("firewall: ensuring coredns: %w", err)
	}

	// Step 5: Wait for services to be healthy before declaring ready.
	if err := m.WaitForHealthy(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	m.log.Debug().Msg("firewall stack running")
	return nil
}

// Stop tears down the firewall stack (containers only, not the network).
// The network is left in place because monitoring or agent containers may be attached.
func (m *Manager) Stop(ctx context.Context) error {
	envoyErr := m.stopAndRemove(ctx, envoyContainer)
	corednsErr := m.stopAndRemove(ctx, corednsContainer)

	if err := errors.Join(envoyErr, corednsErr); err != nil {
		return fmt.Errorf("firewall stop: %w", err)
	}
	return nil
}

// IsRunning reports whether both firewall containers are running.
func (m *Manager) IsRunning(ctx context.Context) bool {
	return m.isContainerRunning(ctx, envoyContainer) &&
		m.isContainerRunning(ctx, corednsContainer)
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

	envoyAddr := fmt.Sprintf("localhost:%d", m.cfg.EnvoyHealthHostPort())
	corednsURL := fmt.Sprintf("http://localhost:%d%s", m.cfg.CoreDNSHealthHostPort(), m.cfg.CoreDNSHealthPath())

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

// ProjectRules builds the full rule set from project config and required rules.
// Used by CLI code to sync rules into the store before the daemon starts.
func ProjectRules(cfg config.Config) []config.EgressRule {
	var rules []config.EgressRule
	projectFw := cfg.Project().Security.Firewall
	required := cfg.RequiredFirewallRules() // TODO: terrible way to boostrap these
	rules = append(rules, required...)
	if projectFw != nil {
		rules = append(rules, projectFw.Rules...)
		for _, d := range projectFw.AddDomains {
			rules = append(rules, config.EgressRule{Dst: d, Proto: "tls", Port: 443, Action: "allow"})
		}
	}
	return rules
}

// addRulesToStore deduplicates and writes rules to the store. Returns true if any new rules were added.
func (m *Manager) addRulesToStore(rules []config.EgressRule) (bool, error) {
	// Normalize incoming rules before the Set closure so we can do an early
	// return if nothing is new — but all store reads happen inside Set to
	// avoid TOCTOU races with concurrent CLI/daemon processes.
	var normalized []config.EgressRule
	for _, r := range rules {
		normalized = append(normalized, normalizeRule(r))
	}

	var added bool
	if err := m.store.Set(func(f *EgressRulesFile) {
		// Normalize and dedup existing rules inside the closure so we
		// operate on the authoritative COW copy, not a stale snapshot.
		existing := normalizeAndDedup(f.Rules)

		known := make(map[string]struct{}, len(existing))
		for _, r := range existing {
			known[ruleKey(r)] = struct{}{}
		}

		for _, r := range normalized {
			key := ruleKey(r)
			if _, exists := known[key]; exists {
				continue
			}
			known[key] = struct{}{}
			existing = append(existing, r)
			added = true
		}
		f.Rules = existing
	}); err != nil {
		return false, fmt.Errorf("updating rules: %w", err)
	}

	if !added {
		return false, nil
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
		removeSet[ruleKey(normalizeRule(r))] = struct{}{}
	}

	var changed bool
	if err := m.store.Set(func(f *EgressRulesFile) {
		// Normalize and dedup inside the closure to operate on the
		// authoritative COW copy, not a stale snapshot.
		normalized := normalizeAndDedup(f.Rules)
		filtered := make([]config.EgressRule, 0, len(normalized))
		for _, r := range normalized {
			if _, remove := removeSet[ruleKey(r)]; !remove {
				filtered = append(filtered, r)
			}
		}
		changed = len(filtered) != len(normalized)
		f.Rules = filtered
	}); err != nil {
		return fmt.Errorf("removing rules: %w", err)
	}

	if !changed {
		return nil
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
	return normalizeAndDedup(m.store.Read().Rules), nil
}

// Disable disconnects a container from the firewall network, blocking all egress
// through the firewall stack.
func (m *Manager) Disable(ctx context.Context, containerID string) error {
	execResp, err := m.client.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		User:         "root",
		AttachStderr: true,
		Cmd:          []string{"/usr/local/bin/firewall.sh", "disable"},
	})
	if err != nil {
		return fmt.Errorf("firewall disable: creating exec: %w", err)
	}

	hijack, err := m.client.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("firewall disable: attaching: %w", err)
	}
	defer hijack.Conn.Close()

	// Read all output (blocks until exec completes).
	output, _ := io.ReadAll(hijack.Reader)

	inspectExec, err := m.client.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
	if err != nil {
		return fmt.Errorf("firewall disable: inspecting exec: %w", err)
	}
	if inspectExec.ExitCode != 0 {
		return fmt.Errorf("firewall disable: script exited %d: %s", inspectExec.ExitCode, strings.TrimSpace(string(output)))
	}

	m.log.Debug().
		Str("container", containerID).
		Msg("firewall disabled")

	return nil
}

// Enable re-applies DNAT + firewall DNS in an agent container via docker exec.
// Reads the current rules from the store to compute TCP port mappings.
func (m *Manager) Enable(ctx context.Context, containerID string) error {
	netInfo, err := m.discoverNetwork(ctx)
	if err != nil {
		return fmt.Errorf("firewall enable: discovering network: %w", err)
	}

	// Best-effort: read host proxy URL from container env.
	var hostProxy string
	inspect, inspectErr := m.client.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if inspectErr == nil {
		for _, env := range inspect.Container.Config.Env {
			if val, ok := strings.CutPrefix(env, "CLAWKER_HOST_PROXY="); ok {
				hostProxy = val
				break
			}
		}
	}

	// Compute TCP port mappings from the current rule state.
	tcpMappingsArg := m.formatPortMappings()

	args := []string{"/usr/local/bin/firewall.sh", "enable",
		netInfo.EnvoyIP, netInfo.CoreDNSIP, netInfo.CIDR}
	if hostProxy != "" {
		args = append(args, hostProxy)
	} else {
		args = append(args, "") // placeholder so tcp_mappings lands in $5
	}
	if tcpMappingsArg != "" {
		args = append(args, tcpMappingsArg)
	}

	execResp, err := m.client.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		User:         "root",
		AttachStderr: true,
		Cmd:          args,
	})
	if err != nil {
		return fmt.Errorf("firewall enable: creating exec: %w", err)
	}

	hijack, err := m.client.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("firewall enable: attaching: %w", err)
	}
	defer hijack.Conn.Close()

	// Read all output (blocks until exec completes).
	output, _ := io.ReadAll(hijack.Reader)

	inspectExec, err := m.client.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
	if err != nil {
		return fmt.Errorf("firewall enable: inspecting exec: %w", err)
	}
	if inspectExec.ExitCode != 0 {
		return fmt.Errorf("firewall enable: script exited %d: %s", inspectExec.ExitCode, strings.TrimSpace(string(output)))
	}

	// Signal the entrypoint that firewall is ready (unblocks CMD).
	if err := m.touchSignalFile(ctx, containerID); err != nil {
		m.log.Warn().Err(err).Msg("failed to touch firewall-ready signal file")
	}

	m.log.Debug().
		Str("container", containerID).
		Msg("firewall enabled")

	return nil
}

// firewallReadyPath is the signal file the entrypoint waits for before running CMD.
const firewallReadyPath = "/var/run/clawker/firewall-ready"

// touchSignalFile creates the firewall-ready signal file inside the container,
// unblocking the entrypoint's wait loop.
func (m *Manager) touchSignalFile(ctx context.Context, containerID string) error {
	execResp, err := m.client.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		User: "root",
		Cmd:  []string{"touch", firewallReadyPath},
	})
	if err != nil {
		return err
	}
	_, err = m.client.ExecStart(ctx, execResp.ID, client.ExecStartOptions{})
	return err
}

// Bypass disables iptables rules and schedules re-enable after timeout.
// Uses detached docker exec: sleep <timeout> && firewall.sh enable <args>.
func (m *Manager) Bypass(ctx context.Context, containerID string, timeout time.Duration) error {
	// Step 1: Disable firewall (flush iptables rules).
	if err := m.Disable(ctx, containerID); err != nil {
		return fmt.Errorf("firewall bypass: %w", err)
	}

	// Step 2: Schedule re-enable via detached exec inside the container.
	netInfo, err := m.discoverNetwork(ctx)
	if err != nil {
		return fmt.Errorf("firewall bypass: discovering network: %w", err)
	}

	// Best-effort: read host proxy URL from container env.
	var hostProxy string
	inspect, inspectErr := m.client.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if inspectErr == nil {
		for _, env := range inspect.Container.Config.Env {
			if val, ok := strings.CutPrefix(env, "CLAWKER_HOST_PROXY="); ok {
				hostProxy = val
				break
			}
		}
	}

	timeoutSecs := int(timeout.Seconds())
	if timeoutSecs <= 0 {
		timeoutSecs = 30
	}

	// Build the re-enable command: sleep <timeout> && firewall.sh enable <args>
	tcpMappingsArg := m.formatPortMappings()
	hostProxyArg := hostProxy
	if hostProxyArg == "" && tcpMappingsArg != "" {
		hostProxyArg = "''" // placeholder so tcp_mappings lands in $5
	}
	enableCmd := fmt.Sprintf("/usr/local/bin/firewall.sh enable %s %s %s",
		netInfo.EnvoyIP, netInfo.CoreDNSIP, netInfo.CIDR)
	if hostProxyArg != "" {
		enableCmd += " " + hostProxyArg
	}
	if tcpMappingsArg != "" {
		enableCmd += " '" + tcpMappingsArg + "'"
	}
	shellCmd := fmt.Sprintf("sleep %d && %s", timeoutSecs, enableCmd)

	execResp, err := m.client.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		User: "root",
		Cmd:  []string{"sh", "-c", shellCmd},
	})
	if err != nil {
		return fmt.Errorf("firewall bypass: creating timer exec: %w", err)
	}

	_, err = m.client.ExecStart(ctx, execResp.ID, client.ExecStartOptions{
		Detach: true,
	})
	if err != nil {
		return fmt.Errorf("firewall bypass: starting timer: %w", err)
	}

	m.log.Debug().
		Str("container", containerID).
		Int("timeout_secs", timeoutSecs).
		Msg("firewall bypass started (re-enable scheduled)")

	return nil
}

// Status returns a health snapshot of the firewall stack.
func (m *Manager) Status(ctx context.Context) (*FirewallStatus, error) {
	rules := normalizeAndDedup(m.store.Read().Rules)

	// Discover network state from Docker (best-effort; fields stay empty if network doesn't exist).
	netInfo, _ := m.discoverNetwork(ctx)

	envoyRunning := m.isContainerRunning(ctx, envoyContainer)
	corednsRunning := m.isContainerRunning(ctx, corednsContainer)

	status := &FirewallStatus{
		Running:       envoyRunning && corednsRunning,
		EnvoyHealth:   envoyRunning,
		CoreDNSHealth: corednsRunning,
		RuleCount:     len(rules),
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

// formatPortMappings reads rules from the store, computes TCP and HTTP port mappings,
// and returns the firewall.sh argument string (format: "dst_port|envoy_port;...").
// HTTP mappings are appended after TCP mappings — all HTTP ports redirect to the
// single HTTP listener, while TCP ports get per-rule dedicated listeners.
func (m *Manager) formatPortMappings() string {
	rules := normalizeAndDedup(m.store.Read().Rules)
	ports := m.envoyPorts()
	tcpMappings := TCPMappings(rules, ports)
	httpMappings := HTTPMappings(rules, ports.HTTPPort)
	allMappings := append(tcpMappings, httpMappings...)
	if len(allMappings) == 0 {
		return ""
	}
	var parts []string
	for _, mp := range allMappings {
		parts = append(parts, fmt.Sprintf("%d|%d", mp.DstPort, mp.EnvoyPort))
	}
	return strings.Join(parts, ";")
}

// envoyPorts returns the EnvoyPorts config from the manager's config.
func (m *Manager) envoyPorts() EnvoyPorts {
	return EnvoyPorts{
		TLSPort:     m.cfg.EnvoyTLSPort(),
		TCPPortBase: m.cfg.EnvoyTCPPortBase(),
		HTTPPort:    m.cfg.EnvoyHTTPPort(),
	}
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

	// Resolve cert directory via config accessor.
	certDir, err := m.cfg.FirewallCertSubdir()
	if err != nil {
		return "", fmt.Errorf("resolving firewall cert dir: %w", err)
	}

	// Ensure certs exist (CA + domain certs for MITM rules).
	caCert, caKey, err := EnsureCA(certDir)
	if err != nil {
		return "", fmt.Errorf("ensuring CA: %w", err)
	}

	// Normalize, dedup, and heal the store inside the Set closure to avoid
	// TOCTOU races with concurrent CLI/daemon processes. Legacy files may
	// contain port:0 or empty proto/action rules that need normalization.
	var allRules []config.EgressRule
	var healed bool
	if err := m.store.Set(func(f *EgressRulesFile) {
		allRules = normalizeAndDedup(f.Rules)
		// Dirty if count changed (dedup) or any normalizable field differs.
		if len(allRules) != len(f.Rules) {
			healed = true
		} else {
			for i, r := range f.Rules {
				n := allRules[i]
				if r.Proto != n.Proto || r.Action != n.Action || r.Port != n.Port {
					healed = true
					break
				}
			}
		}
		if healed {
			f.Rules = allRules
		}
	}); err != nil {
		return "", fmt.Errorf("healing rules store: %w", err)
	}
	if healed {
		if err := m.store.Write(); err != nil {
			return "", fmt.Errorf("writing healed rules: %w", err)
		}
		m.log.Info().Int("rules", len(allRules)).Msg("healed legacy rules in store")
	}

	// Regenerate domain certs for any MITM rules.
	if err := RegenerateDomainCerts(allRules, certDir, caCert, caKey); err != nil {
		return "", fmt.Errorf("regenerating domain certs: %w", err)
	}

	// Generate Envoy config.
	envoyYAML, warnings, err := GenerateEnvoyConfig(allRules, m.envoyPorts())
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
	corefile, err := GenerateCorefile(allRules, m.cfg.CoreDNSHealthHostPort())
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

	// Only restart containers if they're running. If not, the daemon will
	// start them with the fresh configs when it brings the stack up.
	if !m.IsRunning(ctx) {
		return nil
	}

	if err := m.restartContainer(ctx, envoyContainer); err != nil {
		return err
	}
	if err := m.restartContainer(ctx, corednsContainer); err != nil {
		return err
	}

	return m.WaitForHealthy(ctx)
}

// envoyContainerConfig returns the container creation config for the Envoy proxy.
// dataDir must be pre-validated (ensureConfigs checks FirewallDataSubdir before this is called).
func (m *Manager) envoyContainerConfig(net *NetworkInfo, dataDir string) containerSpec {
	certDir, _ := m.cfg.FirewallCertSubdir() // already validated in ensureConfigs
	return containerSpec{
		image:       envoyImage,
		networkName: m.cfg.ClawkerNetwork(),
		networkID:   net.NetworkID,
		staticIP:    net.EnvoyIP,
		labels: map[string]string{
			m.cfg.LabelManaged(): "true",
			m.cfg.LabelPurpose(): m.cfg.PurposeFirewall(),
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
				Source:   certDir,
				Target:   "/etc/envoy/certs",
				ReadOnly: true,
			},
		},
		// Publish TLS listener for host-side health probes (TCP connect).
		portBindings: network.PortMap{
			network.MustParsePort(fmt.Sprintf("%d/tcp", m.cfg.EnvoyTLSPort())): {
				{HostPort: strconv.Itoa(m.cfg.EnvoyHealthHostPort())},
			},
		},
	}
}

// corednsContainerConfig returns the container creation config for the CoreDNS resolver.
// dataDir must be pre-validated (ensureConfigs checks FirewallDataSubdir before this is called).
func (m *Manager) corednsContainerConfig(net *NetworkInfo, dataDir string) containerSpec {
	healthPort := m.cfg.CoreDNSHealthHostPort()
	return containerSpec{
		image:       corednsImage,
		networkName: m.cfg.ClawkerNetwork(),
		networkID:   net.NetworkID,
		staticIP:    net.CoreDNSIP,
		labels: map[string]string{
			m.cfg.LabelManaged(): "true",
			m.cfg.LabelPurpose(): m.cfg.PurposeFirewall(),
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
			network.MustParsePort(fmt.Sprintf("%d/tcp", healthPort)): {
				{HostPort: strconv.Itoa(healthPort)},
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
// If a stopped container exists, it is started rather than recreated —
// firewall containers are long-lived and only need recreation on config/image changes.
// Cross-process safe: handles name conflicts (daemon vs CLI race).
func (m *Manager) ensureContainer(ctx context.Context, name firewallContainer, spec containerSpec) error {
	n := string(name)
	filters := client.Filters{}.Add("name", n)
	listResp, err := m.client.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: filters,
	})
	if err != nil {
		return fmt.Errorf("listing containers for %s: %w", n, err)
	}

	if len(listResp.Items) == 0 {
		m.log.Debug().Str("container", n).Msg("container not found, creating")
		return m.runContainer(ctx, name, spec)
	}

	ctr := listResp.Items[0]
	if ctr.State == container.StateRunning {
		m.log.Debug().Str("container", n).Msg("firewall container already running")
		return nil
	}

	// Container exists but is stopped/created — just start it.
	m.log.Debug().Str("container", n).Str("state", string(ctr.State)).Msg("starting existing firewall container")
	_, startErr := m.client.ContainerStart(ctx, ctr.ID, client.ContainerStartOptions{})
	if startErr != nil {
		return fmt.Errorf("starting existing container %s: %w", n, startErr)
	}
	return nil
}

// runContainer pulls the image if needed, creates the container, and starts it.
// On "name already in use" conflict (another process created it concurrently),
// it looks up the existing container and uses it if running.
func (m *Manager) runContainer(ctx context.Context, name firewallContainer, spec containerSpec) error {
	n := string(name)
	m.log.Debug().Str("container", n).Str("image", spec.image).Msg("creating firewall container")

	if err := m.ensureImage(ctx, spec.image); err != nil {
		return fmt.Errorf("pulling image for %s: %w", n, err)
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

	createResult, err := m.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:           containerConfig,
		HostConfig:       hostConfig,
		NetworkingConfig: networkingConfig,
		Name:             n,
	})
	if err != nil {
		// Name conflict — another process (daemon or CLI) created it concurrently.
		if strings.Contains(err.Error(), "is already in use") {
			m.log.Debug().Str("container", n).Msg("container created by another process, looking up")
			filters := client.Filters{}.Add("name", n)
			listResp, lookupErr := m.client.ContainerList(ctx, client.ContainerListOptions{
				All:     true,
				Filters: filters,
			})
			if lookupErr != nil || len(listResp.Items) == 0 {
				return fmt.Errorf("name conflict for %s but lookup failed: %w", n, lookupErr)
			}
			ctr := listResp.Items[0]
			if ctr.State == container.StateRunning {
				return nil
			}
			_, startErr := m.client.ContainerStart(ctx, ctr.ID, client.ContainerStartOptions{})
			return startErr
		}
		return fmt.Errorf("creating container %s: %w", n, err)
	}

	_, err = m.client.ContainerStart(ctx, createResult.ID, client.ContainerStartOptions{})
	if err != nil {
		return fmt.Errorf("starting container %s: %w", n, err)
	}

	return nil
}

// ensureImage pulls the image if it doesn't exist locally.
func (m *Manager) ensureImage(ctx context.Context, image string) error {
	_, err := m.client.ImageInspect(ctx, image)
	if err == nil {
		return nil // image exists locally
	}
	m.log.Debug().Str("image", image).Msg("pulling firewall image")
	reader, err := m.client.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()
	// Drain the pull output to completion.
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

// stopAndRemove stops and removes a firewall container.
// Not-found is silently ignored.
func (m *Manager) stopAndRemove(ctx context.Context, name firewallContainer) error {
	n := string(name)
	filters := client.Filters{}.Add("name", n)
	result, err := m.client.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: filters,
	})
	if err != nil {
		m.log.Debug().Str("container", n).Err(err).Msg("container lookup failed, skipping stop")
		return nil
	}
	if len(result.Items) == 0 {
		m.log.Debug().Str("container", n).Msg("container not found, skipping stop")
		return nil
	}

	ctr := result.Items[0]
	if ctr.State == container.StateRunning {
		timeout := 10
		if _, err := m.client.ContainerStop(ctx, ctr.ID, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
			m.log.Warn().Err(err).Str("container", n).Msg("failed to stop container gracefully")
		}
	}

	_, err = m.client.ContainerRemove(ctx, ctr.ID, client.ContainerRemoveOptions{Force: true})
	if err != nil {
		return fmt.Errorf("removing container %s: %w", n, err)
	}

	m.log.Debug().Str("container", n).Msg("container removed")
	return nil
}

// isContainerRunning checks whether a firewall container is running.
func (m *Manager) isContainerRunning(ctx context.Context, name firewallContainer) bool {
	n := string(name)
	filters := client.Filters{}.Add("name", n)
	result, err := m.client.ContainerList(ctx, client.ContainerListOptions{
		Filters: filters, // default: running only
	})
	if err != nil {
		return false
	}
	return len(result.Items) > 0
}

// restartContainer restarts a firewall container.
func (m *Manager) restartContainer(ctx context.Context, name firewallContainer) error {
	n := string(name)
	filters := client.Filters{}.Add("name", n)
	result, err := m.client.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: filters,
	})
	if err != nil || len(result.Items) == 0 {
		return fmt.Errorf("finding container %s: %w", n, err)
	}

	timeout := 10
	_, err = m.client.ContainerRestart(ctx, result.Items[0].ID, client.ContainerRestartOptions{Timeout: &timeout})
	if err != nil {
		return fmt.Errorf("restarting container %s: %w", n, err)
	}

	return nil
}
