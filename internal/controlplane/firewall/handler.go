package firewall

import (
	"context"
	"net"
	"sync"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ContainerResolver checks whether a Docker container exists and returns
// its cgroup path. Used by the bypass dead-man timer to verify container
// state against Docker as the source of truth.
type ContainerResolver func(ctx context.Context, containerID string) (cgroupPath string, exists bool, err error)

// Handler implements adminv1.AdminServiceServer. Thin wrapper over
// ebpf.EBPFManager — each RPC validates inputs, calls the corresponding
// method, and maps results to the gRPC response type.
type Handler struct {
	adminv1.UnimplementedAdminServiceServer

	mgr ebpf.EBPFManager
	log *logger.Logger

	bypassTimersMu sync.Mutex
	bypassTimers   map[string]*bypassEntry // keyed by cgroup path

	// resolveContainer queries Docker to verify a container is still
	// running and returns its current cgroup path. Must be non-nil;
	// NewHandler panics otherwise.
	resolveContainer ContainerResolver

	// resolveHostFn is injectable for tests. nil defaults to
	// net.DefaultResolver.LookupHost.
	resolveHostFn func(ctx context.Context, host string) ([]string, error)
}

// bypassEntry tracks the state needed by the dead-man timer to restore
// enforcement. Both the container ID (for Docker API verification) and
// the original cgroup ID (fallback if Docker is unreachable) are stored.
type bypassEntry struct {
	timer       *time.Timer
	containerID string
	cgroupID    uint64 // fallback — used when Docker API is unavailable
}

// NewHandler wires a handler to an ebpf.EBPFManager.
// resolver must be non-nil — panics otherwise.
func NewHandler(mgr ebpf.EBPFManager, log *logger.Logger, resolver ContainerResolver) *Handler {
	if log == nil {
		log = logger.Nop()
	}
	if resolver == nil {
		panic("firewall: NewHandler requires a non-nil ContainerResolver")
	}
	return &Handler{
		mgr:              mgr,
		log:              log,
		bypassTimers:     make(map[string]*bypassEntry),
		resolveContainer: resolver,
	}
}

// Install attaches BPF programs to a container's cgroup and populates
// its container_map entry.
func (h *Handler) Install(_ context.Context, req *adminv1.InstallRequest) (*adminv1.InstallResponse, error) {
	if req.GetConfig() == nil {
		return nil, status.Error(codes.InvalidArgument, "config is required")
	}
	if req.GetCgroupPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "cgroup_path is required")
	}

	cgroupID, err := ebpf.CgroupID(req.GetCgroupPath())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid cgroup path: %v", err)
	}

	cfg, err := ebpf.NewContainerConfig(
		req.GetConfig().GetEnvoyIp(),
		req.GetConfig().GetCorednsIp(),
		req.GetConfig().GetGatewayIp(),
		req.GetConfig().GetCidr(),
		req.GetConfig().GetHostProxyIp(),
		uint16(req.GetConfig().GetHostProxyPort()),
		uint16(req.GetConfig().GetEgressPort()),
	)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "build container config: %v", err)
	}

	if err := h.mgr.Install(cgroupID, req.GetCgroupPath(), cfg); err != nil {
		h.log.Error().Err(err).
			Str("cgroup_path", req.GetCgroupPath()).
			Str("container_id", req.GetContainerId()).
			Msg("Install failed")
		return nil, status.Errorf(codes.Internal, "install failed: %v", err)
	}

	h.log.Info().
		Str("container_id", req.GetContainerId()).
		Uint64("cgroup_id", cgroupID).
		Msg("firewall installed")
	return &adminv1.InstallResponse{CgroupId: cgroupID}, nil
}

// Remove detaches BPF programs and removes the container_map entry.
func (h *Handler) Remove(_ context.Context, req *adminv1.RemoveRequest) (*adminv1.RemoveResponse, error) {
	cgroupID, err := h.cgroupIDFromPath(req.GetCgroupPath())
	if err != nil {
		return nil, err
	}
	h.cancelBypassTimer(req.GetCgroupPath())
	if err := h.mgr.Remove(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "remove failed: %v", err)
	}
	h.log.Info().Uint64("cgroup_id", cgroupID).Msg("firewall removed")
	return &adminv1.RemoveResponse{CgroupId: cgroupID}, nil
}

