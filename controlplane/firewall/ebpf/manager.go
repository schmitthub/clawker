package ebpf

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"

	"github.com/schmitthub/clawker/internal/logger"
)

// BPFContainerConfig is the exported alias for the bpf2go-generated
// clawkerContainerConfig type (derived from C struct container_config).
type BPFContainerConfig = clawkerContainerConfig

// EBPFManager is the interface consumed by the firewall Handler. It covers
// the subset of Manager methods needed to serve gRPC admin RPCs, including
// the global FlushAll used by FirewallRemove.
//
//go:generate moq -rm -pkg mocks -out mocks/ebpf_manager_mock.go . EBPFManager
type EBPFManager interface {
	Install(cgroupID uint64, cgroupPath string, cfg BPFContainerConfig) error
	Remove(cgroupID uint64) error
	Enable(cgroupID uint64) error
	Disable(cgroupID uint64) error
	SyncRoutes(routes []Route) error
	FlushAll() error
}

// Manager loads BPF programs and manages per-container map entries and cgroup attachments.
type Manager struct {
	pinPath string
	log     *logger.Logger

	objs clawkerObjects

	// linksMu guards the links map. Install, Remove, and Close all mutate
	// links and may be called from different goroutines (gRPC handlers).
	linksMu sync.Mutex
	// Per-container cgroup links, keyed by cgroup ID.
	// Only populated when this Manager instance attaches programs (daemon mode).
	links map[uint64][]link.Link

	// seedMu guards seededIPs, which is only touched by SyncRoutes (gRPC
	// handler goroutine) but from potentially concurrent calls.
	seedMu sync.Mutex
	// seededIPs is the set of IP-literal destinations whose dns_cache entries
	// SyncRoutes owns, kept so a reconcile can delete entries for IP rules
	// that went away (orphan diff). Runtime protection of live seeds does not
	// depend on this set: seeded entries carry DNSSourceSeed in the map value
	// itself, which the dnsbpf plugin refuses to overwrite and
	// GarbageCollectDNS refuses to evict — so precedence survives a CP
	// restart even before the first SyncRoutes rebuilds this set.
	seededIPs map[uint32]struct{}
}

// NewManager creates a new eBPF manager. Call Load() or OpenPinned() before use.
func NewManager(log *logger.Logger) *Manager {
	return &Manager{
		pinPath:   PinPath,
		log:       log,
		links:     make(map[uint64][]link.Link),
		seededIPs: make(map[uint32]struct{}),
	}
}

