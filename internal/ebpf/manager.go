package ebpf

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"

	"github.com/schmitthub/clawker/internal/logger"
)

// Manager loads BPF programs and manages per-container map entries and cgroup attachments.
type Manager struct {
	pinPath string
	log     *logger.Logger

	objs clawkerObjects

	// Per-container cgroup links, keyed by cgroup ID.
	// Only populated when this Manager instance attaches programs (daemon mode).
	links map[uint64][]link.Link
}

// NewManager creates a new eBPF manager. Call Load() or OpenPinned() before use.
func NewManager(log *logger.Logger) *Manager {
	return &Manager{
		pinPath: PinPath,
		log:     log,
		links:   make(map[uint64][]link.Link),
	}
}

// Load loads BPF programs from embedded ELF objects, pins maps and programs
// to /sys/fs/bpf/clawker/. Called once at container startup (daemon mode).
func (m *Manager) Load() error {
	if err := os.MkdirAll(m.pinPath, 0o700); err != nil {
		return fmt.Errorf("ebpf: creating pin path %s: %w", m.pinPath, err)
	}

	// Remove stale link pins from previous runs. Pinned links keep old
	// programs alive and attached to dead cgroups. Clean them before
	// loading new programs to avoid resource leaks.
	m.cleanupAllLinks()

	spec, err := loadClawker()
	if err != nil {
		return fmt.Errorf("ebpf: loading collection spec: %w", err)
	}

	for _, mapSpec := range spec.Maps {
		mapSpec.Pinning = ebpf.PinByName
	}

	// Remove stale pinned maps whose schema has changed (e.g., key size).
	// The BPF loader refuses to reuse a pinned map with incompatible specs.
	for name, mapSpec := range spec.Maps {
		pin := filepath.Join(m.pinPath, name)
		existing, err := ebpf.LoadPinnedMap(pin, nil)
		if err != nil {
			continue // not pinned or can't open — Load will handle it
		}
		info, err := existing.Info()
		existing.Close()
		if err != nil {
			continue
		}
		if info.KeySize != mapSpec.KeySize || info.ValueSize != mapSpec.ValueSize {
			m.log.Warn().Str("map", name).
				Uint32("old_key", info.KeySize).Uint32("new_key", mapSpec.KeySize).
				Uint32("old_val", info.ValueSize).Uint32("new_val", mapSpec.ValueSize).
				Msg("pinned map schema changed, removing stale pin")
			os.Remove(pin)
		}
	}

	if err := spec.LoadAndAssign(&m.objs, &ebpf.CollectionOptions{
		Maps: ebpf.MapOptions{PinPath: m.pinPath},
	}); err != nil {
		return fmt.Errorf("ebpf: loading objects: %w", err)
	}

	// Pin programs so command-mode instances can open them for cgroup attachment.
	// Remove stale pins first — the embedded ELF may have newer programs.
	progs := map[string]*ebpf.Program{
		"clawker_connect4":    m.objs.ClawkerConnect4,
		"clawker_sendmsg4":    m.objs.ClawkerSendmsg4,
		"clawker_recvmsg4":    m.objs.ClawkerRecvmsg4,
		"clawker_connect6":    m.objs.ClawkerConnect6,
		"clawker_sendmsg6":    m.objs.ClawkerSendmsg6,
		"clawker_recvmsg6":    m.objs.ClawkerRecvmsg6,
		"clawker_sock_create": m.objs.ClawkerSockCreate,
	}
	for name, prog := range progs {
		pin := filepath.Join(m.pinPath, name)
		os.Remove(pin) // remove stale pin (best-effort)
		if err := prog.Pin(pin); err != nil {
			return fmt.Errorf("ebpf: pinning program %s: %w", name, err)
		}
	}

	m.log.Info().Str("pin_path", m.pinPath).Msg("eBPF programs loaded and pinned")
	return nil
}

