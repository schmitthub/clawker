package controlplane

import (
	"context"
	"net"
	"sync"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AdminHandler implements adminv1.AdminServiceServer. Thin wrapper over
// ebpf.EBPFManager — each RPC validates inputs, calls the corresponding
// method, and maps results to the gRPC response type.
type AdminHandler struct {
	adminv1.UnimplementedAdminServiceServer

	mgr ebpf.EBPFManager
	log *logger.Logger

	bypassTimersMu sync.Mutex
	bypassTimers   map[uint64]*time.Timer

	// resolveHostFn is injectable for tests. nil defaults to
	// net.DefaultResolver.LookupHost.
	resolveHostFn func(ctx context.Context, host string) ([]string, error)
}

// NewAdminHandler wires a handler to an ebpf.EBPFManager.
func NewAdminHandler(mgr ebpf.EBPFManager, log *logger.Logger) *AdminHandler {
	if log == nil {
		log = logger.Nop()
	}
	return &AdminHandler{mgr: mgr, log: log, bypassTimers: make(map[uint64]*time.Timer)}
}

// Install attaches BPF programs to a container's cgroup and populates
// its container_map entry.
func (h *AdminHandler) Install(_ context.Context, req *adminv1.InstallRequest) (*adminv1.InstallResponse, error) {
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
func (h *AdminHandler) Remove(_ context.Context, req *adminv1.RemoveRequest) (*adminv1.RemoveResponse, error) {
	cgroupID, err := h.cgroupIDFromPath(req.GetCgroupPath())
	if err != nil {
		return nil, err
	}
	h.bypassTimersMu.Lock()
	if t, ok := h.bypassTimers[cgroupID]; ok {
		t.Stop()
		delete(h.bypassTimers, cgroupID)
	}
	h.bypassTimersMu.Unlock()
	if err := h.mgr.Remove(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "remove failed: %v", err)
	}
	h.log.Info().Uint64("cgroup_id", cgroupID).Msg("firewall removed")
	return &adminv1.RemoveResponse{CgroupId: cgroupID}, nil
}

// Enable clears the bypass flag, restoring firewall enforcement.
func (h *AdminHandler) Enable(_ context.Context, req *adminv1.EnableRequest) (*adminv1.EnableResponse, error) {
	cgroupID, err := h.cgroupIDFromPath(req.GetCgroupPath())
	if err != nil {
		return nil, err
	}
	h.bypassTimersMu.Lock()
	if t, ok := h.bypassTimers[cgroupID]; ok {
		t.Stop()
		delete(h.bypassTimers, cgroupID)
	}
	h.bypassTimersMu.Unlock()
	if err := h.mgr.Enable(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "enable failed: %v", err)
	}
	return &adminv1.EnableResponse{CgroupId: cgroupID}, nil
}

// Disable sets the bypass flag, letting traffic skip enforcement.
func (h *AdminHandler) Disable(_ context.Context, req *adminv1.DisableRequest) (*adminv1.DisableResponse, error) {
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
// restored. The CLI can call Enable early; the timer no-ops if the flag
// is already cleared.
func (h *AdminHandler) Bypass(_ context.Context, req *adminv1.BypassRequest) (*adminv1.BypassResponse, error) {
	cgroupID, err := h.cgroupIDFromPath(req.GetCgroupPath())
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(req.GetTimeoutSeconds()) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	if err := h.mgr.Disable(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "bypass: disable failed: %v", err)
	}

	// Cancel any existing timer for this cgroup.
	h.bypassTimersMu.Lock()
	if t, ok := h.bypassTimers[cgroupID]; ok {
		t.Stop()
	}
	h.bypassTimers[cgroupID] = time.AfterFunc(timeout, func() {
		const maxRetries = 3
		for attempt := 1; attempt <= maxRetries; attempt++ {
			if err := h.mgr.Enable(cgroupID); err != nil {
				h.log.Error().Err(err).
					Uint64("cgroup_id", cgroupID).
					Int("attempt", attempt).
					Msg("bypass auto-enable failed")
				if attempt < maxRetries {
					time.Sleep(time.Duration(attempt) * time.Second)
					continue
				}
				h.log.Error().
					Uint64("cgroup_id", cgroupID).
					Msg("bypass auto-enable exhausted retries — enforcement NOT restored")
				return
			}
			h.log.Info().
				Uint64("cgroup_id", cgroupID).
				Msg("bypass timer expired, enforcement restored")
			break
		}
		h.bypassTimersMu.Lock()
		delete(h.bypassTimers, cgroupID)
		h.bypassTimersMu.Unlock()
	})
	h.bypassTimersMu.Unlock()

	h.log.Info().
		Uint64("cgroup_id", cgroupID).
		Dur("timeout", timeout).
		Msg("bypass started with server-side failsafe")
	return &adminv1.BypassResponse{CgroupId: cgroupID}, nil
}

// SyncRoutes atomically replaces the global route_map.
func (h *AdminHandler) SyncRoutes(_ context.Context, req *adminv1.SyncRoutesRequest) (*adminv1.SyncRoutesResponse, error) {
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
func (h *AdminHandler) ResolveHostname(ctx context.Context, req *adminv1.ResolveHostnameRequest) (*adminv1.ResolveHostnameResponse, error) {
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

func (h *AdminHandler) cgroupIDFromPath(path string) (uint64, error) {
	if path == "" {
		return 0, status.Error(codes.InvalidArgument, "cgroup_path is required")
	}
	id, err := ebpf.CgroupID(path)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "invalid cgroup path: %v", err)
	}
	return id, nil
}