// Load loads BPF programs from embedded ELF objects, pins maps and programs
// to /sys/fs/bpf/clawker/. Called once at container startup (daemon mode).
//
// Existing pinned maps are reused (PinByName) so container_map entries from
// a previous CP lifetime survive. Stale links (whose target cgroups no
// longer exist) are cleaned up to avoid resource leaks from dead containers.
// Links to live cgroups are preserved so enforcement persists across CP restarts.
func (m *Manager) Load() error {
	if err := os.MkdirAll(m.pinPath, 0o700); err != nil {
		return fmt.Errorf("ebpf: creating pin path %s: %w", m.pinPath, err)
	}

	// Clean up links to dead cgroups. Pinned links keep old programs alive
	// and attached — for dead cgroups this is a resource leak; for live
	// cgroups it's load-bearing enforcement that must survive CP restarts.
	m.cleanupStaleLinks()

	spec, err := loadClawker()
	if err != nil {
		return fmt.Errorf("ebpf: loading collection spec: %w", err)
	}

	// Maps that intentionally do NOT survive across CP restarts. These
	// hold per-CP-lifetime queues / counters (events_ringbuf is the BPF→
	// userspace egress event queue; events_drops counts kernel-fault
	// drops produced during the same CP run). Pinning them would let
	// stale records from a previous (possibly different-ABI) CP boot
	// land in the new CP's netlogger reader — BPF_MAP_TYPE_RINGBUF
	// reports KeySize=ValueSize=0 so the schema-change check below
	// cannot detect the drift. Each CP boot creates a fresh in-memory
	// map; old pin files (from legacy CPs that pinned these) are
	// removed unconditionally so spec.LoadAndAssign doesn't collide.
	ephemeralMaps := map[string]bool{
		"events_ringbuf": true,
		"events_drops":   true,
	}
	for name, mapSpec := range spec.Maps {
		if ephemeralMaps[name] {
			mapSpec.Pinning = ebpf.PinNone
			pin := filepath.Join(m.pinPath, name)
			if err := os.Remove(pin); err == nil {
				m.log.Info().Str("map", name).Msg("removed legacy pin for ephemeral map")
			}
			continue
		}
		mapSpec.Pinning = ebpf.PinByName
	}

	// Remove stale pinned maps whose schema has changed (e.g., key size).
	// The BPF loader refuses to reuse a pinned map with incompatible specs.
	// Skip ephemeral maps — they're unpinned + cleared above.
	for name, mapSpec := range spec.Maps {
		if ephemeralMaps[name] {
			continue
		}
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
		"clawker_connect4":     m.objs.ClawkerConnect4,
		"clawker_sendmsg4":     m.objs.ClawkerSendmsg4,
		"clawker_recvmsg4":     m.objs.ClawkerRecvmsg4,
		"clawker_connect6":     m.objs.ClawkerConnect6,
		"clawker_sendmsg6":     m.objs.ClawkerSendmsg6,
		"clawker_recvmsg6":     m.objs.ClawkerRecvmsg6,
		"clawker_getpeername4": m.objs.ClawkerGetpeername4,
		"clawker_getpeername6": m.objs.ClawkerGetpeername6,
		"clawker_sock_create":  m.objs.ClawkerSockCreate,
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
	// events_ringbuf + events_drops are intentionally unpinned (see Load
	// comment on ephemeralMaps) — break-glass OpenPinned cannot reach
	// them. The netlogger pipeline consumes them via the in-process
	// EventsRingbuf() / EventsDrops() accessors instead.
	maps := map[string]**ebpf.Map{
		ContainerMapName:  &m.objs.ContainerMap,
		"bypass_map":      &m.objs.BypassMap,
		DNSCacheMapName:   &m.objs.DnsCache,
		"route_map":       &m.objs.RouteMap,
		"udp_flow_map":    &m.objs.UdpFlowMap,
		"metrics_map":     &m.objs.MetricsMap,
		"ratelimit_state": &m.objs.RatelimitState,
		"ratelimit_drops": &m.objs.RatelimitDrops,
	}
	for name, target := range maps {
		mp, err := ebpf.LoadPinnedMap(filepath.Join(m.pinPath, name), nil)
		if err != nil {
			return fmt.Errorf("ebpf: opening pinned map %s: %w", name, err)
		}
		*target = mp
	}

	progs := map[string]**ebpf.Program{
		"clawker_connect4":     &m.objs.ClawkerConnect4,
		"clawker_sendmsg4":     &m.objs.ClawkerSendmsg4,
		"clawker_recvmsg4":     &m.objs.ClawkerRecvmsg4,
		"clawker_connect6":     &m.objs.ClawkerConnect6,
		"clawker_sendmsg6":     &m.objs.ClawkerSendmsg6,
		"clawker_recvmsg6":     &m.objs.ClawkerRecvmsg6,
		"clawker_getpeername4": &m.objs.ClawkerGetpeername4,
		"clawker_getpeername6": &m.objs.ClawkerGetpeername6,
		"clawker_sock_create":  &m.objs.ClawkerSockCreate,
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

// CleanupAllLinks removes ALL pinned link files from the pin directory.
// Called by the daemon ONLY when no agent containers remain and the daemon
// is shutting down — this ensures the next container start gets a clean
// slate. Must NOT be called on health check failure, signal shutdown, or
// CP restart, because agent containers may still be running with active
// enforcement.
func (m *Manager) CleanupAllLinks() {
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

// cleanupStaleLinks removes pinned link files whose target cgroups no longer
// exist (dead containers). Links to live cgroups are preserved so their
// enforcement survives CP restarts. The cgroup ID is extracted from the
// pin filename (link_{prog}_{cgroupID}) and looked up in container_map —
// if the cgroup ID is absent from the map (or the map doesn't exist),
// the link is considered stale and removed.
func (m *Manager) cleanupStaleLinks() {
	entries, err := os.ReadDir(m.pinPath)
	if err != nil {
		return
	}

	// Collect unique cgroup IDs from link pin filenames.
	cgroupIDs := make(map[uint64]bool)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "link_") {
			continue
		}
		// Format: link_{prog}_{cgroupID}
		parts := strings.Split(e.Name(), "_")
		if len(parts) < 3 {
			continue
		}
		id, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
		if err != nil {
			continue
		}
		cgroupIDs[id] = true
	}

	if len(cgroupIDs) == 0 {
		return
	}

	// Check which cgroup IDs are still alive by trying to look them up
	// in the container_map (if it exists). A cgroup in container_map is
	// an active enforcement target — keep its links. Everything else is stale.
	liveIDs := make(map[uint64]bool)
	containerMap, err := ebpf.LoadPinnedMap(filepath.Join(m.pinPath, ContainerMapName), nil)
	if err == nil {
		defer containerMap.Close()
		for id := range cgroupIDs {
			var val clawkerContainerConfig
			if err := containerMap.Lookup(id, &val); err == nil {
				liveIDs[id] = true
			}
		}
	}
	// If container_map doesn't exist (first boot), all links are stale.

	// Remove links for dead cgroups.
	cleaned := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "link_") {
			continue
		}
		parts := strings.Split(e.Name(), "_")
		if len(parts) < 3 {
			continue
		}
		id, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
		if err != nil {
			continue
		}
		if liveIDs[id] {
			continue // live cgroup — keep link
		}

		pin := filepath.Join(m.pinPath, e.Name())
		l, lerr := link.LoadPinnedLink(pin, nil)
		if lerr == nil {
			l.Unpin()
			l.Close()
		} else {
			os.Remove(pin)
		}
		cleaned++
	}

	// Programs attached per cgroup — derived from the single source of truth.
	numBPFPrograms := len(m.cgroupAttachments())

	if cleaned > 0 {
		m.log.Info().Int("cleaned", cleaned).Int("kept", len(liveIDs)*numBPFPrograms).
			Msg("cleaned stale link pins from dead cgroups")
	}
}

// cleanupLinks removes stale pinned links for a cgroup ID.
// This ensures re-Install() attaches fresh programs, not stale ones from a previous run.
func (m *Manager) cleanupLinks(cgroupID uint64) {
	// Close in-memory links if we have them.
	if links, ok := m.links[cgroupID]; ok {
		for _, l := range links {
			l.Close()
		}
		delete(m.links, cgroupID)
	}

	// Remove pinned link files for this cgroup.
	for _, a := range m.cgroupAttachments() {
		name := a.name
		pin := filepath.Join(m.pinPath, fmt.Sprintf("link_%s_%d", name, cgroupID))
		os.Remove(pin)
	}
}

// CleanupStaleBypass removes entries from bypass_map whose cgroup IDs
// have no corresponding container_map entry. Called at CP startup
// (INV-B2-013) — without this pass, dead containers' cgroup IDs retain
// a bypass flag and a freshly created cgroup that reuses the same ID
// could inherit unrestricted egress. Returns the number of stale
// entries cleared and a joined error surfacing any per-entry failures
// so the caller can fail startup rather than silently leaving
// enforcement-relevant state in place.
func (m *Manager) CleanupStaleBypass() (int, error) {
	if m.objs.BypassMap == nil || m.objs.ContainerMap == nil {
		return 0, nil
	}

	var stale []uint64
	var errs []error
	var key uint64
	var val uint8
	iter := m.objs.BypassMap.Iterate()
	for iter.Next(&key, &val) {
		var cfg clawkerContainerConfig
		err := m.objs.ContainerMap.Lookup(key, &cfg)
		switch {
		case err == nil:
			// live container — keep bypass
		case errors.Is(err, ebpf.ErrKeyNotExist):
			stale = append(stale, key)
		default:
			m.log.Warn().
				Err(err).
				Uint64("cgroup_id", key).
				Msg("ebpf: container_map lookup failed; preserving bypass entry")
			errs = append(errs, fmt.Errorf("container_map[%d] lookup: %w", key, err))
		}
	}
	if err := iter.Err(); err != nil {
		errs = append(errs, fmt.Errorf("bypass_map iterate: %w", err))
	}

	cleared := 0
	for _, id := range stale {
		if err := m.objs.BypassMap.Delete(id); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			m.log.Warn().Err(err).Uint64("cgroup_id", id).Msg("ebpf: failed to clear stale bypass entry")
			errs = append(errs, fmt.Errorf("bypass_map[%d]: %w", id, err))
			continue
		}
		cleared++
	}
	if cleared > 0 {
		m.log.Info().Int("cleared", cleared).Msg("cleaned stale bypass_map entries")
	}
	return cleared, errors.Join(errs...)
}

