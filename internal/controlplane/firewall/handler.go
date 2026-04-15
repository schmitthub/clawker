package firewall

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/config"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ContainerResolver looks up a container reference (name, short ID, or
// canonical long ID) against Docker and returns its canonical ID, the
// BPF-attachable cgroup path, and whether Docker still knows the container.
//
// A NotFound result MUST come back as (_, _, false, nil) — a nil error with
// exists=false — so callers can distinguish "container is gone" from "we
// couldn't talk to Docker". A real Docker API failure surfaces as err.
type ContainerResolver func(ctx context.Context, ref string) (id, cgroupPath string, exists bool, err error)

// StackLifecycle is the subset of *Stack operations the Handler drives.
// Declared here rather than as a method set on *Stack alone so tests can
// swap in a lightweight fake — Handler orchestrates Envoy+CoreDNS
// lifecycle as part of FirewallInit/Remove/Reload/Status/AddRules/
// RemoveRules, and unit tests should not have to spin up real containers
// to cover the RPC surface.
type StackLifecycle interface {
	EnsureRunning(ctx context.Context) error
	Stop(ctx context.Context) error
	Reload(ctx context.Context) error
	Status(ctx context.Context) (*Status, error)
	NetworkInfo(ctx context.Context) (*NetworkInfo, error)
}

var _ StackLifecycle = (*Stack)(nil)

// Handler implements adminv1.AdminServiceServer for the firewall domain.
// It owns the CP-side orchestration for the 13 Firewall* RPCs:
//
//   - Global lifecycle: FirewallInit, FirewallRemove.
//   - Per-container enforcement: FirewallEnable, FirewallDisable,
//     FirewallBypass. Each resolves the container's current cgroup path
//     via Docker with INV-B2-016 drift guard.
//   - Rules: FirewallAddRules, FirewallRemoveRules, FirewallListRules,
//     FirewallReload. Mutations go through the rules store and hot-reload
//     the Envoy+CoreDNS stack.
//   - Introspection/utilities: FirewallStatus, FirewallRotateCA,
//     FirewallSyncRoutes, FirewallResolveHostname.
type Handler struct {
	adminv1.UnimplementedAdminServiceServer

	ebpf     ebpf.EBPFManager
	stack    StackLifecycle
	store    *storage.Store[EgressRulesFile]
	cfg      config.Config
	resolve  ContainerResolver
	log      *logger.Logger
	certDirF func() (string, error)

	// resolveHostFn is injectable for tests. nil defaults to
	// net.DefaultResolver.LookupHost.
	resolveHostFn func(ctx context.Context, host string) ([]string, error)

	// cgroupIDFn reads the cgroup_id for a cgroupfs path. Injectable for
	// tests — the production path stats a real /sys/fs/cgroup/ inode,
	// which doesn't exist on macOS dev hosts. Defaults to ebpf.CgroupID.
	cgroupIDFn func(cgroupPath string) (uint64, error)

	// bypassTimersMu guards bypassTimers. Bypass starts/stops entries on
	// RPC calls; bypassTimerFired goroutines clean up on timer expiry.
	bypassTimersMu sync.Mutex
	bypassTimers   map[string]*bypassEntry // keyed by container_id

	// cgroupIDMu guards storedCgroupID, the last-known container_id →
	// cgroup_id mapping used for INV-B2-016 drift detection.
	cgroupIDMu     sync.Mutex
	storedCgroupID map[string]uint64
}

// bypassEntry holds the state a bypass timer needs on fire: the
// container_id for a fresh drift-guarded lookup against Docker, and the
// cgroup_id at bypass-time as the fail-closed fallback when Docker is
// unreachable.
type bypassEntry struct {
	timer       *time.Timer
	containerID string
	cgroupID    uint64
}

// HandlerDeps bundles the collaborators Handler needs. Using a deps
// struct keeps the constructor stable as future domain handlers grow.
type HandlerDeps struct {
	EBPF     ebpf.EBPFManager
	Stack    StackLifecycle
	Store    *storage.Store[EgressRulesFile]
	Cfg      config.Config
	Resolver ContainerResolver
	Log      *logger.Logger

	// CertDirFn optionally overrides FirewallCertSubdir resolution —
	// tests pass a temp dir so RotateCA does not touch the real data path.
	// nil defaults to cfg.FirewallCertSubdir.
	CertDirFn func() (string, error)
}

