package firewall

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
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

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/config"
	clawkerebpf "github.com/schmitthub/clawker/internal/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/storage"
)

// firewallContainer is a typed constant restricting container name arguments
// to the known firewall infrastructure containers.
type firewallContainer string

const (
	envoyContainer   firewallContainer = "clawker-envoy"
	corednsContainer firewallContainer = "clawker-coredns"
	ebpfContainer    firewallContainer = "clawker-ebpf"
)

// Infrastructure container constants.
const (
	envoyImage = "envoyproxy/envoy:distroless-v1.37.1@sha256:4d9226b9fd4d1449887de7cde785beb24b12e47d6e79021dec3c79e362609432"

	// ebpfImageTag is the local Docker image tag for the eBPF manager.
	// Built on-demand from the embedded binary when the image doesn't exist locally.
	ebpfImageTag = "clawker-ebpf:latest"

	// corednsImageTag is the local Docker image tag for the custom CoreDNS
	// with the dnsbpf plugin. Built on-demand from the embedded binary.
	corednsImageTag = "clawker-coredns:latest"

	// Health check timing.
	healthCheckTimeout  = 60 * time.Second
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

	// Step 4: Ensure locally-built images exist (build from embedded binaries if needed).
	if err := m.ensureEbpfImage(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	if err := m.ensureCorednsImage(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	// Step 5: Start eBPF container and initialize BPF programs.
	// This MUST happen before CoreDNS starts because the dnsbpf plugin opens
	// the pinned dns_cache map on startup — the map must already exist.
	if err := m.ensureContainer(ctx, ebpfContainer, m.ebpfContainerConfig(netInfo)); err != nil {
		return fmt.Errorf("firewall: ensuring ebpf manager: %w", err)
	}
	if err := m.ebpfExec(ctx, "init"); err != nil {
		return fmt.Errorf("firewall: initializing eBPF programs: %w", err)
	}
	if err := m.syncRoutes(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	// Step 6: Start remaining containers (Envoy + CoreDNS).
	if err := m.ensureContainer(ctx, envoyContainer, m.envoyContainerConfig(netInfo, dataDir)); err != nil {
		return fmt.Errorf("firewall: ensuring envoy: %w", err)
	}
	if err := m.ensureContainer(ctx, corednsContainer, m.corednsContainerConfig(netInfo, dataDir)); err != nil {
		return fmt.Errorf("firewall: ensuring coredns: %w", err)
	}

	// Step 7: Wait for services to be healthy before declaring ready.
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
	ebpfErr := m.stopAndRemove(ctx, ebpfContainer)

	if err := errors.Join(envoyErr, corednsErr, ebpfErr); err != nil {
		return fmt.Errorf("firewall stop: %w", err)
	}
	return nil
}

// IsRunning reports whether all firewall containers are running.
func (m *Manager) IsRunning(ctx context.Context) bool {
	return m.isContainerRunning(ctx, envoyContainer) &&
		m.isContainerRunning(ctx, corednsContainer) &&
		m.isContainerRunning(ctx, ebpfContainer)
}

// WaitForHealthy polls until both firewall services pass health probes (TCP+HTTP)
// or the context expires. Probes go through published localhost ports so they work
// from the host (macOS Docker Desktop doesn't route to container IPs).
//
//   - Envoy: HTTP GET to localhost:18901/ (published dedicated health listener).
//   - CoreDNS: HTTP GET to localhost:18902/health (published health endpoint).
func (m *Manager) WaitForHealthy(ctx context.Context) error {
	// Fast-path: if context is already done, return immediately
	// rather than falling through to HealthTimeoutError with a
	// misleading negative timeout duration.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Respect caller's context deadline; fall back to
	// healthCheckTimeout when no deadline is set.
	timeout := healthCheckTimeout
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
		if timeout <= 0 {
			return context.DeadlineExceeded
		}
		deadline = dl
	}
	httpClient := &http.Client{Timeout: 2 * time.Second}

	envoyURL := fmt.Sprintf("http://localhost:%d/", m.cfg.EnvoyHealthHostPort())
	corednsURL := fmt.Sprintf("http://localhost:%d%s", m.cfg.CoreDNSHealthHostPort(), m.cfg.CoreDNSHealthPath())

	var envoyReady, corednsReady bool
	var lastEnvoyErr, lastCorednsErr error

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if !envoyReady {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, envoyURL, nil)
			if err != nil {
				lastEnvoyErr = err
			} else if resp, err := httpClient.Do(req); err != nil {
				lastEnvoyErr = err
			} else {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					envoyReady = true
					m.log.Debug().Msg("envoy health check passed (HTTP 200)")
				} else {
					lastEnvoyErr = fmt.Errorf("HTTP %d", resp.StatusCode)
				}
			}
		}

		if !corednsReady {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, corednsURL, nil)
			if err != nil {
				lastCorednsErr = err
			} else if resp, err := httpClient.Do(req); err != nil {
				lastCorednsErr = err
			} else {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					corednsReady = true
					m.log.Debug().Msg("coredns health check passed (HTTP 200)")
				} else {
					lastCorednsErr = fmt.Errorf("HTTP %d", resp.StatusCode)
				}
			}
		}

		if envoyReady && corednsReady {
			return nil
		}

		time.Sleep(healthCheckInterval)
	}

	// Prefer context error (e.g., user Ctrl+C) over timeout.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	var unhealthy error
	if !envoyReady {
		envoyErr := ErrEnvoyUnhealthy
		if lastEnvoyErr != nil {
			envoyErr = fmt.Errorf("%w: last probe: %v", ErrEnvoyUnhealthy, lastEnvoyErr)
		}
		unhealthy = errors.Join(unhealthy, envoyErr)
	}
	if !corednsReady {
		corednsErr := ErrCoreDNSUnhealthy
		if lastCorednsErr != nil {
			corednsErr = fmt.Errorf("%w: last probe: %v", ErrCoreDNSUnhealthy, lastCorednsErr)
		}
		unhealthy = errors.Join(unhealthy, corednsErr)
	}
	return &HealthTimeoutError{Timeout: timeout, Err: unhealthy}
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