// Enable clears the bypass flag, restoring firewall enforcement.
func (h *Handler) Enable(_ context.Context, req *adminv1.EnableRequest) (*adminv1.EnableResponse, error) {
	if req.GetContainerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "container_id is required")
	}
	cgroupID, err := h.cgroupIDFromPath(req.GetCgroupPath())
	if err != nil {
		return nil, err
	}
	h.cancelBypassTimer(req.GetCgroupPath())
	if err := h.mgr.Enable(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "enable failed: %v", err)
	}
	return &adminv1.EnableResponse{CgroupId: cgroupID}, nil
}

// Disable sets the bypass flag, letting traffic skip enforcement.
func (h *Handler) Disable(_ context.Context, req *adminv1.DisableRequest) (*adminv1.DisableResponse, error) {
	if req.GetContainerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "container_id is required")
	}
	cgroupID, err := h.cgroupIDFromPath(req.GetCgroupPath())
	if err != nil {
		return nil, err
	}
	if err := h.mgr.Disable(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "disable failed: %v", err)
	}
	return &adminv1.DisableResponse{CgroupId: cgroupID}, nil
}

// Bypass sets the bypass flag and starts a server-side dead-man timer.
// After timeout_seconds the CP automatically clears the flag via Enable.
// Acts as a failsafe — if the CLI crashes mid-bypass, enforcement is
// restored. The CLI can call Enable early; Enable is idempotent, so the
// timer harmlessly re-clears an already-cleared flag when it fires.
//
// When the timer fires, it queries Docker via resolveContainer to verify
// the container is still running and get its current cgroup path. Docker
// is the source of truth for container existence — our maps are not.
//
// Resolution strategy (fail-closed — always attempt Enable):
//  1. Docker says alive → recompute cgroup ID from Docker's cgroup path → Enable
//  2. Docker says gone  → use stored cgroup ID to clear stale bypass_map entry → Enable
//  3. Docker API error  → use stored cgroup ID as fallback → Enable
func (h *Handler) Bypass(_ context.Context, req *adminv1.BypassRequest) (*adminv1.BypassResponse, error) {
	cgroupPath := req.GetCgroupPath()
	containerID := req.GetContainerId()
	if containerID == "" {
		return nil, status.Error(codes.InvalidArgument, "container_id is required for bypass dead-man verification")
	}
	cgroupID, err := h.cgroupIDFromPath(cgroupPath)
	if err != nil {
		return nil, err
	}

	const maxBypassTimeout = time.Hour
	timeout := time.Duration(req.GetTimeoutSeconds()) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > maxBypassTimeout {
		return nil, status.Errorf(codes.InvalidArgument, "bypass timeout %s exceeds maximum %s", timeout, maxBypassTimeout)
	}

	if err := h.mgr.Disable(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "bypass: disable failed: %v", err)
	}

	h.bypassTimersMu.Lock()
	if existing, ok := h.bypassTimers[cgroupPath]; ok {
		existing.timer.Stop()
	}

	entry := &bypassEntry{
		containerID: containerID,
		cgroupID:    cgroupID,
	}
	entry.timer = time.AfterFunc(timeout, func() {
		h.bypassTimerFired(cgroupPath, entry)
	})
	h.bypassTimers[cgroupPath] = entry
	h.bypassTimersMu.Unlock()

	h.log.Info().
		Uint64("cgroup_id", cgroupID).
		Str("cgroup_path", cgroupPath).
		Str("container_id", containerID).
		Dur("timeout", timeout).
		Msg("bypass started with server-side failsafe")
	return &adminv1.BypassResponse{CgroupId: cgroupID}, nil
}

// bypassTimerFired is called when a dead-man timer expires. It consults
// Docker as the source of truth for container state, then always calls
// Enable to restore enforcement. The map entry is unconditionally cleaned
// up regardless of whether Enable succeeds.
func (h *Handler) bypassTimerFired(cgroupPath string, entry *bypassEntry) {
	defer func() {
		h.bypassTimersMu.Lock()
		delete(h.bypassTimers, cgroupPath)
		h.bypassTimersMu.Unlock()
	}()

	enableID := h.resolveBypassCgroupID(entry)

	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := h.mgr.Enable(enableID); err != nil {
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
			Str("cgroup_path", cgroupPath).
			Str("container_id", entry.containerID).
			Msg("bypass timer expired, enforcement restored")
		return
	}
}