// maxBypassTimeout caps how long a single FirewallBypass call can
// suppress enforcement. Any caller that needs a longer window must
// repeat the call — keeps a single lost-CLI bypass from persisting for
// days.
const maxBypassTimeout = time.Hour

// NewHandler wires a firewall Handler. Panics on missing EBPF or
// Resolver — they are hit by every RPC and a nil value there would
// surface as a confusing nil-deref deep inside the gRPC interceptor
// chain. Stack / Store / Cfg are optional at construction so tests that
// only exercise the ebpf-backed RPCs can skip them; calls that need them
// fall through to a panic at the first nil access with a clear line
// number.
func NewHandler(deps HandlerDeps) *Handler {
	switch {
	case deps.EBPF == nil:
		panic("firewall: NewHandler requires a non-nil EBPFManager")
	case deps.Resolver == nil:
		panic("firewall: NewHandler requires a non-nil ContainerResolver")
	}
	log := deps.Log
	if log == nil {
		log = logger.Nop()
	}
	certDirFn := deps.CertDirFn
	if certDirFn == nil && deps.Cfg != nil {
		certDirFn = deps.Cfg.FirewallCertSubdir
	}
	return &Handler{
		ebpf:           deps.EBPF,
		stack:          deps.Stack,
		store:          deps.Store,
		cfg:            deps.Cfg,
		resolve:        deps.Resolver,
		log:            log,
		certDirF:       certDirFn,
		cgroupIDFn:     ebpf.CgroupID,
		bypassTimers:   make(map[string]*bypassEntry),
		storedCgroupID: make(map[string]uint64),
	}
}

// --- Global lifecycle ---

// FirewallInit brings the firewall stack (Envoy + CoreDNS) up. BPF
// programs are loaded once at CP startup via ebpf.Manager.Load; this RPC
// is the idempotent "stack up" signal from the CLI.
func (h *Handler) FirewallInit(ctx context.Context, _ *adminv1.FirewallInitRequest) (*adminv1.FirewallInitResponse, error) {
	if err := h.stack.EnsureRunning(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "firewall init: %v", err)
	}
	st, err := h.stack.Status(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "firewall init: status: %v", err)
	}
	return &adminv1.FirewallInitResponse{
		EnvoyIp:   st.EnvoyIP,
		CorednsIp: st.CoreDNSIP,
		NetworkId: st.NetworkID,
	}, nil
}

// FirewallRemove is global teardown: cancel bypass timers, stop the stack,
// flush all per-container eBPF state, and wipe the rules store. Errors
// aggregate rather than short-circuiting so partial teardown still makes
// progress on the uncorrelated subsystems. The rules-store Write is
// chained off Set: a Set failure means the in-memory mutation aborted,
// so the on-disk file is still the pre-Remove baseline and Write would
// only re-stamp stale state.
func (h *Handler) FirewallRemove(ctx context.Context, _ *adminv1.FirewallRemoveRequest) (*adminv1.FirewallRemoveResponse, error) {
	h.CancelAllBypassTimers()

	var errs []error
	if err := h.stack.Stop(ctx); err != nil {
		errs = append(errs, fmt.Errorf("stack stop: %w", err))
	}
	if err := h.ebpf.FlushAll(); err != nil {
		errs = append(errs, fmt.Errorf("ebpf flush: %w", err))
	}
	if err := h.store.Set(func(f *EgressRulesFile) { f.Rules = nil }); err != nil {
		errs = append(errs, fmt.Errorf("clear rules: %w", err))
	} else if err := h.store.Write(); err != nil {
		errs = append(errs, fmt.Errorf("write rules: %w", err))
	}

	h.cgroupIDMu.Lock()
	h.storedCgroupID = make(map[string]uint64)
	h.cgroupIDMu.Unlock()

	if len(errs) > 0 {
		return nil, status.Errorf(codes.Internal, "firewall remove: %v", errors.Join(errs...))
	}
	return &adminv1.FirewallRemoveResponse{}, nil
}

// --- Per-container enforcement ---

