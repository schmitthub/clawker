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
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	clawkerebpf "github.com/schmitthub/clawker/internal/controlplane/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/storage"
	"google.golang.org/grpc"
)

// firewallContainer is a typed constant restricting container name arguments
// to the known firewall infrastructure containers.
type firewallContainer string

const (
	envoyContainer   firewallContainer = "clawker-envoy"
	corednsContainer firewallContainer = "clawker-coredns"
	// cpContainer is the clawker control plane container. Replaces the
	// historical clawker-ebpf container (which ran `sleep infinity` as a
	// docker-exec target). The CP now owns ebpf.Manager.Load() lifetime
	// directly and serves ControlPlaneService over mTLS gRPC on a UDS.
	cpContainer firewallContainer = "clawker-cp"
)

// Infrastructure container constants.
const (
	envoyImage = "envoyproxy/envoy:distroless-v1.37.1@sha256:4d9226b9fd4d1449887de7cde785beb24b12e47d6e79021dec3c79e362609432"

	// cpImageTag is the local Docker image tag for the clawker control plane.
	// Built on-demand from the embedded clawker-cp binary when the image
	// doesn't exist locally. The image also bundles the ebpf-manager binary
	// as a break-glass debug tool (not the primary interface).
	cpImageTag = "clawker-cp:latest"

	// corednsImageTag is the local Docker image tag for the custom CoreDNS
	// with the dnsbpf plugin. Built on-demand from the embedded binary.
	corednsImageTag = "clawker-coredns:latest"

	// Health check timing.
	healthCheckTimeout  = 60 * time.Second
	healthCheckInterval = 500 * time.Millisecond

	// Readiness gate timing for the clawker-cp container after start.
	// The CP writes <firewallDataDir>/cp-ready when it has loaded the BPF
	// programs and bound its listeners — the firewall manager polls that
	// file before proceeding with SyncRoutes and Envoy/CoreDNS startup.
	cpReadyTimeout  = 30 * time.Second
	cpReadyInterval = 100 * time.Millisecond
)

// Compile-time interface compliance check.
var _ FirewallManager = (*Manager)(nil)