// FlushAll clears all per-container/per-flow eBPF state: empties
// container_map, bypass_map, ratelimit_state, udp_flow_map, and
// ratelimit_drops, then unpins every link. Called ONLY on drain-to-zero
// shutdown (INV-B2-007) when no agents remain — this is the drain
// complement to CleanupAllLinks, which only touches pinned links.
//
// After FlushAll, the BPF maps contain no entries but the programs
// themselves stay loaded until Close is called; the caller drives the
// final teardown order (typically: FlushAll → Close).
//
// Returns a joined error surfacing all per-entry failures.
func (m *Manager) FlushAll() error {
	var errs []error

	if m.objs.ContainerMap != nil {
		var keys []uint64
		var key uint64
		var val clawkerContainerConfig
		iter := m.objs.ContainerMap.Iterate()
		for iter.Next(&key, &val) {
			keys = append(keys, key)
		}
		if err := iter.Err(); err != nil {
			errs = append(errs, fmt.Errorf("iterate container_map: %w", err))
		}
		for _, id := range keys {
			if err := m.objs.ContainerMap.Delete(id); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				errs = append(errs, fmt.Errorf("flush container_map[%d]: %w", id, err))
			}
		}
	}

	if m.objs.BypassMap != nil {
		var keys []uint64
		var key uint64
		var val uint8
		iter := m.objs.BypassMap.Iterate()
		for iter.Next(&key, &val) {
			keys = append(keys, key)
		}
		if err := iter.Err(); err != nil {
			errs = append(errs, fmt.Errorf("iterate bypass_map: %w", err))
		}
		for _, id := range keys {
			if err := m.objs.BypassMap.Delete(id); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				errs = append(errs, fmt.Errorf("flush bypass_map[%d]: %w", id, err))
			}
		}
	}

	// Drain ratelimit_state so a CP restart leaves each cgroup starting from
	// a full token bucket — matches drain-to-zero semantics for the other
	// per-cgroup maps. LRU eviction would eventually do this, but explicit
	// drain keeps the post-drain state deterministic.
	if m.objs.RatelimitState != nil {
		var keys []uint64
		var key uint64
		var val clawkerRatelimitStateVal
		iter := m.objs.RatelimitState.Iterate()
		for iter.Next(&key, &val) {
			keys = append(keys, key)
		}
		if err := iter.Err(); err != nil {
			errs = append(errs, fmt.Errorf("iterate ratelimit_state: %w", err))
		}
		for _, id := range keys {
			if err := m.objs.RatelimitState.Delete(id); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				errs = append(errs, fmt.Errorf("flush ratelimit_state[%d]: %w", id, err))
			}
		}
	}

	// Drain udp_flow_map (UDP reverse-NAT state). LRU eviction would eventually
	// clear it, but draining on drain-to-zero keeps the post-drain state
	// deterministic alongside the other per-flow maps.
	if m.objs.UdpFlowMap != nil {
		var keys []clawkerUdpFlowKey
		var key clawkerUdpFlowKey
		var val clawkerUdpFlowVal
		iter := m.objs.UdpFlowMap.Iterate()
		for iter.Next(&key, &val) {
			keys = append(keys, key)
		}
		if err := iter.Err(); err != nil {
			errs = append(errs, fmt.Errorf("iterate udp_flow_map: %w", err))
		}
		for _, k := range keys {
			if err := m.objs.UdpFlowMap.Delete(k); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				errs = append(
					errs,
					fmt.Errorf("flush udp_flow_map[cookie=%d,backend_port=%d]: %w", k.Cookie, k.BackendPort, err),
				)
			}
		}
	}

	// Drain ratelimit_drops so per-cgroup drop counters reset on CP restart.
	// Unlike ratelimit_state, ratelimit_drops is a plain BPF_MAP_TYPE_HASH
	// with no eviction — without this drain, drop counts from previous CP
	// lifetimes would accumulate indefinitely up to max_entries=256.
	// Operators expect "since the current CP started" semantics; carrying
	// stale counts across restarts would misattribute drops to fresh
	// containers reusing recycled cgroup IDs.
	if m.objs.RatelimitDrops != nil {
		var keys []uint64
		var key uint64
		var val uint64
		iter := m.objs.RatelimitDrops.Iterate()
		for iter.Next(&key, &val) {
			keys = append(keys, key)
		}
		if err := iter.Err(); err != nil {
			errs = append(errs, fmt.Errorf("iterate ratelimit_drops: %w", err))
		}
		for _, id := range keys {
			if err := m.objs.RatelimitDrops.Delete(id); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				errs = append(errs, fmt.Errorf("flush ratelimit_drops[%d]: %w", id, err))
			}
		}
	}

	m.CleanupAllLinks()
	return errors.Join(errs...)
}