// addRulesToStore validates, deduplicates, and writes rules to the store.
// Returns true if any new rules were added. Validation is all-or-nothing:
// a single invalid destination aborts the entire batch before any store
// mutation, so invalid input never persists.
func (m *Manager) addRulesToStore(rules []config.EgressRule) (bool, error) {
	// Validate all destinations before touching the store.
	for _, r := range rules {
		if err := ValidateDst(r.Dst); err != nil {
			return false, err
		}
	}

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
		existing, _ := normalizeAndDedup(f.Rules)

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
		normalized, _ := normalizeAndDedup(f.Rules)
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
	rules, warnings := normalizeAndDedup(m.store.Read().Rules)
	for _, w := range warnings {
		m.log.Warn().Msg(w)
	}
	return rules, nil
}

// Disable detaches eBPF programs from a container's cgroup, removing all
// traffic routing. The container gets unrestricted egress.
func (m *Manager) Disable(ctx context.Context, containerID string) error {
	cgroupPath := ebpfCgroupPath(m.cgroupDriver(ctx), containerID)
	if err := m.ebpfExec(ctx, "disable", cgroupPath); err != nil {
		return fmt.Errorf("firewall disable: %w", err)
	}

	m.log.Debug().Str("container", containerID).Msg("firewall disabled via eBPF")
	return nil
}