// FirewallEnable enrolls a container into container_map. Idempotent and
// drift-guarded (INV-B2-016): every call resolves container_id → fresh
// cgroup_path via Docker, logs a warning on cgroup_id drift, and returns
// FailedPrecondition if Docker says the container is gone.
//
// The BPF container_config is built entirely from CP-side state: Envoy/
// CoreDNS/CIDR/gateway come from the firewall network discovery, the
// egress port from settings, and the host-proxy bypass IP from resolving
// host.docker.internal inside the CP netns when the project enables the
// host proxy. Callers send only container_id.
func (h *Handler) FirewallEnable(ctx context.Context, req *adminv1.FirewallEnableRequest) (*adminv1.FirewallEnableResponse, error) {
	if req.GetContainerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "container_id is required")
	}
	if h.cfg == nil {
		return nil, status.Error(codes.FailedPrecondition, "firewall enable: cp config not wired")
	}

	cid, cgroupPath, cgroupID, err := h.resolveForEnable(ctx, req.GetContainerId())
	if err != nil {
		return nil, err
	}

	netInfo, err := h.stack.NetworkInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "firewall enable: discover network: %v", err)
	}

	hostProxyIP, hostProxyPort, err := h.resolveHostProxy(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "firewall enable: resolve host proxy: %v", err)
	}

	cfg, err := ebpf.NewContainerConfig(
		netInfo.EnvoyIP,
		netInfo.CoreDNSIP,
		netInfo.Gateway.String(),
		netInfo.CIDR,
		hostProxyIP,
		hostProxyPort,
		uint16(h.cfg.EnvoyEgressPort()),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "firewall enable: build container config: %v", err)
	}

	if err := h.ebpf.Install(cgroupID, cgroupPath, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "enable: %v", err)
	}

	// Mid-bypass Enable must cancel the dead-man timer so it does not fire
	// a redundant Enable on a now-correct state. Safe no-op if no timer is
	// pending.
	h.cancelBypassTimer(cid)

	h.log.Info().
		Str("container_id", cid).
		Uint64("cgroup_id", cgroupID).
		Msg("firewall enabled")
	return &adminv1.FirewallEnableResponse{}, nil
}

// FirewallDisable sets the per-container BPF bypass flag so the eBPF fast
// path exits to unrestricted egress. BPF links remain attached so a
// re-enable is cheap (see FirewallRemove for full teardown). When Docker
// confirms the container is gone we fall back to the stored cgroup_id so
// stale bypass_map entries can still be cleared; when the container is
// unknown entirely (never enrolled) Disable is a no-op.
func (h *Handler) FirewallDisable(ctx context.Context, req *adminv1.FirewallDisableRequest) (*adminv1.FirewallDisableResponse, error) {
	if req.GetContainerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "container_id is required")
	}

	cid, cgroupPath, exists, err := h.resolve(ctx, req.GetContainerId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve container: %v", err)
	}

	var cgroupID uint64
	if exists {
		cgroupID, err = h.cgroupIDFn(cgroupPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "compute cgroup id: %v", err)
		}
	} else {
		h.cgroupIDMu.Lock()
		stored, ok := h.storedCgroupID[cid]
		h.cgroupIDMu.Unlock()
		if !ok {
			h.log.Info().
				Str("container_id", req.GetContainerId()).
				Msg("firewall disable: container unknown to CP, no-op")
			return &adminv1.FirewallDisableResponse{}, nil
		}
		cgroupID = stored
	}

	if err := h.ebpf.Disable(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "disable: %v", err)
	}

	h.log.Info().
		Str("container_id", cid).
		Uint64("cgroup_id", cgroupID).
		Msg("firewall disabled")
	return &adminv1.FirewallDisableResponse{}, nil
}