// Close detaches all links and closes all programs and maps.
func (m *Manager) Close() error {
	m.linksMu.Lock()
	defer m.linksMu.Unlock()
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

// bypassMap is the minimal subset of *ebpf.Map used by clearBypass and the
// dns_cache GC helper. It exists purely as a test seam so BPF map delete
// behavior can be exercised without a live kernel + bpf2go objects. The
// production *ebpf.Map satisfies this interface via its Delete method —
// the same seam is reused for both BypassMap (uint64 keys) and DnsCache
// (uint32 keys) because Go's structural typing makes the concrete key
// type an implementation detail of the caller.
type bypassMap interface {
	Delete(key any) error
}

// clearBypass removes any lingering bypass entry for cgroupID. ErrKeyNotExist
// is the common/expected case and is treated as success. Any other error
// (EPERM, ENOMEM, EINVAL, ...) is logged at Warn level here so forensics are
// captured regardless of what the caller does, and returned so callers can
// choose whether to additionally surface or propagate it. The current callers
// (Enable, Disable) log-only — the bypass-clear is best-effort — but the
// return value is retained so a future caller can hard-fail if needed.
func clearBypass(m bypassMap, cgroupID uint64, log *logger.Logger) error {
	err := m.Delete(cgroupID)
	if err == nil || errors.Is(err, ebpf.ErrKeyNotExist) {
		return nil
	}
	if log != nil {
		log.Warn().Err(err).Uint64("cgroup_id", cgroupID).Msg("ebpf: failed to clear bypass flag (non-fatal)")
	}
	return err
}

// cgroupAttachment binds a program's pin/link name to its loaded program and
// cgroup attach type.
type cgroupAttachment struct {
	name string
	prog *ebpf.Program
	typ  ebpf.AttachType
}

// cgroupAttachments is the single source of truth for the set of cgroup BPF
// programs clawker attaches per container — Install attaches them, cleanupLinks
// and Remove unpin their links by name, and the count drives the cleanup log.
// Adding or removing a program is a one-line edit here (plus the two pin maps in
// Load/OpenPinned, which must bind struct-field addresses and can't derive).
func (m *Manager) cgroupAttachments() []cgroupAttachment {
	return []cgroupAttachment{
		{"connect4", m.objs.ClawkerConnect4, ebpf.AttachCGroupInet4Connect},
		{"sendmsg4", m.objs.ClawkerSendmsg4, ebpf.AttachCGroupUDP4Sendmsg},
		{"recvmsg4", m.objs.ClawkerRecvmsg4, ebpf.AttachCGroupUDP4Recvmsg},
		{"connect6", m.objs.ClawkerConnect6, ebpf.AttachCGroupInet6Connect},
		{"sendmsg6", m.objs.ClawkerSendmsg6, ebpf.AttachCGroupUDP6Sendmsg},
		{"recvmsg6", m.objs.ClawkerRecvmsg6, ebpf.AttachCGroupUDP6Recvmsg},
		{"getpeername4", m.objs.ClawkerGetpeername4, ebpf.AttachCgroupInet4GetPeername},
		{"getpeername6", m.objs.ClawkerGetpeername6, ebpf.AttachCgroupInet6GetPeername},
		{"sock_create", m.objs.ClawkerSockCreate, ebpf.AttachCGroupInetSockCreate},
	}
}

// Install attaches BPF programs to a container's cgroup and populates routing maps.
// Cleans up any stale links for this cgroup before attaching and clears any
// stale bypass flag so the container lands in a known enforced state.
func (m *Manager) Install(cgroupID uint64, cgroupPath string, cfg clawkerContainerConfig) error {
	m.linksMu.Lock()
	defer m.linksMu.Unlock()
	// Clean up stale links from previous Install() calls for this cgroup.
	// Stale links keep old programs attached, causing silent misbehavior.
	m.cleanupLinks(cgroupID)

	// Clear any lingering bypass flag so Enable() is a symmetric counterpart
	// to Disable() (which also deletes the bypass entry). Without this,
	// `firewall bypass --stop` (which calls Enable to re-enforce) would leave
	// BypassMap[cgroupID] = 1 in place and the BPF fast path would keep
	// bypassing enforcement. Non-ErrKeyNotExist failures are already logged
	// inside clearBypass; we discard the return here because Enable is
	// best-effort-symmetric with Disable and should not fail just because
	// the old bypass entry could not be cleaned up.
	_ = clearBypass(m.objs.BypassMap, cgroupID, m.log)

	if err := m.objs.ContainerMap.Update(cgroupID, cfg, ebpf.UpdateAny); err != nil {
		return fmt.Errorf("ebpf enable: updating container_map: %w", err)
	}

	var linked []link.Link
	for _, a := range m.cgroupAttachments() {
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

// Remove detaches BPF programs from a container's cgroup and removes map entries.
func (m *Manager) Remove(cgroupID uint64) error {
	m.linksMu.Lock()
	defer m.linksMu.Unlock()
	// Close in-memory links if we hold them.
	if linked, ok := m.links[cgroupID]; ok {
		for _, l := range linked {
			l.Close()
		}
		delete(m.links, cgroupID)
	}

	// Also unpin any persisted links for this cgroup.
	for _, a := range m.cgroupAttachments() {
		name := a.name
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
	// Reuse the same helper as Enable so bypass-clear error handling is
	// consistent everywhere. ErrKeyNotExist is the common case (container
	// never had a bypass set); non-ENOENT errors are Warn-logged but left
	// non-fatal so Disable still returns nil and the caller can proceed
	// with cleanup.
	_ = clearBypass(m.objs.BypassMap, cgroupID, m.log)

	// route_map is global (not per-container) — no cleanup needed here.

	m.log.Info().Uint64("cgroup_id", cgroupID).Msg("eBPF programs detached")
	return nil
}

// SyncRoutes replaces the global route_map with the given routes.
// Called by the firewall manager whenever egress rules change (add/remove/reload).
// All enforced containers immediately see the updated routes.
//
// Per-iteration Delete/Update failures are logged at Warn level for forensics
// and collected into a single joined error. When every operation succeeds the
// returned error is nil (errors.Join of an empty slice returns nil). Previously
// these errors were silently discarded and a partial sync returned success.
func (m *Manager) SyncRoutes(routes []Route) error {
	var errs []error

	// Clear existing routes.
	var keysToDelete []clawkerRouteKey
	var rk clawkerRouteKey
	var rv clawkerRouteVal
	iter := m.objs.RouteMap.Iterate()
	for iter.Next(&rk, &rv) {
		keysToDelete = append(keysToDelete, rk)
	}
	if err := iter.Err(); err != nil {
		errs = append(errs, fmt.Errorf("iterate route_map: %w", err))
	}
	for _, k := range keysToDelete {
		if err := m.objs.RouteMap.Delete(k); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			m.log.Warn().Err(err).
				Uint32("identity", k.Identity).
				Uint16("dst_port", k.DstPort).
				Uint8("l4_proto", k.L4Proto).
				Msg("ebpf sync-routes: deleting stale route_map entry")
			errs = append(errs, fmt.Errorf("delete route_map[identity=%d, dst_port=%d, l4_proto=%d]: %w",
				k.Identity, k.DstPort, k.L4Proto, err))
		}
	}

	// Populate with new routes.
	for _, r := range routes {
		key := clawkerRouteKey{
			// Kernel-map encode boundary: the generated bpf2go key carries
			// the raw on-wire uint32.
			Identity: uint32(r.Identity),
			DstPort:  r.DstPort,
			L4Proto:  r.L4Proto,
		}
		val := clawkerRouteVal{EnvoyPort: r.EnvoyPort}
		if err := m.objs.RouteMap.Update(key, val, ebpf.UpdateAny); err != nil {
			m.log.Warn().Err(err).
				Uint32("identity", uint32(r.Identity)).
				Uint16("dst_port", r.DstPort).
				Uint8("l4_proto", r.L4Proto).
				Msg("ebpf sync-routes: updating route_map")
			errs = append(
				errs,
				fmt.Errorf(
					"update route_map[identity=%d, dst_port=%d, l4_proto=%d]: %w",
					r.Identity,
					r.DstPort,
					r.L4Proto,
					err,
				),
			)
		}
	}

	// Seed dns_cache for IP-literal routes. The agent connects to a bare IP,
	// which CoreDNS never resolved, so connect4/sendmsg4's dns_cache lookup would
	// miss and the datagram would be denied. Writing dns_cache[ip]=Identity
	// (the same identity the route is keyed on) makes the existing lookup hit. FQDN
	// routes carry SeedIP==0 — CoreDNS populates their dns_cache entry.
	//
	// dns_cache is keyed by destination IP and has two writers: this seed and
	// the CoreDNS dnsbpf plugin (which writes dns_cache[ip] on every A record).
	// If an operator configures both an FQDN rule and a bare-IP rule for an IP
	// the FQDN resolves to, both writers contend for that one key. Precedence
	// is by source tag (cilium ipcache source-precedence analog): the seed
	// writes DNSSourceSeed and wins — it represents an explicit operator rule
	// whose route must hold regardless of DNS churn, so dnsbpf skips any key
	// tagged DNSSourceSeed and GarbageCollectDNS never evicts one. Both rules
	// still route to valid Envoy listeners either way; the contested IP's
	// access-log attribution follows the seeded rule. The seed's sole removal
	// path is the orphan diff below, when the IP rule is removed.
	seeded := 0
	for _, r := range routes {
		if r.SeedIP == 0 {
			continue
		}
		if err := m.seedDNSCache(r.SeedIP, r.Identity, dnsSeedTTLSeconds); err != nil {
			m.log.Warn().Err(err).
				Uint32("seed_ip", r.SeedIP).
				Uint32("identity", uint32(r.Identity)).
				Msg("ebpf sync-routes: seeding dns_cache for IP rule")
			errs = append(errs, fmt.Errorf("seed dns_cache[ip=%d, identity=%d]: %w", r.SeedIP, r.Identity, err))
			continue
		}
		seeded++
	}

	// Update the protected-seed set and remove dns_cache entries for IP rules
	// that went away. Seed entries carry DNSSourceSeed, which GarbageCollectDNS
	// never evicts and the dnsbpf plugin refuses to overwrite — this orphan
	// delete is their sole removal path, so a dropped delete would pin the
	// stale entry forever (denying that IP's allowed traffic, or aliasing a
	// reissued identity). A co-resident FQDN rule for the same IP just
	// re-resolves through CoreDNS on its next query.
	m.seedMu.Lock()
	next, seedErrs := reconcileSeeds(m.objs.DnsCache, routes, m.seededIPs, m.log)
	m.seededIPs = next
	m.seedMu.Unlock()
	errs = append(errs, seedErrs...)

	m.log.Info().
		Int("routes", len(routes)).
		Int("ip_seeds", seeded).
		Int("errors", len(errs)).
		Msg("global route_map synced")
	return errors.Join(errs...)
}

// diffSeededIPs derives, from a SyncRoutes call's routes and the previously
// seeded IP set, the new seed set and the IPs that were seeded before but are no
// longer present (orphans whose dns_cache entries should be removed). Pure so the
// seed-lifecycle logic is unit-testable without a kernel.
func diffSeededIPs(routes []Route, prev map[uint32]struct{}) (next map[uint32]struct{}, orphaned []uint32) {
	next = make(map[uint32]struct{})
	for _, r := range routes {
		if r.SeedIP != 0 {
			next[r.SeedIP] = struct{}{}
		}
	}
	for ip := range prev {
		if _, still := next[ip]; !still {
			orphaned = append(orphaned, ip)
		}
	}
	return next, orphaned
}

// reconcileSeeds computes the next tracked-seed set for a SyncRoutes call and
// deletes dns_cache entries for orphaned IP-literal seeds. ErrKeyNotExist
// counts as success (the entry is already gone). An IP whose delete fails any
// other way stays in the returned set: seed entries are exempt from GC
// eviction, so this delete is their only removal path — retaining the IP makes
// the next reconcile's diff list it as an orphan again and retry. Each failure
// is also returned so SyncRoutes surfaces a non-nil error to the caller.
func reconcileSeeds(
	dnsCache bypassMap,
	routes []Route,
	prev map[uint32]struct{},
	log *logger.Logger,
) (map[uint32]struct{}, []error) {
	var errs []error
	next, orphaned := diffSeededIPs(routes, prev)
	for _, ip := range orphaned {
		err := dnsCache.Delete(ip)
		if err == nil || errors.Is(err, ebpf.ErrKeyNotExist) {
			continue
		}
		if log != nil {
			log.Warn().Err(err).
				Str("event", "dns_seed_orphan_delete_failed").
				Uint32("ip", ip).
				Msg("ebpf sync-routes: removing orphan dns_cache seed")
		}
		next[ip] = struct{}{}
		errs = append(errs, fmt.Errorf("delete orphan dns_cache seed[ip=%d]: %w", ip, err))
	}
	return next, errs
}

// Disable sets the bypass flag for a container, allowing unrestricted egress.
func (m *Manager) Disable(cgroupID uint64) error {
	val := uint8(1)
	if err := m.objs.BypassMap.Update(cgroupID, val, ebpf.UpdateAny); err != nil {
		return fmt.Errorf("ebpf bypass: %w", err)
	}
	m.log.Info().Uint64("cgroup_id", cgroupID).Msg("eBPF bypass enabled")
	return nil
}

// Enable removes the bypass flag, restoring firewall enforcement.
func (m *Manager) Enable(cgroupID uint64) error {
	if err := m.objs.BypassMap.Delete(cgroupID); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		return fmt.Errorf("ebpf unbypass: %w", err)
	}
	m.log.Info().Uint64("cgroup_id", cgroupID).Msg("eBPF bypass disabled")
	return nil
}

// dnsExpiryTS returns the wall-clock expiry for a TTL, saturating at
// MaxUint32 so the int64→uint32 narrowing can never wrap (expire_ts is a
// u32 on the BPF side).
func dnsExpiryTS(ttlSeconds uint32) uint32 {
	exp := time.Now().Unix() + int64(ttlSeconds)
	if exp <= 0 || exp > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(exp)
}

// UpdateDNSCache writes a DNS resolution result to the dns_cache map,
// overwriting any existing entry for the IP (last-writer-wins). Used by the
// break-glass ebpf-manager CLI, where a manual override is the intent. The
// entry is tagged DNSSourceDNS, so the dnsbpf plugin may later overwrite it
// on a fresh resolution and GC expires it at its TTL like any DNS entry.
func (m *Manager) UpdateDNSCache(ip uint32, identity RouteIdentity, ttlSeconds uint32) error {
	entry := clawkerDnsEntry{
		// Kernel-map encode boundary: raw on-wire uint32.
		Identity: uint32(identity),
		ExpireTs: dnsExpiryTS(ttlSeconds),
		Source:   DNSSourceDNS,
	}
	if err := m.objs.DnsCache.Update(ip, entry, ebpf.UpdateAny); err != nil {
		return fmt.Errorf("ebpf dns-update: %w", err)
	}
	return nil
}

// seedDNSCache writes a DNSSourceSeed dns_cache entry for an IP-literal
// route, overwriting any DNS-derived entry for the same IP — seeds take
// precedence (see the two-writer contention note in SyncRoutes). The dnsbpf
// plugin refuses to overwrite a DNSSourceSeed entry, so the seed holds until
// SyncRoutes' orphan diff removes it.
func (m *Manager) seedDNSCache(ip uint32, identity RouteIdentity, ttlSeconds uint32) error {
	entry := clawkerDnsEntry{
		// Kernel-map encode boundary: raw on-wire uint32.
		Identity: uint32(identity),
		ExpireTs: dnsExpiryTS(ttlSeconds),
		Source:   DNSSourceSeed,
	}
	if err := m.objs.DnsCache.Update(ip, entry, ebpf.UpdateAny); err != nil {
		return fmt.Errorf("ebpf seed dns_cache: %w", err)
	}
	return nil
}

// deleteExpiredDNSEntries clears the given keys from a BPF map, returning the
// number of entries actually cleared and the number whose Delete failed for a
// real reason. ErrKeyNotExist counts as cleared (end-state matches intent —
// entry is gone, usually because another actor raced us, e.g. the dnsbpf
// CoreDNS plugin rewrote the entry between Iterate and Delete). Real delete
// failures (EPERM, ENOMEM, ...) are logged at Debug level and counted as
// failed, NOT cleared, so cleared is an honest "entries removed" metric and
// failed lets the caller detect a sweep that enumerated work but reclaimed
// nothing. Split out from GarbageCollectDNS for unit testability.
func deleteExpiredDNSEntries(m bypassMap, keys []uint32, log *logger.Logger) (cleared, failed int, firstErr error) {
	for _, key := range keys {
		err := m.Delete(key)
		if err == nil || errors.Is(err, ebpf.ErrKeyNotExist) {
			cleared++
			continue
		}
		failed++
		if firstErr == nil {
			firstErr = err // surface the cause (EPERM/ENOMEM/...), not just a count
		}
		if log != nil {
			log.Debug().Err(err).Uint32("ip", key).Msg("ebpf gc-dns: deleting expired dns_cache entry (non-fatal)")
		}
	}
	return cleared, failed, firstErr
}

// dnsEntryEvictable reports whether a dns_cache entry may be evicted by GC at
// wall-clock now. Three vetoes, in order:
//   - not yet expired;
//   - DNSSourceSeed — IP-literal rule seeds are removed by SyncRoutes' orphan
//     diff when the rule goes away, never by TTL expiry. The source tag in the
//     map value (not the in-memory seededIPs set) carries this, so the
//     protection holds across a CP restart, before the first SyncRoutes;
//   - a live UDP flow to the IP (cilium's zombie-DNS analog: tofqdns zombie
//     tracking keeps a name→IP mapping alive while connections persist). The
//     BPF sendmsg4 path looks dns_cache up on every unconnected datagram, so
//     evicting an expired entry mid-QUIC-stream would deny the flow's next
//     datagram. The entry stays until the flow leaves udp_flow_map (LRU
//     eviction of the idle flow), then the next sweep reclaims it.
//
// Pure — split out from GarbageCollectDNS for unit testability.
func dnsEntryEvictable(ip uint32, entry clawkerDnsEntry, now uint32, liveUDP map[uint32]struct{}) bool {
	if entry.ExpireTs >= now {
		return false
	}
	if entry.Source == DNSSourceSeed {
		return false
	}
	if _, live := liveUDP[ip]; live {
		return false
	}
	return true
}

// liveUDPDstIPs walks udp_flow_map and returns the set of original
// destination IPs (network byte order — the same representation dns_cache is
// keyed by) that have reverse-NAT state, i.e. a UDP flow the kernel still
// considers live. udp_flow_map is LRU, so abandoned flows age out without a
// userspace sweep and stop vetoing eviction on their own.
func (m *Manager) liveUDPDstIPs() (map[uint32]struct{}, error) {
	live := make(map[uint32]struct{})
	var k clawkerUdpFlowKey
	var v clawkerUdpFlowVal
	iter := m.objs.UdpFlowMap.Iterate()
	for iter.Next(&k, &v) {
		live[v.OrigDstIp] = struct{}{}
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("ebpf: iterating udp_flow_map: %w", err)
	}
	return live, nil
}

// GarbageCollectDNS removes expired entries from the dns_cache map and
// returns (cleared, err): cleared is the number of entries actually evicted,
// and err is non-nil (via joinDNSGCErrors) when the sweep could not reclaim —
// the map could not be enumerated (wedged iterator) or an expired entry could
// not be deleted for a real reason. A clean sweep that reclaims zero entries
// still returns nil. The non-nil err lets the CP main loop's degraded-GC
// detector trip on a persistently wedged sweep rather than mistaking a no-op
// pass for progress. Individual delete failures are also logged at Debug and
// the next GC pass retries them. Two classes of expired entry are spared:
// DNSSourceSeed entries (IP-literal rule seeds, lifecycle owned by
// SyncRoutes) and entries whose IP still has a live flow in udp_flow_map
// (cilium's zombie-DNS analog — see dnsEntryEvictable).
func (m *Manager) GarbageCollectDNS() (cleared int, err error) {
	now := uint32(time.Now().Unix())

	// Snapshot the set of original destination IPs with live UDP flows.
	// Liveness is the eviction veto for expired entries; if the flow map
	// can't be enumerated the veto set is unknown, so the sweep evicts
	// nothing rather than risk killing a mid-stream flow.
	liveUDP, flowErr := m.liveUDPDstIPs()
	if flowErr != nil {
		m.log.Warn().
			Err(flowErr).
			Msg("ebpf gc-dns: iterating udp_flow_map — skipping this sweep (next pass will retry)")
		return 0, joinDNSGCErrors(flowErr, 0, nil)
	}

	var ip uint32
	var entry clawkerDnsEntry
	var expired []uint32
	zombies := 0

	iter := m.objs.DnsCache.Iterate()
	for iter.Next(&ip, &entry) {
		if !dnsEntryEvictable(ip, entry, now, liveUDP) {
			if entry.ExpireTs < now && entry.Source != DNSSourceSeed {
				zombies++
			}
			continue
		}
		expired = append(expired, ip)
	}
	if zombies > 0 {
		m.log.Debug().Int("zombies", zombies).Msg("ebpf gc-dns: sparing expired dns_cache entries with live UDP flows")
	}

	iterErr := iter.Err()
	if iterErr != nil {
		// Iterator failure means we could not even enumerate the map, so this
		// sweep reclaimed nothing. It is surfaced (via joinDNSGCErrors) so the
		// caller's degraded-GC detector trips on a persistently wedged iterator
		// rather than treating the failed pass as a success.
		m.log.Warn().Err(iterErr).Msg("ebpf gc-dns: iterating dns_cache (next pass will retry)")
	}

	cleared, failed, firstDeleteErr := deleteExpiredDNSEntries(m.objs.DnsCache, expired, m.log)

	return cleared, joinDNSGCErrors(iterErr, failed, firstDeleteErr)
}

// joinDNSGCErrors assembles the error a single GarbageCollectDNS sweep reports
// from the two independent ways a sweep can fail to reclaim. Either alone is
// enough to fail the sweep so the caller's degraded-GC detector trips:
//   - iterErr != nil: the map could not be enumerated, so the sweep reclaimed
//     nothing (a wedged iterator).
//   - failed > 0: expired, eligible entries were found but at least one Delete
//     failed for a real reason (EPERM, ENOMEM, ...), so the map is not shrinking.
//
// A clean sweep (no iterator error, nothing failed) returns nil even when it
// reclaimed zero entries — "swept nothing because nothing had expired" is
// success, not failure, and must not count toward escalation.
func joinDNSGCErrors(iterErr error, failed int, firstDeleteErr error) error {
	var errs []error
	if iterErr != nil {
		errs = append(errs, fmt.Errorf("iterate dns_cache: %w", iterErr))
	}
	if failed > 0 {
		if firstDeleteErr != nil {
			errs = append(errs, fmt.Errorf("%d dns_cache delete(s) failed (first: %w)", failed, firstDeleteErr))
		} else {
			errs = append(errs, fmt.Errorf("%d dns_cache delete(s) failed", failed))
		}
	}
	return errors.Join(errs...)
}

// LookupContainer returns the container_map entry for a given cgroup ID.
func (m *Manager) LookupContainer(cgroupID uint64) (clawkerContainerConfig, error) {
	var cfg clawkerContainerConfig
	err := m.objs.ContainerMap.Lookup(cgroupID, &cfg)
	return cfg, err
}

// EventsRingbuf returns the in-process events_ringbuf map handle.
// Intentionally unpinned (per-CP-lifetime queue) — break-glass
// OpenPinned cannot reach this map; the netlogger reader in-process
// is the sole consumer. Read-only: netlogger uses ringbuf.NewReader
// on the returned handle to drain egress event records emitted by the
// cgroup BPF programs. Returns nil before Load has been called.
func (m *Manager) EventsRingbuf() *ebpf.Map {
	return m.objs.EventsRingbuf
}

// EventsDrops returns the in-process events_drops PERCPU_ARRAY map
// handle. Intentionally unpinned (per-CP-lifetime counter). Userspace
// reads key=0 and sums across CPUs to surface kernel-fault drop counts
// (bpf_ringbuf_reserve returning NULL). Returns nil before Load.
func (m *Manager) EventsDrops() *ebpf.Map {
	return m.objs.EventsDrops
}

// RatelimitDrops returns the pinned ratelimit_drops HASH map handle.
// Userspace iterates {cgroup_id -> drop count} to attribute intentional
// rate-limit drops to specific noisy agents.
func (m *Manager) RatelimitDrops() *ebpf.Map {
	return m.objs.RatelimitDrops
}

// DNSCache returns the pinned dns_cache map handle. Exposed so the
// netlogger reverse-DNS map can iterate {IPv4 -> identity} entries
// without re-opening the pinned file. Read-only access pattern.
func (m *Manager) DNSCache() *ebpf.Map {
	return m.objs.DnsCache
}

// ContainerEntry pairs a cgroup ID with its container_map config.
// Used by DumpContainers for break-glass introspection.
type ContainerEntry struct {
	CgroupID uint64          `json:"cgroup_id"`
	Config   ContainerConfig `json:"config"`
}

// BypassEntry pairs a cgroup ID with its bypass_map state.
type BypassEntry struct {
	CgroupID uint64 `json:"cgroup_id"`
	Bypass   bool   `json:"bypass"`
}

// DNSCacheEntry mirrors one dns_cache map entry: an IPv4 address
// (network byte order — matches ctx->user_ip4 in the BPF connect hook
// and the ContainerConfig IP fields), its route identity, and
// wall-clock expiration.
type DNSCacheEntry struct {
	IP       net.IP        `json:"ip"`
	Identity RouteIdentity `json:"identity"`
	ExpireTS uint32        `json:"expireTs"`
	Source   uint8         `json:"source"` // DNSSource* write-precedence tag
}

// DumpRoutes returns every entry in the global route_map.
// Used by the break-glass ebpf-manager dump-routes subcommand and by
// future control-plane introspection RPCs to verify that the BPF route
// table reflects what the rules store says.
//
// On iteration failure, returns (nil, err) — partial slices are never
// returned because operators (and future RPC consumers) would
// otherwise be unable to distinguish "the map has N entries" from
// "iteration broke after N of M entries", and silent truncation
// during incident response leads to the wrong conclusion.
func (m *Manager) DumpRoutes() ([]Route, error) {
	out := make([]Route, 0)
	var k clawkerRouteKey
	var v clawkerRouteVal
	iter := m.objs.RouteMap.Iterate()
	for iter.Next(&k, &v) {
		out = append(out, Route{
			// Kernel-map decode boundary: bpf2go key carries the raw uint32.
			Identity:  RouteIdentity(k.Identity),
			DstPort:   k.DstPort,
			EnvoyPort: v.EnvoyPort,
			L4Proto:   k.L4Proto,
		})
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("ebpf: iterating route_map: %w", err)
	}
	return out, nil
}

// DumpContainers returns every entry in container_map. Returns
// (nil, err) on iteration failure — see DumpRoutes for rationale.
func (m *Manager) DumpContainers() ([]ContainerEntry, error) {
	out := make([]ContainerEntry, 0)
	var cgroupID uint64
	var cfg clawkerContainerConfig
	iter := m.objs.ContainerMap.Iterate()
	for iter.Next(&cgroupID, &cfg) {
		out = append(out, ContainerEntry{
			CgroupID: cgroupID,
			Config: ContainerConfig{
				EnvoyIP:       cfg.EnvoyIp,
				CoreDNSIP:     cfg.CorednsIp,
				GatewayIP:     cfg.GatewayIp,
				NetAddr:       cfg.NetAddr,
				NetMask:       cfg.NetMask,
				HostProxyIP:   cfg.HostProxyIp,
				HostProxyPort: cfg.HostProxyPort,
				EgressPort:    cfg.EgressPort,
			},
		})
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("ebpf: iterating container_map: %w", err)
	}
	return out, nil
}

// DumpBypass returns every entry in bypass_map. Returns (nil, err) on
// iteration failure — see DumpRoutes for rationale.
func (m *Manager) DumpBypass() ([]BypassEntry, error) {
	out := make([]BypassEntry, 0)
	var cgroupID uint64
	var flag uint8
	iter := m.objs.BypassMap.Iterate()
	for iter.Next(&cgroupID, &flag) {
		out = append(out, BypassEntry{
			CgroupID: cgroupID,
			Bypass:   flag != 0,
		})
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("ebpf: iterating bypass_map: %w", err)
	}
	return out, nil
}

// DumpDNS returns every entry in dns_cache. Returns (nil, err) on
// iteration failure — see DumpRoutes for rationale.
func (m *Manager) DumpDNS() ([]DNSCacheEntry, error) {
	out := make([]DNSCacheEntry, 0)
	var ip uint32
	var entry clawkerDnsEntry
	iter := m.objs.DnsCache.Iterate()
	for iter.Next(&ip, &entry) {
		out = append(out, DNSCacheEntry{
			IP: Uint32ToIP(ip),
			// Kernel-map decode boundary: bpf2go entry carries the raw uint32.
			Identity: RouteIdentity(entry.Identity),
			ExpireTS: entry.ExpireTs,
			Source:   entry.Source,
		})
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("ebpf: iterating dns_cache: %w", err)
	}
	return out, nil
}

// Route describes a per-domain egress route for a container, identified by
// route identity. L4Proto (L4ProtoTCP/L4ProtoUDP) selects the transport so TCP
// and UDP routes for the same {domain, port} stay independent in the route_map.
type Route struct {
	Identity  RouteIdentity `json:"identity"`
	DstPort   uint16        `json:"dstPort"`
	EnvoyPort uint16        `json:"envoyPort"`
	L4Proto   uint8         `json:"l4Proto"`
	// SeedIP, when non-zero, is a literal IPv4 dst (network byte order) whose
	// dns_cache entry SyncRoutes must seed (dns_cache[SeedIP]=Identity) so the
	// connect4/sendmsg4 lookup hits. The agent connects to the bare IP, which
	// CoreDNS never resolved, so without the seed the lookup misses. Zero for
	// FQDN routes (CoreDNS populates dns_cache on resolution).
	SeedIP uint32 `json:"seed_ip,omitempty"`
}

// dnsSeedTTLSeconds is the TTL for SyncRoutes-seeded IP-literal dns_cache
// entries. The value is nominal: seeds carry DNSSourceSeed, which
// GarbageCollectDNS never evicts regardless of expire_ts — their sole removal
// path is SyncRoutes' orphan diff when the IP rule goes away. A far-future
// TTL keeps break-glass dumps honest (the entry is not "expired") rather
// than providing the protection itself.
const dnsSeedTTLSeconds uint32 = 365 * 24 * 3600

// NewContainerConfig builds a BPF container_config from network parameters.
func NewContainerConfig(envoyIP, corednsIP, gatewayIP, cidr string,
	hostProxyIP string, hostProxyPort, egressPort uint16,
) (clawkerContainerConfig, error) {
	netAddr, netMask, err := CIDRToAddrMask(cidr)
	if err != nil {
		return clawkerContainerConfig{}, fmt.Errorf("parsing CIDR %s: %w", cidr, err)
	}

	envoy, err := parseIP(envoyIP)
	if err != nil {
		return clawkerContainerConfig{}, fmt.Errorf("envoyIP: %w", err)
	}
	coredns, err := parseIP(corednsIP)
	if err != nil {
		return clawkerContainerConfig{}, fmt.Errorf("corednsIP: %w", err)
	}
	gateway, err := parseIP(gatewayIP)
	if err != nil {
		return clawkerContainerConfig{}, fmt.Errorf("gatewayIP: %w", err)
	}

	cfg := clawkerContainerConfig{
		EnvoyIp:       IPToUint32(envoy),
		CorednsIp:     IPToUint32(coredns),
		GatewayIp:     IPToUint32(gateway),
		NetAddr:       netAddr,
		NetMask:       netMask,
		HostProxyPort: hostProxyPort,
		EgressPort:    egressPort,
	}
	if hostProxyIP != "" {
		hp, err := parseIP(hostProxyIP)
		if err != nil {
			return clawkerContainerConfig{}, fmt.Errorf("hostProxyIP: %w", err)
		}
		cfg.HostProxyIp = IPToUint32(hp)
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