// OpenPinned opens already-pinned maps and programs from /sys/fs/bpf/clawker/.
// Used by command-mode instances (docker exec) that operate on maps without
// re-loading the BPF programs.
func (m *Manager) OpenPinned() error {
	maps := map[string]**ebpf.Map{
		"container_map": &m.objs.ContainerMap,
		"bypass_map":    &m.objs.BypassMap,
		"dns_cache":     &m.objs.DnsCache,
		"route_map":     &m.objs.RouteMap,
		"metrics_map":   &m.objs.MetricsMap,
	}
	for name, target := range maps {
		mp, err := ebpf.LoadPinnedMap(filepath.Join(m.pinPath, name), nil)
		if err != nil {
			return fmt.Errorf("ebpf: opening pinned map %s: %w", name, err)
		}
		*target = mp
	}

	progs := map[string]**ebpf.Program{
		"clawker_connect4":    &m.objs.ClawkerConnect4,
		"clawker_sendmsg4":    &m.objs.ClawkerSendmsg4,
		"clawker_recvmsg4":    &m.objs.ClawkerRecvmsg4,
		"clawker_connect6":    &m.objs.ClawkerConnect6,
		"clawker_sendmsg6":    &m.objs.ClawkerSendmsg6,
		"clawker_recvmsg6":    &m.objs.ClawkerRecvmsg6,
		"clawker_sock_create": &m.objs.ClawkerSockCreate,
	}
	for name, target := range progs {
		p, err := ebpf.LoadPinnedProgram(filepath.Join(m.pinPath, name), nil)
		if err != nil {
			return fmt.Errorf("ebpf: opening pinned program %s: %w", name, err)
		}
		*target = p
	}

	return nil
}

// cleanupAllLinks removes ALL pinned link files from the pin directory.
// Called during Load() to clear stale links from previous runs that keep
// old programs alive and attached to dead cgroups.
func (m *Manager) cleanupAllLinks() {
	entries, err := os.ReadDir(m.pinPath)
	if err != nil {
		return // pin dir may not exist yet
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "link_") {
			pin := filepath.Join(m.pinPath, e.Name())
			l, lerr := link.LoadPinnedLink(pin, nil)
			if lerr == nil {
				l.Unpin()
				l.Close()
			} else {
				os.Remove(pin) // best-effort
			}
		}
	}
}

// cleanupLinks removes stale pinned links for a cgroup ID.
// This ensures re-Enable() attaches fresh programs, not stale ones from a previous run.
func (m *Manager) cleanupLinks(cgroupID uint64) {
	// Close in-memory links if we have them.
	if links, ok := m.links[cgroupID]; ok {
		for _, l := range links {
			l.Close()
		}
		delete(m.links, cgroupID)
	}

	// Remove pinned link files for this cgroup.
	progNames := []string{"connect4", "sendmsg4", "recvmsg4", "connect6", "sendmsg6", "recvmsg6", "sock_create"}
	for _, name := range progNames {
		pin := filepath.Join(m.pinPath, fmt.Sprintf("link_%s_%d", name, cgroupID))
		os.Remove(pin)
	}
}

// Close detaches all links and closes all programs and maps.
func (m *Manager) Close() error {
	for cgID, links := range m.links {
		for _, l := range links {
			if err := l.Close(); err != nil {
				m.log.Warn().Err(err).Uint64("cgroup_id", cgID).Msg("closing cgroup link")
			}
		}
	}
	m.links = make(map[uint64][]link.Link)
	return m.objs.Close()
}

// Enable attaches BPF programs to a container's cgroup and populates routing maps.
// Cleans up any stale links for this cgroup before attaching.
func (m *Manager) Enable(cgroupID uint64, cgroupPath string, cfg clawkerContainerConfig) error {
	// Clean up stale links from previous Enable() calls for this cgroup.
	// Stale links keep old programs attached, causing silent misbehavior.
	m.cleanupLinks(cgroupID)

	if err := m.objs.ContainerMap.Update(cgroupID, cfg, ebpf.UpdateAny); err != nil {
		return fmt.Errorf("ebpf enable: updating container_map: %w", err)
	}

	type attachment struct {
		name string
		prog *ebpf.Program
		typ  ebpf.AttachType
	}

	attachments := []attachment{
		{"connect4", m.objs.ClawkerConnect4, ebpf.AttachCGroupInet4Connect},
		{"sendmsg4", m.objs.ClawkerSendmsg4, ebpf.AttachCGroupUDP4Sendmsg},
		{"recvmsg4", m.objs.ClawkerRecvmsg4, ebpf.AttachCGroupUDP4Recvmsg},
		{"connect6", m.objs.ClawkerConnect6, ebpf.AttachCGroupInet6Connect},
		{"sendmsg6", m.objs.ClawkerSendmsg6, ebpf.AttachCGroupUDP6Sendmsg},
		{"recvmsg6", m.objs.ClawkerRecvmsg6, ebpf.AttachCGroupUDP6Recvmsg},
		{"sock_create", m.objs.ClawkerSockCreate, ebpf.AttachCGroupInetSockCreate},
	}

	var linked []link.Link
	for _, a := range attachments {
		l, err := link.AttachCgroup(link.CgroupOptions{
			Path:    cgroupPath,
			Attach:  a.typ,
			Program: a.prog,
		})
		if err != nil {
			for _, prev := range linked {
				prev.Close()
			}
			return fmt.Errorf("ebpf enable: attaching %s: %w", a.name, err)
		}

		// Pin the link so it persists if this process exits.
		pinPath := filepath.Join(m.pinPath, fmt.Sprintf("link_%s_%d", a.name, cgroupID))
		os.Remove(pinPath) // remove stale pin
		if pinErr := l.Pin(pinPath); pinErr != nil {
			m.log.Warn().Err(pinErr).Str("program", a.name).Msg("ebpf enable: pinning link (non-fatal)")
		}

		linked = append(linked, l)
	}

	m.links[cgroupID] = linked
	m.log.Info().Uint64("cgroup_id", cgroupID).Msg("eBPF programs attached")
	return nil
}