// FirewallBypass = Disable + CP dead-man timer that calls the shared drift
// resolver and then Enable on expiry. The Enable restore path reuses the
// same drift guard as direct FirewallEnable so enforcement returns on the
// container's current cgroup_id, not the one it had at bypass-time.
func (h *Handler) FirewallBypass(ctx context.Context, req *adminv1.FirewallBypassRequest) (*adminv1.FirewallBypassResponse, error) {
	if req.GetContainerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "container_id is required")
	}

	timeout := time.Duration(req.GetTimeoutSeconds()) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > maxBypassTimeout {
		return nil, status.Errorf(codes.InvalidArgument, "bypass timeout %s exceeds maximum %s", timeout, maxBypassTimeout)
	}

	cid, cgroupPath, exists, err := h.resolve(ctx, req.GetContainerId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve container: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.FailedPrecondition, "container %q does not exist", req.GetContainerId())
	}
	cgroupID, err := h.cgroupIDFn(cgroupPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "compute cgroup id: %v", err)
	}

	if err := h.ebpf.Disable(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "bypass: disable: %v", err)
	}

	// Seed storedCgroupID so a Disable issued mid-bypass on a now-gone
	// container can still clear the bypass_map entry via the fallback
	// branch in FirewallDisable. Without this, Bypass-then-kill leaves
	// orphan eBPF state until CleanupStaleBypass at the next CP startup.
	h.cgroupIDMu.Lock()
	h.storedCgroupID[cid] = cgroupID
	h.cgroupIDMu.Unlock()

	h.bypassTimersMu.Lock()
	if existing, ok := h.bypassTimers[cid]; ok {
		existing.timer.Stop()
	}
	entry := &bypassEntry{containerID: cid, cgroupID: cgroupID}
	entry.timer = time.AfterFunc(timeout, func() {
		h.bypassTimerFired(cid, entry)
	})
	h.bypassTimers[cid] = entry
	h.bypassTimersMu.Unlock()

	h.log.Info().
		Str("container_id", cid).
		Uint64("cgroup_id", cgroupID).
		Dur("timeout", timeout).
		Msg("bypass started with server-side failsafe")
	return &adminv1.FirewallBypassResponse{}, nil
}

// --- Rules + lifecycle ---

// FirewallAddRules persists new egress rules, validates them all-or-
// nothing before touching the store, then hot-reloads Envoy+CoreDNS so
// caller code can treat a nil response as "stack already serving the new
// rules".
func (h *Handler) FirewallAddRules(ctx context.Context, req *adminv1.FirewallAddRulesRequest) (*adminv1.FirewallAddRulesResponse, error) {
	rules := ProtoRulesToConfig(req.GetRules())
	for _, r := range rules {
		if err := ValidateDst(r.Dst); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "validate rule %q: %v", r.Dst, err)
		}
	}
	added, err := h.addRulesToStore(rules)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "add rules: %v", err)
	}
	if added == 0 {
		return &adminv1.FirewallAddRulesResponse{AddedCount: 0}, nil
	}
	if err := h.stack.Reload(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "reload stack: %v", err)
	}
	return &adminv1.FirewallAddRulesResponse{AddedCount: int32(added), StackRestarted: true}, nil
}

// FirewallRemoveRules deletes matching rules and hot-reloads.
func (h *Handler) FirewallRemoveRules(ctx context.Context, req *adminv1.FirewallRemoveRulesRequest) (*adminv1.FirewallRemoveRulesResponse, error) {
	rules := ProtoRulesToConfig(req.GetRules())
	removed, err := h.removeRulesFromStore(rules)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "remove rules: %v", err)
	}
	if removed == 0 {
		return &adminv1.FirewallRemoveRulesResponse{RemovedCount: 0}, nil
	}
	if err := h.stack.Reload(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "reload stack: %v", err)
	}
	return &adminv1.FirewallRemoveRulesResponse{RemovedCount: int32(removed), StackRestarted: true}, nil
}

// FirewallListRules returns the current normalized+deduped rule set.
func (h *Handler) FirewallListRules(_ context.Context, _ *adminv1.FirewallListRulesRequest) (*adminv1.FirewallListRulesResponse, error) {
	rules, warnings := NormalizeAndDedup(h.store.Read().Rules)
	for _, w := range warnings {
		h.log.Warn().Msg(w)
	}
	return &adminv1.FirewallListRulesResponse{Rules: ConfigRulesToProto(rules)}, nil
}