// resolveBypassCgroupID determines the cgroup ID to use when re-enabling
// enforcement. Docker is the source of truth:
//
//  1. Container alive in Docker → use cgroup path from Docker → fresh cgroup ID
//  2. Container gone in Docker → use stored cgroup ID (clear stale bypass_map)
//  3. Docker API unreachable → use stored cgroup ID (fail-closed)
//
// Enable is NEVER skipped — every path returns a cgroup ID to call Enable on.
func (h *Handler) resolveBypassCgroupID(entry *bypassEntry) uint64 {
	if entry.containerID == "" {
		// Should not happen — Bypass validates container_id.
		return entry.cgroupID
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dockerCgroupPath, exists, err := h.resolveContainer(ctx, entry.containerID)
	if err != nil {
		h.log.Warn().Err(err).
			Str("container_id", entry.containerID).
			Uint64("fallback_cgroup_id", entry.cgroupID).
			Msg("bypass timer: Docker API error, using stored cgroup ID")
		return entry.cgroupID
	}

	if !exists {
		h.log.Info().
			Str("container_id", entry.containerID).
			Uint64("fallback_cgroup_id", entry.cgroupID).
			Msg("bypass timer: container gone per Docker, clearing stale bypass entry")
		return entry.cgroupID
	}

	// Container alive — recompute cgroup ID from Docker's reported path.
	freshID, err := ebpf.CgroupID(dockerCgroupPath)
	if err != nil {
		h.log.Warn().Err(err).
			Str("docker_cgroup_path", dockerCgroupPath).
			Uint64("fallback_cgroup_id", entry.cgroupID).
			Msg("bypass timer: cgroup stat failed on Docker path, using stored cgroup ID")
		return entry.cgroupID
	}

	if freshID != entry.cgroupID {
		h.log.Warn().
			Uint64("stored_cgroup_id", entry.cgroupID).
			Uint64("fresh_cgroup_id", freshID).
			Str("container_id", entry.containerID).
			Msg("bypass timer: cgroup ID drift detected, using fresh ID from Docker")
	}
	return freshID
}

// cancelBypassTimer stops and removes a bypass timer for the given cgroup path.
func (h *Handler) cancelBypassTimer(cgroupPath string) {
	h.bypassTimersMu.Lock()
	defer h.bypassTimersMu.Unlock()
	if entry, ok := h.bypassTimers[cgroupPath]; ok {
		entry.timer.Stop()
		delete(h.bypassTimers, cgroupPath)
	}
}

// SyncRoutes atomically replaces the global route_map.
func (h *Handler) SyncRoutes(_ context.Context, req *adminv1.SyncRoutesRequest) (*adminv1.SyncRoutesResponse, error) {
	routes := make([]ebpf.Route, 0, len(req.GetRoutes()))
	for _, r := range req.GetRoutes() {
		routes = append(routes, ebpf.Route{
			DomainHash: r.GetDomainHash(),
			DstPort:    uint16(r.GetDstPort()),
			EnvoyPort:  uint16(r.GetEnvoyPort()),
		})
	}
	if err := h.mgr.SyncRoutes(routes); err != nil {
		return nil, status.Errorf(codes.Internal, "sync routes failed: %v", err)
	}
	return &adminv1.SyncRoutesResponse{Applied: uint32(len(routes))}, nil
}

// ResolveHostname performs a DNS lookup from the CP container's network
// namespace.
func (h *Handler) ResolveHostname(ctx context.Context, req *adminv1.ResolveHostnameRequest) (*adminv1.ResolveHostnameResponse, error) {
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
	return &adminv1.ResolveHostnameResponse{Addresses: addrs}, nil
}

func (h *Handler) cgroupIDFromPath(path string) (uint64, error) {
	if path == "" {
		return 0, status.Error(codes.InvalidArgument, "cgroup_path is required")
	}
	id, err := ebpf.CgroupID(path)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "invalid cgroup path: %v", err)
	}
	return id, nil
}