// Disable detaches BPF programs from a container's cgroup and removes map entries.
func (m *Manager) Disable(cgroupID uint64) error {
	// Close in-memory links if we hold them.
	if linked, ok := m.links[cgroupID]; ok {
		for _, l := range linked {
			l.Close()
		}
		delete(m.links, cgroupID)
	}

	// Also unpin any persisted links for this cgroup.
	linkNames := []string{"connect4", "sendmsg4", "recvmsg4", "connect6", "sendmsg6", "recvmsg6", "sock_create"}
	for _, name := range linkNames {
		pinPath := filepath.Join(m.pinPath, fmt.Sprintf("link_%s_%d", name, cgroupID))
		l, err := link.LoadPinnedLink(pinPath, nil)
		if err == nil {
			l.Unpin()
			l.Close()
		} else {
			os.Remove(pinPath) // best-effort cleanup
		}
	}

	if err := m.objs.ContainerMap.Delete(cgroupID); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		m.log.Warn().Err(err).Msg("ebpf disable: deleting container_map entry")
	}
	_ = m.objs.BypassMap.Delete(cgroupID)

	// route_map is global (not per-container) — no cleanup needed here.

	m.log.Info().Uint64("cgroup_id", cgroupID).Msg("eBPF programs detached")
	return nil
}

// SyncRoutes replaces the global route_map with the given routes.
// Called by the firewall manager whenever egress rules change (add/remove/reload).
// All enforced containers immediately see the updated routes.
func (m *Manager) SyncRoutes(routes []Route) error {
	// Clear existing routes.
	var keysToDelete []clawkerRouteKey
	var rk clawkerRouteKey
	var rv clawkerRouteVal
	iter := m.objs.RouteMap.Iterate()
	for iter.Next(&rk, &rv) {
		keysToDelete = append(keysToDelete, rk)
	}
	for _, k := range keysToDelete {
		_ = m.objs.RouteMap.Delete(k)
	}

	// Populate with new routes.
	for _, r := range routes {
		key := clawkerRouteKey{
			DomainHash: r.DomainHash,
			DstPort:    r.DstPort,
		}
		val := clawkerRouteVal{EnvoyPort: r.EnvoyPort}
		if err := m.objs.RouteMap.Update(key, val, ebpf.UpdateAny); err != nil {
			m.log.Warn().Err(err).Uint32("domain_hash", r.DomainHash).Msg("ebpf sync-routes: updating route_map")
		}
	}

	m.log.Info().Int("routes", len(routes)).Msg("global route_map synced")
	return nil
}

// Bypass sets the bypass flag for a container, allowing unrestricted egress.
func (m *Manager) Bypass(cgroupID uint64) error {
	val := uint8(1)
	if err := m.objs.BypassMap.Update(cgroupID, val, ebpf.UpdateAny); err != nil {
		return fmt.Errorf("ebpf bypass: %w", err)
	}
	m.log.Info().Uint64("cgroup_id", cgroupID).Msg("eBPF bypass enabled")
	return nil
}

// Unbypass removes the bypass flag, restoring firewall enforcement.
func (m *Manager) Unbypass(cgroupID uint64) error {
	if err := m.objs.BypassMap.Delete(cgroupID); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		return fmt.Errorf("ebpf unbypass: %w", err)
	}
	m.log.Info().Uint64("cgroup_id", cgroupID).Msg("eBPF bypass disabled")
	return nil
}

// UpdateDNSCache writes a DNS resolution result to the dns_cache map.
func (m *Manager) UpdateDNSCache(ip uint32, domainHash uint32, ttlSeconds uint32) error {
	entry := clawkerDnsEntry{
		DomainHash: domainHash,
		ExpireTs:   uint32(time.Now().Unix()) + ttlSeconds,
	}
	return m.objs.DnsCache.Update(ip, entry, ebpf.UpdateAny)
}