// Enable attaches eBPF programs to a container's cgroup, routing traffic
// through Envoy (TCP) and CoreDNS (DNS). Replaces iptables DNAT rules.
func (m *Manager) Enable(ctx context.Context, containerID string) error {
	// Ensure the eBPF manager container is running (idempotent).
	// The daemon's EnsureRunning creates it, but it may be missing if the daemon
	// started before the eBPF feature was added, or if it was manually removed.
	if err := m.ensureEbpfImage(ctx); err != nil {
		return fmt.Errorf("firewall enable: %w", err)
	}

	netInfo, err := m.discoverNetwork(ctx)
	if err != nil {
		return fmt.Errorf("firewall enable: discovering network: %w", err)
	}

	if err := m.ensureContainer(ctx, ebpfContainer, m.ebpfContainerConfig(netInfo)); err != nil {
		return fmt.Errorf("firewall enable: ensuring ebpf container: %w", err)
	}

	// DO NOT call ebpfExec("init") here — that would re-run Load() which
	// calls cleanupAllLinks() and detaches BPF programs from ALL other
	// running containers. Programs are loaded by EnsureRunning at daemon
	// startup and pinned to the host bpffs; they persist for the daemon
	// lifetime. No re-init is needed for per-container enable.

	// Sync global routes (idempotent — safe to call on every Enable).
	if err := m.syncRoutes(ctx); err != nil {
		return fmt.Errorf("firewall enable: %w", err)
	}

	// Host proxy bypass: read from project config (authoritative), not container env.
	var hostProxyIP string
	var hostProxyPort uint16
	if m.cfg.Project().Security.HostProxyEnabled() {
		hostProxyPort = uint16(m.cfg.Settings().HostProxy.Daemon.Port)
		// Resolve host.docker.internal from inside the Docker network.
		// On Docker Desktop it resolves to 192.168.65.254, not localhost.
		if resolved, err := m.ebpfExecOutput(ctx, "resolve", "host.docker.internal"); err == nil {
			hostProxyIP = strings.TrimSpace(resolved)
		} else {
			m.log.Warn().Err(err).Msg("could not resolve host.docker.internal, host proxy bypass disabled")
		}
	}

	// Compute gateway IP from CIDR.
	gatewayIP := computeGateway(netInfo.CIDR)
	ports := m.envoyPorts()

	// Build JSON config for the eBPF manager (container_map entry only, no routes).
	cfgJSON := fmt.Sprintf(
		`{"envoy_ip":%q,"coredns_ip":%q,"gateway_ip":%q,"cidr":%q,"host_proxy_ip":%q,"host_proxy_port":%d,"egress_port":%d}`,
		netInfo.EnvoyIP, netInfo.CoreDNSIP, gatewayIP, netInfo.CIDR,
		hostProxyIP, hostProxyPort, ports.EgressPort,
	)

	cgroupPath := ebpfCgroupPath(m.cgroupDriver(ctx), containerID)
	if err := m.ebpfExec(ctx, "enable", cgroupPath, cfgJSON); err != nil {
		return fmt.Errorf("firewall enable: %w", err)
	}

	// Signal the entrypoint that firewall is ready (unblocks CMD).
	if err := m.touchSignalFile(ctx, containerID); err != nil {
		m.log.Warn().Err(err).Msg("failed to touch firewall-ready signal file")
	}

	m.log.Debug().Str("container", containerID).Msg("firewall enabled via eBPF")
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

// emitAgentMapping was deleted — eBPF metrics replace the Loki agent_map pipeline.
// See .claude/docs/EBPF-DESIGN.md § Observability.

// Bypass sets the eBPF bypass flag for a container, allowing unrestricted egress.
// Schedules automatic re-enable after timeout via a detached exec in the eBPF
// manager container (sleep + unbypass).
func (m *Manager) Bypass(ctx context.Context, containerID string, timeout time.Duration) error {
	cgroupPath := ebpfCgroupPath(m.cgroupDriver(ctx), containerID)

	// Set bypass flag (instant, atomic).
	if err := m.ebpfExec(ctx, "bypass", cgroupPath); err != nil {
		return fmt.Errorf("firewall bypass: %w", err)
	}

	// Schedule automatic re-enable via detached exec in the eBPF manager container.
	timeoutSecs := int(timeout.Seconds())
	if timeoutSecs <= 0 {
		timeoutSecs = 30
	}

	shellCmd := fmt.Sprintf("sleep %d && /usr/local/bin/ebpf-manager unbypass %s", timeoutSecs, cgroupPath)
	execResp, err := m.client.ExecCreate(ctx, string(ebpfContainer), client.ExecCreateOptions{
		Cmd: []string{"sh", "-c", shellCmd},
	})
	if err != nil {
		return fmt.Errorf("firewall bypass: creating timer: %w", err)
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
		Msg("firewall bypass started (re-enable scheduled via eBPF)")

	return nil
}

// Status returns a health snapshot of the firewall stack.
func (m *Manager) Status(ctx context.Context) (*FirewallStatus, error) {
	rules, _ := normalizeAndDedup(m.store.Read().Rules)

	// Discover network state from Docker (best-effort; fields stay empty if network doesn't exist).
	netInfo, _ := m.discoverNetwork(ctx)

	envoyRunning := m.isContainerRunning(ctx, envoyContainer)
	corednsRunning := m.isContainerRunning(ctx, corednsContainer)
	ebpfRunning := m.isContainerRunning(ctx, ebpfContainer)

	status := &FirewallStatus{
		Running:       envoyRunning && corednsRunning && ebpfRunning,
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

// envoyPorts returns the EnvoyPorts config from the manager's config.
func (m *Manager) envoyPorts() EnvoyPorts {
	return EnvoyPorts{
		EgressPort:  m.cfg.EnvoyEgressPort(),
		TCPPortBase: m.cfg.EnvoyTCPPortBase(),
		HealthPort:  m.cfg.EnvoyHealthPort(),
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

	// Ensure certs exist (CA + domain certs for TLS rules).
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
		var ruleWarnings []string
		allRules, ruleWarnings = normalizeAndDedup(f.Rules)
		for _, w := range ruleWarnings {
			m.log.Warn().Msg(w)
		}
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

	// Regenerate domain certs for TLS rules.
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

	// DO NOT call ebpfExec("init") here — that would re-run Load() which
	// calls cleanupAllLinks() and detaches BPF programs from ALL running
	// containers. Maps are already pinned from the initial EnsureRunning,
	// so we just need to sync routes for the rule change.
	if err := m.syncRoutes(ctx); err != nil {
		return err
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
		// Publish the dedicated health listener — NOT the TLS port.
		// Publishing the TLS port causes Docker to insert NAT rules for it,
		// which masquerades the source IP of DNAT'd inter-container traffic.
		portBindings: network.PortMap{
			network.MustParsePort(fmt.Sprintf("%d/tcp", m.cfg.EnvoyHealthPort())): {
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
		image:       corednsImageTag,
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
			{
				// BPF filesystem: the dnsbpf plugin writes to the pinned
				// dns_cache map at /sys/fs/bpf/clawker/dns_cache.
				Type:   mount.TypeBind,
				Source: "/sys/fs/bpf",
				Target: "/sys/fs/bpf",
			},
		},
		// Publish health endpoint for host-side health probes.
		portBindings: network.PortMap{
			network.MustParsePort(fmt.Sprintf("%d/tcp", healthPort)): {
				{HostPort: strconv.Itoa(healthPort)},
			},
		},
		// CAP_BPF + CAP_SYS_ADMIN: CAP_BPF is required for bpf(BPF_OBJ_GET) to open
		// the pinned dns_cache map. CAP_SYS_ADMIN is needed for bpf(BPF_MAP_UPDATE_ELEM)
		// on kernels < 5.19 where CAP_BPF alone is insufficient for map writes.
		capAdd: []string{"BPF", "SYS_ADMIN"},
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
	capAdd       []string // Linux capabilities (e.g., "BPF", "SYS_ADMIN")
	stopTimeout  *int     // Seconds to wait for SIGTERM before SIGKILL (nil = Docker default 10s)
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
	if spec.stopTimeout != nil {
		containerConfig.StopTimeout = spec.stopTimeout
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
	if len(spec.capAdd) > 0 {
		hostConfig.CapAdd = spec.capAdd
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

// embeddedImageSpec describes an embedded binary that can be built into a
// Docker image on-demand. Both the eBPF manager and custom CoreDNS images
// use this same build-from-embedded-binary pattern.
type embeddedImageSpec struct {
	tag        string // Docker image tag (e.g. "clawker-ebpf:latest")
	binary     []byte // Embedded binary bytes
	binaryName string // Filename inside the tar context (e.g. "ebpf-manager")
	dockerfile string // Dockerfile content
	makeTarget string // Make target for building the binary (used in error messages)
}

var ebpfImageSpec = embeddedImageSpec{
	tag:        ebpfImageTag,
	binary:     nil, // populated per-call in ensureEbpfImage to avoid copying the large []byte
	binaryName: "ebpf-manager",
	dockerfile: "FROM alpine:3.21@sha256:a8560b36e8b8210634f77d9f7f9efd7ffa463e380b75e2e74aff4511df3ef88c\nRUN apk add --no-cache iproute2\nCOPY ebpf-manager /usr/local/bin/ebpf-manager\nCMD [\"sleep\", \"infinity\"]\n",
	makeTarget: "ebpf-binary",
}

var corednsImageSpec = embeddedImageSpec{
	tag:        corednsImageTag,
	binary:     nil, // populated per-call in ensureCorednsImage to avoid copying the large []byte
	binaryName: "coredns",
	dockerfile: "FROM alpine:3.21@sha256:a8560b36e8b8210634f77d9f7f9efd7ffa463e380b75e2e74aff4511df3ef88c\nCOPY coredns /usr/local/bin/coredns\nENTRYPOINT [\"/usr/local/bin/coredns\"]\n",
	makeTarget: "coredns-binary",
}

// ensureEbpfImage ensures the eBPF manager Docker image exists locally.
// Builds it from the embedded binary if it doesn't exist.
func (m *Manager) ensureEbpfImage(ctx context.Context) error {
	spec := ebpfImageSpec
	spec.binary = ebpfManagerBinary
	return m.ensureEmbeddedImage(ctx, spec)
}

// ensureCorednsImage ensures the custom CoreDNS image exists locally.
// Builds it from the embedded binary if it doesn't exist.
func (m *Manager) ensureCorednsImage(ctx context.Context) error {
	spec := corednsImageSpec
	spec.binary = corednsClawkerBinary
	return m.ensureEmbeddedImage(ctx, spec)
}

// ensureEmbeddedImage checks if a locally-built image exists and builds it
// from the embedded binary if not. Used for both eBPF manager and CoreDNS.
func (m *Manager) ensureEmbeddedImage(ctx context.Context, spec embeddedImageSpec) error {
	_, err := m.client.ImageInspect(ctx, spec.tag)
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("checking %s image: %w", spec.tag, err)
	}

	if len(spec.binary) == 0 {
		return fmt.Errorf("%s binary not embedded — run 'make %s' then rebuild clawker",
			spec.binaryName, spec.makeTarget)
	}

	m.log.Debug().Str("image", spec.tag).Msg("building image from embedded binary")

	buildCtx, err := embeddedBuildContext(spec)
	if err != nil {
		return fmt.Errorf("creating %s build context: %w", spec.binaryName, err)
	}

	resp, err := m.client.ImageBuild(ctx, buildCtx, client.ImageBuildOptions{
		Tags:           []string{spec.tag},
		Dockerfile:     "Dockerfile",
		Remove:         true,
		ForceRemove:    true,
		SuppressOutput: true,
	})
	if err != nil {
		return fmt.Errorf("building %s image: %w", spec.tag, err)
	}
	defer resp.Body.Close()

	// Drain the build output and check for build-time errors embedded in the
	// JSON stream (Dockerfile RUN failures, COPY errors, base image pull failures).
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			break // malformed JSON — build likely succeeded, don't mask it
		}
		if msg.Error != "" {
			return fmt.Errorf("building %s image: %s", spec.tag, msg.Error)
		}
	}
	return nil
}

// embeddedBuildContext creates an in-memory tar archive containing a Dockerfile
// and the embedded binary, suitable for Docker ImageBuild.
func embeddedBuildContext(spec embeddedImageSpec) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	if err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Size: int64(len(spec.dockerfile)),
		Mode: 0644,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(spec.dockerfile)); err != nil {
		return nil, err
	}

	if err := tw.WriteHeader(&tar.Header{
		Name: spec.binaryName,
		Size: int64(len(spec.binary)),
		Mode: 0755,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(spec.binary); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
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
	if err != nil {
		return fmt.Errorf("finding container %s: %w", n, err)
	}
	if len(result.Items) == 0 {
		return fmt.Errorf("container %s not found", n)
	}

	timeout := 10
	_, err = m.client.ContainerRestart(ctx, result.Items[0].ID, client.ContainerRestartOptions{Timeout: &timeout})
	if err != nil {
		return fmt.Errorf("restarting container %s: %w", n, err)
	}

	return nil
}

// --- eBPF helpers ---

// syncRoutes updates the global BPF route_map with TCP/SSH routes derived from
// the current egress rules store. Called during EnsureRunning, Enable, and
// regenerateAndRestart so all enforced containers immediately see rule changes.
func (m *Manager) syncRoutes(ctx context.Context) error {
	rules, warnings := normalizeAndDedup(m.store.Read().Rules)
	for _, w := range warnings {
		m.log.Warn().Msg(w)
	}
	ports := m.envoyPorts()
	tcpMappings := TCPMappings(rules, ports)

	routes := make([]ebpfRoute, 0, len(tcpMappings))
	for _, mp := range tcpMappings {
		routes = append(routes, ebpfRoute{
			DomainHash: clawkerebpf.DomainHash(mp.Dst),
			DstPort:    uint16(mp.DstPort),
			EnvoyPort:  uint16(mp.EnvoyPort),
		})
	}

	if err := m.ebpfExec(ctx, "sync-routes", marshalRoutes(routes)); err != nil {
		return fmt.Errorf("syncing BPF route_map: %w", err)
	}
	return nil
}

// ebpfRoute is the JSON-serializable route type for the eBPF manager container.
type ebpfRoute struct {
	DomainHash uint32 `json:"domain_hash"`
	DstPort    uint16 `json:"dst_port"`
	EnvoyPort  uint16 `json:"envoy_port"`
}

// ebpfStaticIP computes the eBPF manager's static IP from the Envoy IP.
// Envoy is .200, CoreDNS is .201, eBPF manager is .202.
func ebpfStaticIP(envoyIP string) string {
	ip := net.ParseIP(envoyIP).To4()
	if ip == nil {
		return ""
	}
	ip[3] = ip[3] + 2 // envoy(.200) + 2 = .202
	return ip.String()
}

// ebpfCgroupPath returns the cgroup v2 path for a Docker container.
// The path format depends on the cgroup driver:
//   - cgroupfs (Docker Desktop default): /sys/fs/cgroup/docker/<containerID>
//   - systemd (native Linux):            /sys/fs/cgroup/system.slice/docker-<containerID>.scope
func ebpfCgroupPath(cgroupDriver, containerID string) string {
	if cgroupDriver == "systemd" {
		return "/sys/fs/cgroup/system.slice/docker-" + containerID + ".scope"
	}
	return "/sys/fs/cgroup/docker/" + containerID
}

// cgroupDriver queries the Docker daemon for its cgroup driver (e.g. "cgroupfs" or "systemd").
func (m *Manager) cgroupDriver(ctx context.Context) string {
	info, err := m.client.Info(ctx, client.InfoOptions{})
	if err != nil {
		m.log.Warn().Err(err).Msg("failed to query Docker cgroup driver, assuming cgroupfs")
		return "cgroupfs"
	}
	return info.Info.CgroupDriver
}

// ebpfExec runs a command in the eBPF manager container via docker exec.
func (m *Manager) ebpfExec(ctx context.Context, args ...string) error {
	cmd := append([]string{"/usr/local/bin/ebpf-manager"}, args...)

	execResp, err := m.client.ExecCreate(ctx, string(ebpfContainer), client.ExecCreateOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	})
	if err != nil {
		return fmt.Errorf("creating exec in %s: %w", ebpfContainer, err)
	}

	hijack, err := m.client.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("attaching exec in %s: %w", ebpfContainer, err)
	}
	defer hijack.Conn.Close()

	output, _ := io.ReadAll(hijack.Reader)

	inspectExec, err := m.client.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspecting exec in %s: %w", ebpfContainer, err)
	}
	if inspectExec.ExitCode != 0 {
		return fmt.Errorf("ebpf-manager %s exited %d: %s", args[0], inspectExec.ExitCode, strings.TrimSpace(string(output)))
	}
	return nil
}

// ebpfExecOutput runs a command in the eBPF manager container and returns stdout.
// Uses stdcopy to demultiplex Docker's stream headers from the exec output.
func (m *Manager) ebpfExecOutput(ctx context.Context, args ...string) (string, error) {
	cmd := append([]string{"/usr/local/bin/ebpf-manager"}, args...)

	execResp, err := m.client.ExecCreate(ctx, string(ebpfContainer), client.ExecCreateOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	})
	if err != nil {
		return "", fmt.Errorf("creating exec in %s: %w", ebpfContainer, err)
	}

	hijack, err := m.client.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("attaching exec in %s: %w", ebpfContainer, err)
	}
	defer hijack.Conn.Close()

	var stdout, stderr bytes.Buffer
	_, _ = stdcopy.StdCopy(&stdout, &stderr, hijack.Reader)

	inspectExec, err := m.client.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspecting exec in %s: %w", ebpfContainer, err)
	}
	if inspectExec.ExitCode != 0 {
		return "", fmt.Errorf("ebpf-manager %s exited %d: %s", args[0], inspectExec.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// ebpfContainerConfig returns the container creation config for the eBPF manager.
func (m *Manager) ebpfContainerConfig(net *NetworkInfo) containerSpec {
	stopTimeout := 1 // sleep infinity ignores SIGTERM; no graceful shutdown needed
	return containerSpec{
		image:       ebpfImageTag,
		networkName: m.cfg.ClawkerNetwork(),
		networkID:   net.NetworkID,
		staticIP:    ebpfStaticIP(net.EnvoyIP),
		labels: map[string]string{
			m.cfg.LabelManaged(): "true",
			m.cfg.LabelPurpose(): m.cfg.PurposeFirewall(),
		},
		stopTimeout: &stopTimeout,
		cmd:         []string{"sleep", "infinity"},
		mounts: []mount.Mount{
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
		},
		capAdd: []string{"BPF", "SYS_ADMIN", "NET_ADMIN"},
	}
}

// computeGateway extracts the gateway IP (.1) from a CIDR.
func computeGateway(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	ip = ip.To4()
	if ip == nil {
		return ""
	}
	ip[3] = 1
	return ip.String()
}

// marshalRoutes serializes routes as a JSON array string.
func marshalRoutes(routes []ebpfRoute) string {
	if len(routes) == 0 {
		return "[]"
	}
	b, err := json.Marshal(routes)
	if err != nil {
		return "[]"
	}
	return string(b)
}