// FirewallReload regenerates configs and restarts Envoy+CoreDNS without
// mutating the rule set.
func (h *Handler) FirewallReload(ctx context.Context, _ *adminv1.FirewallReloadRequest) (*adminv1.FirewallReloadResponse, error) {
	if err := h.stack.Reload(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "reload: %v", err)
	}
	return &adminv1.FirewallReloadResponse{StackRestarted: true}, nil
}

// FirewallStatus returns a health snapshot.
func (h *Handler) FirewallStatus(ctx context.Context, _ *adminv1.FirewallStatusRequest) (*adminv1.FirewallStatusResponse, error) {
	st, err := h.stack.Status(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "status: %v", err)
	}
	return &adminv1.FirewallStatusResponse{
		Running:       st.Running,
		EnvoyHealth:   st.EnvoyHealth,
		CorednsHealth: st.CoreDNSHealth,
		RuleCount:     int32(st.RuleCount),
		EnvoyIp:       st.EnvoyIP,
		CorednsIp:     st.CoreDNSIP,
		NetworkId:     st.NetworkID,
	}, nil
}

// FirewallRotateCA regenerates the MITM CA + per-domain certs and reloads
// the stack so Envoy picks up the new chain.
func (h *Handler) FirewallRotateCA(ctx context.Context, _ *adminv1.FirewallRotateCARequest) (*adminv1.FirewallRotateCAResponse, error) {
	certDir, err := h.certDirF()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve cert dir: %v", err)
	}
	rules, _ := NormalizeAndDedup(h.store.Read().Rules)
	if err := RotateCA(certDir, rules); err != nil {
		return nil, status.Errorf(codes.Internal, "rotate ca: %v", err)
	}
	if err := h.stack.Reload(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "rotate ca: reload: %v", err)
	}
	return &adminv1.FirewallRotateCAResponse{}, nil
}

// --- Utilities ---

// FirewallSyncRoutes atomically replaces the global route_map. Internal
// callers normally drive this via AddRules/RemoveRules/Reload; the RPC is
// kept on the surface for break-glass route re-sync.
func (h *Handler) FirewallSyncRoutes(_ context.Context, req *adminv1.FirewallSyncRoutesRequest) (*adminv1.FirewallSyncRoutesResponse, error) {
	routes := make([]ebpf.Route, 0, len(req.GetRoutes()))
	for _, r := range req.GetRoutes() {
		routes = append(routes, ebpf.Route{
			DomainHash: r.GetDomainHash(),
			DstPort:    uint16(r.GetDstPort()),
			EnvoyPort:  uint16(r.GetEnvoyPort()),
		})
	}
	if err := h.ebpf.SyncRoutes(routes); err != nil {
		return nil, status.Errorf(codes.Internal, "sync routes failed: %v", err)
	}
	return &adminv1.FirewallSyncRoutesResponse{Applied: uint32(len(routes))}, nil
}

// FirewallResolveHostname performs a DNS lookup from the CP's network
// namespace — used to resolve host.docker.internal during per-container
// enroll so the BPF container_config holds a routable address.
func (h *Handler) FirewallResolveHostname(ctx context.Context, req *adminv1.FirewallResolveHostnameRequest) (*adminv1.FirewallResolveHostnameResponse, error) {
	if req.GetHostname() == "" {
		return nil, status.Error(codes.InvalidArgument, "hostname is required")
	}
	resolve := h.resolveHostFn
	if resolve == nil {
		resolve = net.DefaultResolver.LookupHost
	}
	addrs, err := resolve(ctx, req.GetHostname())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve %q: %v", req.GetHostname(), err)
	}
	return &adminv1.FirewallResolveHostnameResponse{Addresses: addrs}, nil
}

// --- Shutdown helpers (called from drain-to-zero in cmd/clawker-cp) ---

// CancelAllBypassTimers stops every pending dead-man timer and clears
// the bypass-timers map. Part of INV-B2-007 drain-to-zero: cancelling
// before eBPF FlushAll stops scheduled Enables from firing against
// maps that are about to be emptied. A fire goroutine past
// timer.Stop's check may still call ebpf.Enable once; that call only
// touches bypass_map (ErrKeyNotExist treated as success), so post-Flush
// it is a harmless delete of a non-existent key. Returns the count
// cancelled.
func (h *Handler) CancelAllBypassTimers() int {
	h.bypassTimersMu.Lock()
	defer h.bypassTimersMu.Unlock()
	n := len(h.bypassTimers)
	for _, entry := range h.bypassTimers {
		entry.timer.Stop()
	}
	h.bypassTimers = make(map[string]*bypassEntry)
	if n > 0 {
		h.log.Info().Int("cancelled", n).Msg("cancelled all pending bypass timers")
	}
	return n
}