// GarbageCollectDNS removes expired entries from the dns_cache map.
func (m *Manager) GarbageCollectDNS() int {
	now := uint32(time.Now().Unix())
	var ip uint32
	var entry clawkerDnsEntry
	var expired []uint32

	iter := m.objs.DnsCache.Iterate()
	for iter.Next(&ip, &entry) {
		if entry.ExpireTs < now {
			expired = append(expired, ip)
		}
	}
	for _, key := range expired {
		_ = m.objs.DnsCache.Delete(key)
	}
	return len(expired)
}

// LookupContainer returns the container_map entry for a given cgroup ID.
func (m *Manager) LookupContainer(cgroupID uint64) (clawkerContainerConfig, error) {
	var cfg clawkerContainerConfig
	err := m.objs.ContainerMap.Lookup(cgroupID, &cfg)
	return cfg, err
}

// Route describes a per-domain TCP route for a container, identified by domain hash.
type Route struct {
	DomainHash uint32 `json:"domain_hash"`
	DstPort    uint16 `json:"dst_port"`
	EnvoyPort  uint16 `json:"envoy_port"`
}

// NewContainerConfig builds a BPF container_config from network parameters.
func NewContainerConfig(envoyIP, corednsIP, gatewayIP, cidr string,
	hostProxyIP string, hostProxyPort, egressPort uint16) (clawkerContainerConfig, error) {

	netAddr, netMask, err := CIDRToAddrMask(cidr)
	if err != nil {
		return clawkerContainerConfig{}, fmt.Errorf("parsing CIDR %s: %w", cidr, err)
	}

	cfg := clawkerContainerConfig{
		EnvoyIp:       IPToUint32(parseIP(envoyIP)),
		CorednsIp:     IPToUint32(parseIP(corednsIP)),
		GatewayIp:     IPToUint32(parseIP(gatewayIP)),
		NetAddr:       netAddr,
		NetMask:       netMask,
		HostProxyPort: hostProxyPort,
		EgressPort:    egressPort,
	}
	if hostProxyIP != "" {
		cfg.HostProxyIp = IPToUint32(parseIP(hostProxyIP))
	}
	return cfg, nil
}

// CgroupPath returns the cgroup v2 path for a Docker container.
func CgroupPath(containerID string) string {
	return filepath.Join("/sys/fs/cgroup/system.slice", "docker-"+containerID+".scope")
}

// cgroupRoot is the only legitimate filesystem root for cgroup v2 paths.
// Validated in CgroupID to sanitize caller-supplied paths against traversal
// and injection — defense-in-depth for the privileged ebpf-manager binary
// running with CAP_BPF + CAP_SYS_ADMIN.
const cgroupRoot = "/sys/fs/cgroup/"

// validateCgroupPath canonicalizes p and ensures it points inside the
// cgroup v2 hierarchy. Returns the cleaned path or an error. The
// filepath.Clean + HasPrefix(constant) + ".." rejection chain is
// recognized by CodeQL's go/path-injection query as a sanitizer barrier.
func validateCgroupPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("cgroup path is empty")
	}
	if strings.ContainsAny(p, "\x00\n\r") {
		return "", fmt.Errorf("cgroup path contains illegal characters: %q", p)
	}
	if strings.Contains(p, "..") {
		return "", fmt.Errorf("cgroup path must not contain '..': %q", p)
	}
	cleaned := filepath.Clean(p)
	if !strings.HasPrefix(cleaned, cgroupRoot) {
		return "", fmt.Errorf("cgroup path must be under %s: %q", cgroupRoot, p)
	}
	return cleaned, nil
}

// CgroupID reads the cgroup ID from a cgroup path (inode number on cgroup v2).
// The path is validated against validateCgroupPath before being opened, which
// both enforces the /sys/fs/cgroup/ invariant and acts as the CodeQL
// go/path-injection sanitizer for the ebpf-manager entry points that pass
// os.Args through to here.
func CgroupID(cgroupPath string) (uint64, error) {
	cgroupPath, err := validateCgroupPath(cgroupPath)
	if err != nil {
		return 0, err
	}

	f, err := os.Open(cgroupPath)
	if err != nil {
		return 0, fmt.Errorf("opening cgroup: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat cgroup: %w", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("unexpected stat type for cgroup %s", cgroupPath)
	}
	return stat.Ino, nil
}

// Supported checks if the current kernel supports eBPF cgroup programs.
func Supported() error {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return fmt.Errorf("cgroup v2 not available: %w", err)
	}
	return nil
}