// Manager implements FirewallManager using the Docker API.
// It manages Envoy, CoreDNS, and clawker-cp containers on the clawker-net
// Docker network.
type Manager struct {
	client client.APIClient
	cfg    config.Config
	log    *logger.Logger
	store  *storage.Store[EgressRulesFile]

	// Test seams — all nil in production, wired to the real implementations
	// in NewManager. Tests in package firewall can override these to observe
	// arguments or inject failures without standing up a full Docker API
	// mock. Each field is called by the corresponding method below — the
	// real methods are named with an "Impl" suffix so that the public
	// names remain the canonical entry points.
	cgroupDriverFn    func(ctx context.Context) (string, error)
	ebpfExecFn        func(ctx context.Context, args ...string) error
	ebpfExecOutputFn  func(ctx context.Context, args ...string) (string, error)
	touchSignalFileFn func(ctx context.Context, containerID string) error
	waitForCPReadyFn  func(ctx context.Context) error

	// CP gRPC client. Built lazily on first use by cpClient() because the
	// CP must be running and must have generated its cert material before
	// the client can dial.
	cpClientMu     sync.Mutex
	cpClientCached adminv1.AdminServiceClient
	cpClientConn   *grpc.ClientConn
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
	m := &Manager{
		client: client,
		cfg:    cfg,
		log:    log,
		store:  store,
	}
	// Wire test seams to the real implementations. Tests can overwrite
	// any of these fields directly (they live in package firewall).
	m.cgroupDriverFn = m.cgroupDriverImpl
	m.ebpfExecFn = m.ebpfExecImpl
	m.ebpfExecOutputFn = m.ebpfExecOutputImpl
	m.touchSignalFileFn = m.touchSignalFileImpl
	m.waitForCPReadyFn = m.waitForCPReadyImpl
	return m, nil
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
	if err := m.ensureCPImage(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	if err := m.ensureCorednsImage(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	// Step 5: Start the clawker control plane container.
	// This MUST happen before CoreDNS starts because the CP's ebpf.Manager
	// .Load() pins the dns_cache BPF map, and the dnsbpf plugin opens it
	// on CoreDNS startup — the map must already exist.
	cpSpec, err := m.cpContainerConfig(netInfo)
	if err != nil {
		return fmt.Errorf("firewall: building cp container config: %w", err)
	}
	if err := m.ensureContainer(ctx, cpContainer, cpSpec); err != nil {
		return fmt.Errorf("firewall: ensuring clawker-cp: %w", err)
	}
	if err := m.waitForCPReady(ctx); err != nil {
		return fmt.Errorf("firewall: waiting for clawker-cp readiness: %w", err)
	}
	// NOTE: no ebpfExec("init") here. The CP owns ebpf.Manager.Load()
	// lifetime in-process — calling init again would re-run cleanupAllLinks
	// and strip BPF programs from any other running containers.
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
	ebpfErr := m.stopAndRemove(ctx, cpContainer)

	if err := errors.Join(envoyErr, corednsErr, ebpfErr); err != nil {
		return fmt.Errorf("firewall stop: %w", err)
	}
	return nil
}

// IsRunning reports whether all firewall containers are running.
func (m *Manager) IsRunning(ctx context.Context) bool {
	return m.isContainerRunning(ctx, envoyContainer) &&
		m.isContainerRunning(ctx, corednsContainer) &&
		m.isContainerRunning(ctx, cpContainer)
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

// resolveContainerID looks up a container by name, short ID, or long ID and
// returns its canonical long ID. Required because ebpfCgroupPath builds a
// literal filesystem path (/sys/fs/cgroup/docker/<long-id> with the cgroupfs
// driver, or .../docker-<long-id>.scope under systemd) that only matches the
// long ID — Docker's cgroup directories are never named after a container's
// friendly name. Enable/Disable/Bypass accept either form so that CLI
// callers (`clawker firewall {enable,disable,bypass} --agent foo`) can pass
// a container name while container-lifecycle callers (container run/stop)
// can pass the ID they already hold.
func (m *Manager) resolveContainerID(ctx context.Context, ref string) (string, error) {
	// Fast-path: if the caller already holds a canonical 64-char hex
	// container ID, skip the ContainerInspect round-trip. This matches
	// Docker's own "is this an ID?" heuristic (64 lowercase hex chars) and
	// is hit by every container-lifecycle caller (run/stop) that already
	// has the ID in hand.
	if len(ref) == 64 && isAllHex(ref) {
		return ref, nil
	}
	info, err := m.client.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("resolving container %q: %w", ref, err)
	}
	return info.Container.ID, nil
}

// isAllHex reports whether s contains only lowercase hexadecimal characters
// (0-9 and a-f). Docker container IDs are represented as lowercase hex in the
// API, so this matches the exact wire format.
func isAllHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// Disable detaches eBPF programs from a container's cgroup, removing all
// traffic routing. The container gets unrestricted egress. The container
// argument may be a name or ID — it is resolved to the canonical long ID
// before the cgroup path is constructed.
func (m *Manager) Disable(ctx context.Context, containerID string) error {
	containerID, err := m.resolveContainerID(ctx, containerID)
	if err != nil {
		return fmt.Errorf("firewall disable: %w", err)
	}
	driver, err := m.cgroupDriver(ctx)
	if err != nil {
		return fmt.Errorf("firewall disable: %w", err)
	}
	cgroupPath := ebpfCgroupPath(driver, containerID)
	if err := m.ebpfExec(ctx, "disable", cgroupPath); err != nil {
		return fmt.Errorf("firewall disable: %w", err)
	}

	m.log.Debug().Str("container", containerID).Msg("firewall disabled via eBPF")
	return nil
}

// Enable attaches eBPF programs to a container's cgroup, routing traffic
// through Envoy (TCP) and CoreDNS (DNS). Replaces iptables DNAT rules. The
// container argument may be a name or ID — it is resolved to the canonical
// long ID before the cgroup path is constructed.
func (m *Manager) Enable(ctx context.Context, containerID string) error {
	containerID, err := m.resolveContainerID(ctx, containerID)
	if err != nil {
		return fmt.Errorf("firewall enable: %w", err)
	}

	// Ensure the clawker-cp container is running (idempotent).
	// The daemon's EnsureRunning creates it, but it may be missing if the
	// daemon started before the CP feature was added, or if it was
	// manually removed.
	if err := m.ensureCPImage(ctx); err != nil {
		return fmt.Errorf("firewall enable: %w", err)
	}

	netInfo, err := m.discoverNetwork(ctx)
	if err != nil {
		return fmt.Errorf("firewall enable: discovering network: %w", err)
	}

	cpSpec, err := m.cpContainerConfig(netInfo)
	if err != nil {
		return fmt.Errorf("firewall enable: building cp container config: %w", err)
	}
	if err := m.ensureContainer(ctx, cpContainer, cpSpec); err != nil {
		return fmt.Errorf("firewall enable: ensuring clawker-cp container: %w", err)
	}
	if err := m.waitForCPReady(ctx); err != nil {
		return fmt.Errorf("firewall enable: waiting for clawker-cp readiness: %w", err)
	}

	// DO NOT re-run Load() here — the CP owns Manager.Load() lifetime
	// in-process, and programs are pinned to the host bpffs for the
	// daemon lifetime. Per-container Enable just attaches new links.

	// Sync global routes (idempotent — safe to call on every Enable).
	if err := m.syncRoutes(ctx); err != nil {
		return fmt.Errorf("firewall enable: %w", err)
	}

	// Host proxy bypass: read from project config (authoritative), not container env.
	// If the project has the host proxy enabled we MUST have a valid IP — a
	// silent Warn-and-continue here leaves the container unable to reach the
	// host proxy, breaking browser auth, git credential forwarding, and
	// everything else that tunnels through it. Surface resolve failures so the
	// daemon path fails loudly instead of producing a subtly-broken container.
	var hostProxyIP string
	var hostProxyPort uint16
	if m.cfg.Project().Security.HostProxyEnabled() {
		hostProxyPort = uint16(m.cfg.Settings().HostProxy.Daemon.Port)
		// Resolve host.docker.internal from inside the Docker network.
		// On Docker Desktop it resolves to 192.168.65.254, not localhost.
		resolved, resolveErr := m.ebpfExecOutput(ctx, "resolve", "host.docker.internal")
		if resolveErr != nil {
			return fmt.Errorf("firewall enable: resolving host.docker.internal for host-proxy bypass: %w", resolveErr)
		}
		hostProxyIP = strings.TrimSpace(resolved)
		if hostProxyIP == "" {
			return fmt.Errorf("firewall enable: host.docker.internal resolved to empty address for host-proxy bypass")
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

	driver, err := m.cgroupDriver(ctx)
	if err != nil {
		return fmt.Errorf("firewall enable: %w", err)
	}
	cgroupPath := ebpfCgroupPath(driver, containerID)
	if err := m.ebpfExec(ctx, "enable", cgroupPath, cfgJSON); err != nil {
		return fmt.Errorf("firewall enable: %w", err)
	}

	// Signal the entrypoint that firewall is ready (unblocks CMD). The
	// entrypoint script in internal/bundler/assets/entrypoint.sh blocks on
	// this file — a silent failure here leaves the container hanging forever
	// waiting for a signal that will never come, so we propagate instead of
	// logging and continuing.
	if err := m.touchSignalFile(ctx, containerID); err != nil {
		return fmt.Errorf("firewall enable: touching firewall-ready signal: %w", err)
	}

	m.log.Debug().Str("container", containerID).Msg("firewall enabled via eBPF")
	return nil
}

// firewallReadyPath is the signal file the entrypoint waits for before running CMD.
const firewallReadyPath = "/var/run/clawker/firewall-ready"

// touchSignalFile creates the firewall-ready signal file inside the container,
// unblocking the entrypoint's wait loop. Routed through touchSignalFileFn for
// test override.
func (m *Manager) touchSignalFile(ctx context.Context, containerID string) error {
	return m.touchSignalFileFn(ctx, containerID)
}

// touchSignalFileImpl is the production implementation of touchSignalFile.
func (m *Manager) touchSignalFileImpl(ctx context.Context, containerID string) error {
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
// manager container (sleep + unbypass). The container argument may be a name
// or ID — it is resolved to the canonical long ID before the cgroup path is
// constructed.
func (m *Manager) Bypass(ctx context.Context, containerID string, timeout time.Duration) error {
	containerID, err := m.resolveContainerID(ctx, containerID)
	if err != nil {
		return fmt.Errorf("firewall bypass: %w", err)
	}
	driver, err := m.cgroupDriver(ctx)
	if err != nil {
		return fmt.Errorf("firewall bypass: %w", err)
	}
	cgroupPath := ebpfCgroupPath(driver, containerID)

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
	execResp, err := m.client.ExecCreate(ctx, string(cpContainer), client.ExecCreateOptions{
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

	// Discover network state. A NotFound error is legitimate (the firewall
	// hasn't been brought up yet, so the clawker-net bridge doesn't exist);
	// in that case we leave the network fields empty and continue. Any other
	// error means we genuinely can't talk to Docker, which must propagate so
	// callers distinguish "firewall down" from "Docker unreachable".
	netInfo, err := m.discoverNetwork(ctx)
	if err != nil && !cerrdefs.IsNotFound(err) {
		return nil, fmt.Errorf("firewall status: %w", err)
	}

	// Status must propagate Docker API errors rather than silently reporting
	// "not running" — callers (CLI, daemon) distinguish "firewall is down"
	// from "we couldn't talk to Docker" to decide what to do next.
	envoyRunning, envoyErr := m.isContainerRunningE(ctx, envoyContainer)
	if envoyErr != nil {
		return nil, fmt.Errorf("firewall status: %w", envoyErr)
	}
	corednsRunning, corednsErr := m.isContainerRunningE(ctx, corednsContainer)
	if corednsErr != nil {
		return nil, fmt.Errorf("firewall status: %w", corednsErr)
	}
	ebpfRunning, ebpfErr := m.isContainerRunningE(ctx, cpContainer)
	if ebpfErr != nil {
		return nil, fmt.Errorf("firewall status: %w", ebpfErr)
	}

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
	// start them with the fresh configs when it brings the stack up. We
	// use the error-returning form so that a transient Docker API failure
	// doesn't silently degrade to a no-op (which would leave the stack
	// running with stale rules until the daemon's next reconcile pass).
	envoyRunning, err := m.isContainerRunningE(ctx, envoyContainer)
	if err != nil {
		return fmt.Errorf("firewall reload: %w", err)
	}
	corednsRunning, err := m.isContainerRunningE(ctx, corednsContainer)
	if err != nil {
		return fmt.Errorf("firewall reload: %w", err)
	}
	ebpfRunning, err := m.isContainerRunningE(ctx, cpContainer)
	if err != nil {
		return fmt.Errorf("firewall reload: %w", err)
	}
	if !(envoyRunning && corednsRunning && ebpfRunning) {
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

// embeddedBinary describes one binary to COPY into an on-demand image
// built from embedded bytes. Used by embeddedImageSpec to support
// images with more than one binary (e.g. the clawker-cp image bundles
// both clawker-cp and ebpf-manager).
type embeddedBinary struct {
	binary   []byte // Embedded binary bytes (go:embed'd somewhere)
	fileName string // Filename inside the tar context, e.g. "clawker-cp"
}

// embeddedImageSpec describes a set of embedded binaries that can be
// baked into a Docker image on-demand. Every firewall infrastructure
// image uses this same build-from-embedded-binary pattern so we can
// stamp out privileged images without a registry.
type embeddedImageSpec struct {
	tag        string           // Docker image tag (e.g. "clawker-cp:latest")
	binaries   []embeddedBinary // One or more binaries to COPY in
	dockerfile string           // Dockerfile content
	makeTarget string           // Make target for building the binary set (error messages)
}

// cpImageSpec is the template for the clawker-cp infrastructure image.
// The image bundles two binaries:
//
//   - clawker-cp: the CP daemon. Set as CMD, runs as PID 1.
//   - ebpf-manager: the short-lived break-glass debug CLI, invoked
//     manually via `docker exec clawker-cp ebpf-manager ...`. NOT the
//     primary interface — all machine-to-machine calls go via the
//     gRPC ControlPlaneService on the CP's UDS listener.
//
// Binary bytes are populated per-call in ensureCPImage to avoid copying
// the large []byte fields at package init time.
var cpImageSpec = embeddedImageSpec{
	tag: cpImageTag,
	binaries: []embeddedBinary{
		{fileName: "clawker-cp"},
		{fileName: "ebpf-manager"},
	},
	dockerfile: "FROM alpine:3.21@sha256:a8560b36e8b8210634f77d9f7f9efd7ffa463e380b75e2e74aff4511df3ef88c\n" +
		"RUN apk add --no-cache iproute2\n" +
		"COPY clawker-cp /usr/local/bin/clawker-cp\n" +
		"COPY ebpf-manager /usr/local/bin/ebpf-manager\n" +
		"CMD [\"/usr/local/bin/clawker-cp\"]\n",
	makeTarget: "cp-binary",
}

var corednsImageSpec = embeddedImageSpec{
	tag: corednsImageTag,
	binaries: []embeddedBinary{
		{fileName: "coredns"},
	},
	dockerfile: "FROM alpine:3.21@sha256:a8560b36e8b8210634f77d9f7f9efd7ffa463e380b75e2e74aff4511df3ef88c\n" +
		"COPY coredns /usr/local/bin/coredns\n" +
		"ENTRYPOINT [\"/usr/local/bin/coredns\"]\n",
	makeTarget: "coredns-binary",
}

// ensureCPImage ensures the clawker-cp Docker image exists locally.
// Builds it from the embedded clawker-cp + ebpf-manager binaries if
// it doesn't exist.
func (m *Manager) ensureCPImage(ctx context.Context) error {
	spec := cpImageSpec
	// Populate binary bytes per-call.
	spec.binaries = []embeddedBinary{
		{fileName: "clawker-cp", binary: clawkerCPBinary},
		{fileName: "ebpf-manager", binary: ebpfManagerBinary},
	}
	return m.ensureEmbeddedImage(ctx, spec)
}

// ensureCorednsImage ensures the custom CoreDNS image exists locally.
// Builds it from the embedded binary if it doesn't exist.
func (m *Manager) ensureCorednsImage(ctx context.Context) error {
	spec := corednsImageSpec
	spec.binaries = []embeddedBinary{
		{fileName: "coredns", binary: corednsClawkerBinary},
	}
	return m.ensureEmbeddedImage(ctx, spec)
}

// ensureEmbeddedImage checks if a locally-built image exists and builds it
// from the embedded binaries if not. Used for both the clawker-cp image
// (which bundles clawker-cp + ebpf-manager) and the custom CoreDNS image.
func (m *Manager) ensureEmbeddedImage(ctx context.Context, spec embeddedImageSpec) error {
	_, err := m.client.ImageInspect(ctx, spec.tag)
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("checking %s image: %w", spec.tag, err)
	}

	if len(spec.binaries) == 0 {
		return fmt.Errorf("%s has no embedded binaries — run 'make %s' then rebuild clawker",
			spec.tag, spec.makeTarget)
	}
	for _, b := range spec.binaries {
		if len(b.binary) == 0 {
			return fmt.Errorf("%s binary not embedded — run 'make %s' then rebuild clawker",
				b.fileName, spec.makeTarget)
		}
	}

	m.log.Debug().Str("image", spec.tag).Msg("building image from embedded binaries")

	buildCtx, err := embeddedBuildContext(spec)
	if err != nil {
		return fmt.Errorf("creating %s build context: %w", spec.tag, err)
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
// and every embedded binary in the spec, suitable for Docker ImageBuild.
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

	for _, b := range spec.binaries {
		if err := tw.WriteHeader(&tar.Header{
			Name: b.fileName,
			Size: int64(len(b.binary)),
			Mode: 0755,
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(b.binary); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}

// stopAndRemove stops and removes a firewall container.
// Not-found is silently ignored, but any Docker API error is surfaced so
// that Daemon.Run's Stop-error propagation (see daemon.go) can bubble
// failures up to the caller instead of silently leaving orphaned containers.
func (m *Manager) stopAndRemove(ctx context.Context, name firewallContainer) error {
	n := string(name)
	filters := client.Filters{}.Add("name", n)
	result, err := m.client.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: filters,
	})
	if err != nil {
		m.log.Error().Str("container", n).Err(err).Msg("container lookup failed during stop")
		return fmt.Errorf("listing container %s: %w", n, err)
	}
	if len(result.Items) == 0 {
		m.log.Debug().Str("container", n).Msg("container not found, skipping stop")
		return nil
	}

	ctr := result.Items[0]
	if ctr.State == container.StateRunning {
		// Timeout: nil → Docker honors the container-level StopTimeout set
		// in ContainerConfig at creation time (see moby client_stop.go: "If
		// the timeout is nil, the container's StopTimeout value is used, if
		// set, otherwise the engine default"). This lets the ebpf manager
		// container (which runs `sleep infinity` and cannot handle SIGTERM)
		// exit in 1 second via its configured stopTimeout, while envoy and
		// coredns fall back to Docker's 10-second default for graceful
		// HTTP drain. Previously a hardcoded 10s override here meant ebpf
		// always ate the full 10-second SIGTERM grace period, making
		// `firewall down` unnecessarily slow.
		if _, err := m.client.ContainerStop(ctx, ctr.ID, client.ContainerStopOptions{}); err != nil {
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

// isContainerRunningE checks whether a firewall container is running,
// returning the Docker API error instead of swallowing it. Internal callers
// (Status, regenerateAndRestart) use this form so that transient Docker API
// failures propagate to the caller rather than masquerading as "not
// running" and triggering an unintended code path.
func (m *Manager) isContainerRunningE(ctx context.Context, name firewallContainer) (bool, error) {
	n := string(name)
	filters := client.Filters{}.Add("name", n)
	result, err := m.client.ContainerList(ctx, client.ContainerListOptions{
		Filters: filters, // default: running only
	})
	if err != nil {
		return false, fmt.Errorf("listing container %s: %w", n, err)
	}
	return len(result.Items) > 0, nil
}

// isContainerRunning is the boolean-only wrapper used by the public
// IsRunning() method. IsRunning is part of the FirewallManager interface
// and cannot be broken to return an error, so lookup failures are logged
// at Warn and reported as "not running". All other internal callers use
// isContainerRunningE and propagate.
func (m *Manager) isContainerRunning(ctx context.Context, name firewallContainer) bool {
	running, err := m.isContainerRunningE(ctx, name)
	if err != nil {
		m.log.Warn().Str("container", string(name)).Err(err).Msg("firewall container lookup failed, reporting not running")
		return false
	}
	return running
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

// cgroupDriver queries the Docker daemon for its cgroup driver
// (e.g. "cgroupfs" or "systemd"). Routed through cgroupDriverFn for test
// override. Errors are surfaced to the caller rather than silently defaulted —
// a bad assumption on a systemd-driver host produces a cgroup path that does
// not exist and leads to cryptic ENOENT from ebpfExec downstream.
func (m *Manager) cgroupDriver(ctx context.Context) (string, error) {
	return m.cgroupDriverFn(ctx)
}

// cgroupDriverImpl is the production implementation of cgroupDriver.
func (m *Manager) cgroupDriverImpl(ctx context.Context) (string, error) {
	info, err := m.client.Info(ctx, client.InfoOptions{})
	if err != nil {
		return "", fmt.Errorf("querying Docker cgroup driver: %w", err)
	}
	return info.Info.CgroupDriver, nil
}

// ebpfExec dispatches a legacy-string-arg command to the clawker-cp's
// ControlPlaneService gRPC surface. The name is retained for backward
// compatibility with the existing ebpfExecFn test seam — unit tests that
// stub ebpfExecFn continue to work unchanged. Over time the intent is to
// migrate call sites to typed gRPC calls on a ControlPlaneServiceClient
// mock and delete this shim entirely.
//
// Routed through ebpfExecFn for test override.
func (m *Manager) ebpfExec(ctx context.Context, args ...string) error {
	return m.ebpfExecFn(ctx, args...)
}

// ebpfExecImpl is the production implementation of ebpfExec. It dispatches
// on the first arg (the old ebpf-manager subcommand name) to the matching
// typed gRPC method on ControlPlaneService.
func (m *Manager) ebpfExecImpl(ctx context.Context, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("ebpfExec: missing subcommand")
	}
	client, err := m.cpClient()
	if err != nil {
		return err
	}
	switch args[0] {
	case "init":
		// No-op: the CP runs ebpf.Manager.Load() once at startup and
		// keeps link handles alive for the process lifetime. Historical
		// callers that ran `ebpf-manager init` on every reload caused
		// the 2026-04-10 hot-reload pinning bug; eliminating the call
		// is the permanent fix.
		return nil
	case "enable":
		if len(args) != 3 {
			return fmt.Errorf("ebpfExec enable: expected <cgroupPath> <configJSON>, got %d args", len(args))
		}
		var cfg enableArgs
		if err := json.Unmarshal([]byte(args[2]), &cfg); err != nil {
			return fmt.Errorf("ebpfExec enable: parse config JSON: %w", err)
		}
		_, err := client.Install(ctx, &adminv1.InstallRequest{
			CgroupPath: args[1],
			Config: &adminv1.ContainerConfig{
				EnvoyIp:       cfg.EnvoyIP,
				CorednsIp:     cfg.CoreDNSIP,
				GatewayIp:     cfg.GatewayIP,
				Cidr:          cfg.CIDR,
				HostProxyIp:   cfg.HostProxyIP,
				HostProxyPort: uint32(cfg.HostProxyPort),
				EgressPort:    uint32(cfg.EgressPort),
			},
		})
		return err
	case "disable":
		if len(args) != 2 {
			return fmt.Errorf("ebpfExec disable: expected <cgroupPath>, got %d args", len(args))
		}
		_, err := client.Remove(ctx, &adminv1.RemoveRequest{
			CgroupPath: args[1],
		})
		return err
	case "bypass":
		if len(args) != 2 {
			return fmt.Errorf("ebpfExec bypass: expected <cgroupPath>, got %d args", len(args))
		}
		_, err := client.Disable(ctx, &adminv1.DisableRequest{
			CgroupPath: args[1],
		})
		return err
	case "unbypass":
		if len(args) != 2 {
			return fmt.Errorf("ebpfExec unbypass: expected <cgroupPath>, got %d args", len(args))
		}
		_, err := client.Enable(ctx, &adminv1.EnableRequest{
			CgroupPath: args[1],
		})
		return err
	case "sync-routes":
		if len(args) != 2 {
			return fmt.Errorf("ebpfExec sync-routes: expected <routesJSON>, got %d args", len(args))
		}
		var routes []ebpfRoute
		if err := json.Unmarshal([]byte(args[1]), &routes); err != nil {
			return fmt.Errorf("ebpfExec sync-routes: parse routes JSON: %w", err)
		}
		protoRoutes := make([]*adminv1.Route, 0, len(routes))
		for _, r := range routes {
			protoRoutes = append(protoRoutes, &adminv1.Route{
				DomainHash: r.DomainHash,
				DstPort:    uint32(r.DstPort),
				EnvoyPort:  uint32(r.EnvoyPort),
			})
		}
		_, err := client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{Routes: protoRoutes})
		return err
	default:
		return fmt.Errorf("ebpfExec: unknown subcommand %q", args[0])
	}
}

// enableArgs is the JSON payload historically passed to `ebpf-manager enable`.
// Retained so the ebpfExecImpl → gRPC shim can parse call-site JSON without
// touching every existing call site. Future follow-ups should migrate call
// sites to typed EnableContainerFirewallRequest construction and delete this.
type enableArgs struct {
	EnvoyIP       string `json:"envoy_ip"`
	CoreDNSIP     string `json:"coredns_ip"`
	GatewayIP     string `json:"gateway_ip"`
	CIDR          string `json:"cidr"`
	HostProxyIP   string `json:"host_proxy_ip"`
	HostProxyPort uint16 `json:"host_proxy_port"`
	EgressPort    uint16 `json:"egress_port"`
}

// ebpfExecOutput dispatches a legacy-string-arg read-only command to the
// CP's ControlPlaneService. Currently only "resolve <host>" is used; kept
// as a separate seam because its return value is a string rather than an
// error.
func (m *Manager) ebpfExecOutput(ctx context.Context, args ...string) (string, error) {
	return m.ebpfExecOutputFn(ctx, args...)
}

// ebpfExecOutputImpl is the production implementation of ebpfExecOutput.
func (m *Manager) ebpfExecOutputImpl(ctx context.Context, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("ebpfExecOutput: missing subcommand")
	}
	client, err := m.cpClient()
	if err != nil {
		return "", err
	}
	switch args[0] {
	case "resolve":
		if len(args) != 2 {
			return "", fmt.Errorf("ebpfExecOutput resolve: expected <hostname>, got %d args", len(args))
		}
		resp, err := client.ResolveHostname(ctx, &adminv1.ResolveHostnameRequest{Hostname: args[1]})
		if err != nil {
			return "", err
		}
		if len(resp.GetAddresses()) == 0 {
			return "", fmt.Errorf("resolve %s: no addresses", args[1])
		}
		return resp.GetAddresses()[0], nil
	default:
		return "", fmt.Errorf("ebpfExecOutput: unknown subcommand %q", args[0])
	}
}

// waitForCPReady polls the CP's HTTP /healthz endpoint until it returns
// 200, indicating full initialization (subprocesses healthy, eBPF loaded,
// gRPC server serving). Called after ensureContainer(cpContainer) and
// before any gRPC call. Routed through waitForCPReadyFn for test override.
func (m *Manager) waitForCPReady(ctx context.Context) error {
	return m.waitForCPReadyFn(ctx)
}

// waitForCPReadyImpl polls http://127.0.0.1:<CPHealthPort>/healthz.
func (m *Manager) waitForCPReadyImpl(ctx context.Context) error {
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", consts.CPHealthPort)
	client := &http.Client{Timeout: 2 * time.Second}

	deadline := time.Now().Add(cpReadyTimeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return fmt.Errorf("build healthz request: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("clawker-cp did not become ready within %s (healthz at %s)",
				cpReadyTimeout, healthURL)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cpReadyInterval):
		}
	}
}

// cpClient returns the gRPC AdminServiceClient for the CP. Built lazily on
// first use. Uses auth.DialCPAdmin which handles mTLS + private_key_jwt
// token exchange via Hydra's /oauth2/token endpoint.
func (m *Manager) cpClient() (adminv1.AdminServiceClient, error) {
	m.cpClientMu.Lock()
	defer m.cpClientMu.Unlock()
	if m.cpClientCached != nil {
		return m.cpClientCached, nil
	}

	dataDir := config.DataDir()
	settings := m.cfg.Settings()
	adminPort := settings.ControlPlane.AdminPortOrDefault()

	client, conn, err := auth.DialCPAdmin(context.Background(), dataDir, adminPort, consts.HydraPublicPort)
	if err != nil {
		return nil, fmt.Errorf("dial cp: %w", err)
	}
	m.cpClientCached = client
	m.cpClientConn = conn
	return client, nil
}

// cpContainerConfig returns the container creation config for the clawker
// control plane. The CP replaces the sleep-infinity ebpf-manager container:
// it runs ebpf.Manager.Load() in-process, serves ControlPlaneService over
// mTLS gRPC on a UDS, and hosts the OIDC /token endpoint for CLI client
// credentials.
//
// The container mounts <firewallDataDir> into /var/run/clawker-cp so that
// cp-ca.{pem,key}, cp-oidc-signing.{pem,key}, cp-certs/, cp.sock,
// cp-oidc.sock, and cp-ready all live in a directory the host CLI can
// also read. Host-side code (internal/firewall/oidc_client.go) reads the
// certs from <firewallDataDir>/cp-certs/ and dials the UDS sockets
// directly.
func (m *Manager) cpContainerConfig(netInfo *NetworkInfo) (containerSpec, error) {
	// 30 seconds is plenty for GracefulStop + Manager.Close to finish.
	stopTimeout := 30
	dataDir, err := m.cfg.FirewallDataSubdir()
	if err != nil {
		return containerSpec{}, fmt.Errorf("firewall data dir: %w", err)
	}
	return containerSpec{
		image:       cpImageTag,
		networkName: m.cfg.ClawkerNetwork(),
		networkID:   netInfo.NetworkID,
		staticIP:    ebpfStaticIP(netInfo.EnvoyIP),
		labels: map[string]string{
			m.cfg.LabelManaged(): "true",
			m.cfg.LabelPurpose(): m.cfg.PurposeFirewall(),
		},
		stopTimeout: &stopTimeout,
		cmd:         []string{"/usr/local/bin/clawker-cp"},
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
			{
				// The CP writes CA, OIDC signing key, leaf certs, and
				// the UDS socket + ready file into this directory.
				// Host-side CLI reads the same directory.
				Type:   mount.TypeBind,
				Source: dataDir,
				Target: "/var/run/clawker-cp",
			},
		},
		capAdd: []string{"BPF", "SYS_ADMIN", "NET_ADMIN"},
	}, nil
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