// --- internal plumbing ---

// resolveForEnable enforces INV-B2-016 for the Enable path: fresh Docker
// lookup, drift warning on stored-vs-fresh cgroup_id mismatch, and
// FailedPrecondition when Docker reports the container gone.
func (h *Handler) resolveForEnable(ctx context.Context, ref string) (cid, cgroupPath string, cgroupID uint64, err error) {
	cid, cgroupPath, exists, resolveErr := h.resolve(ctx, ref)
	if resolveErr != nil {
		return "", "", 0, status.Errorf(codes.Internal, "resolve container: %v", resolveErr)
	}
	if !exists {
		return "", "", 0, status.Errorf(codes.FailedPrecondition, "container %q does not exist", ref)
	}
	cgroupID, err = h.cgroupIDFn(cgroupPath)
	if err != nil {
		return "", "", 0, status.Errorf(codes.Internal, "compute cgroup id: %v", err)
	}
	h.cgroupIDMu.Lock()
	if prev, ok := h.storedCgroupID[cid]; ok && prev != cgroupID {
		h.log.Warn().
			Str("container_id", cid).
			Uint64("stored_cgroup_id", prev).
			Uint64("fresh_cgroup_id", cgroupID).
			Msg("cgroup_id drift detected, using fresh value")
	}
	h.storedCgroupID[cid] = cgroupID
	h.cgroupIDMu.Unlock()
	return cid, cgroupPath, cgroupID, nil
}

// resolveHostProxy returns the IP + port a container should use to reach
// the host proxy via its bypass route. Empty IP + zero port when the
// project has the host proxy disabled — callers pass those through to
// ebpf.NewContainerConfig which treats them as "no bypass". Errors only
// when the host proxy is enabled but its hostname fails to resolve, so
// enforcement does not silently enroll a container with a broken proxy
// path.
func (h *Handler) resolveHostProxy(ctx context.Context) (ip string, port uint16, err error) {
	if h.cfg == nil || !h.cfg.Project().Security.HostProxyEnabled() {
		return "", 0, nil
	}
	port = uint16(h.cfg.Settings().HostProxy.Daemon.Port)

	resolve := h.resolveHostFn
	if resolve == nil {
		resolve = net.DefaultResolver.LookupHost
	}
	addrs, err := resolve(ctx, "host.docker.internal")
	if err != nil {
		return "", 0, fmt.Errorf("looking up host.docker.internal: %w", err)
	}
	if len(addrs) == 0 {
		return "", 0, fmt.Errorf("host.docker.internal resolved to no addresses")
	}
	ip = strings.TrimSpace(addrs[0])
	if ip == "" {
		return "", 0, fmt.Errorf("host.docker.internal resolved to empty address")
	}
	return ip, port, nil
}

func (h *Handler) bypassTimerFired(cid string, entry *bypassEntry) {
	defer func() {
		h.bypassTimersMu.Lock()
		delete(h.bypassTimers, cid)
		h.bypassTimersMu.Unlock()
	}()

	enableID := resolveBypassCgroupID(entry, h.resolve, h.cgroupIDFn, h.log)

	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := h.ebpf.Enable(enableID); err != nil {
			h.log.Error().Err(err).
				Uint64("cgroup_id", enableID).
				Str("container_id", entry.containerID).
				Int("attempt", attempt).
				Msg("bypass auto-enable failed")
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			h.log.Error().
				Uint64("cgroup_id", enableID).
				Str("container_id", entry.containerID).
				Msg("bypass auto-enable exhausted retries — enforcement NOT restored")
			return
		}
		h.log.Info().
			Uint64("cgroup_id", enableID).
			Str("container_id", entry.containerID).
			Msg("bypass timer expired, enforcement restored")
		return
	}
}

func (h *Handler) cancelBypassTimer(cid string) {
	h.bypassTimersMu.Lock()
	defer h.bypassTimersMu.Unlock()
	if entry, ok := h.bypassTimers[cid]; ok {
		entry.timer.Stop()
		delete(h.bypassTimers, cid)
	}
}

// addRulesToStore validates, normalizes, dedups, and persists new rules.
// Returns the count of rules actually added. Validation is all-or-nothing:
// one bad destination aborts before any store mutation.
func (h *Handler) addRulesToStore(rules []config.EgressRule) (int, error) {
	normalized := make([]config.EgressRule, 0, len(rules))
	for _, r := range rules {
		normalized = append(normalized, NormalizeRule(r))
	}
	var added int
	if err := h.store.Set(func(f *EgressRulesFile) {
		existing, _ := NormalizeAndDedup(f.Rules)
		known := make(map[string]struct{}, len(existing))
		for _, r := range existing {
			known[RuleKey(r)] = struct{}{}
		}
		for _, r := range normalized {
			key := RuleKey(r)
			if _, exists := known[key]; exists {
				continue
			}
			known[key] = struct{}{}
			existing = append(existing, r)
			added++
		}
		f.Rules = existing
	}); err != nil {
		return 0, fmt.Errorf("updating rules: %w", err)
	}
	if added == 0 {
		return 0, nil
	}
	if err := h.store.Write(); err != nil {
		return 0, fmt.Errorf("writing rules: %w", err)
	}
	return added, nil
}

// removeRulesFromStore deletes matching rules. Returns the count removed.
func (h *Handler) removeRulesFromStore(toRemove []config.EgressRule) (int, error) {
	if len(toRemove) == 0 {
		return 0, nil
	}
	removeSet := make(map[string]struct{}, len(toRemove))
	for _, r := range toRemove {
		removeSet[RuleKey(NormalizeRule(r))] = struct{}{}
	}
	var removed int
	if err := h.store.Set(func(f *EgressRulesFile) {
		normalized, _ := NormalizeAndDedup(f.Rules)
		filtered := make([]config.EgressRule, 0, len(normalized))
		for _, r := range normalized {
			if _, drop := removeSet[RuleKey(r)]; drop {
				removed++
				continue
			}
			filtered = append(filtered, r)
		}
		f.Rules = filtered
	}); err != nil {
		return 0, fmt.Errorf("removing rules: %w", err)
	}
	if removed == 0 {
		return 0, nil
	}
	if err := h.store.Write(); err != nil {
		return 0, fmt.Errorf("writing rules: %w", err)
	}
	return removed, nil
}

// ProtoRulesToConfig copies []*adminv1.EgressRule → []config.EgressRule.
// The two types track identical field sets; the dedicated mapper keeps
// the handler free of gRPC types when calling into the rules store.
func ProtoRulesToConfig(in []*adminv1.EgressRule) []config.EgressRule {
	out := make([]config.EgressRule, 0, len(in))
	for _, r := range in {
		var paths []config.PathRule
		for _, p := range r.GetPathRules() {
			paths = append(paths, config.PathRule{Path: p.GetPath(), Action: p.GetAction()})
		}
		out = append(out, config.EgressRule{
			Dst:         r.GetDst(),
			Proto:       r.GetProto(),
			Port:        int(r.GetPort()),
			Action:      r.GetAction(),
			PathRules:   paths,
			PathDefault: r.GetPathDefault(),
		})
	}
	return out
}

// ConfigRulesToProto copies []config.EgressRule → []*adminv1.EgressRule.
// Exported because CLI command code needs the reverse mapping when
// displaying rules returned from FirewallListRules.
func ConfigRulesToProto(in []config.EgressRule) []*adminv1.EgressRule {
	out := make([]*adminv1.EgressRule, 0, len(in))
	for _, r := range in {
		var paths []*adminv1.PathRule
		for _, p := range r.PathRules {
			paths = append(paths, &adminv1.PathRule{Path: p.Path, Action: p.Action})
		}
		out = append(out, &adminv1.EgressRule{
			Dst:         r.Dst,
			Proto:       r.Proto,
			Port:        uint32(r.Port),
			Action:      r.Action,
			PathRules:   paths,
			PathDefault: r.PathDefault,
		})
	}
	return out
}
