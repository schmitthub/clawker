package controlplane

import (
	"context"
	"net"
	"time"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ControlPlaneHandler implements v1.ControlPlaneServiceServer. It's a thin
// wrapper over an ebpf.Manager — each RPC validates its inputs, calls the
// corresponding Manager method in-process, and maps the result (or error)
// back to the gRPC response type.
//
// The Manager reference is held by the handler for its entire lifetime.
// cmd/clawker-cp constructs the handler once at startup after calling
// Manager.Load(); the BPF programs stay live in the kernel as long as the
// CP process is running.
type ControlPlaneHandler struct {
	v1.UnimplementedControlPlaneServiceServer

	mgr *ebpf.Manager
	log *logger.Logger

	// resolveHostFn is the libc DNS resolver hook. Injectable for tests
	// that need to return canned IPs without hitting the network. nil in
	// production defaults to net.DefaultResolver.LookupHost.
	resolveHostFn func(ctx context.Context, host string) ([]string, error)
}

// NewControlPlaneHandler wires a handler to a loaded ebpf.Manager. The
// Manager must have Load() successfully called before the handler starts
// serving; cmd/clawker-cp enforces that ordering.
func NewControlPlaneHandler(mgr *ebpf.Manager, log *logger.Logger) *ControlPlaneHandler {
	if log == nil {
		log = logger.Nop()
	}
	return &ControlPlaneHandler{
		mgr: mgr,
		log: log,
	}
}

// Health is the readiness probe. Returns ok=true unconditionally — the
// interceptor whitelist means this method is reachable without a JWT, so
// callers (the firewall manager's EnsureRunning gate) can probe before
// they've completed OIDC setup.
func (h *ControlPlaneHandler) Health(_ context.Context, _ *v1.HealthRequest) (*v1.HealthResponse, error) {
	return &v1.HealthResponse{Ok: true}, nil
}

// EnableContainerFirewall validates the cgroup path, builds an ebpf
// container_config from the request, and attaches BPF programs.
func (h *ControlPlaneHandler) EnableContainerFirewall(
	_ context.Context,
	req *v1.EnableContainerFirewallRequest,
) (*v1.EnableContainerFirewallResponse, error) {
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

	if err := h.mgr.Enable(cgroupID, req.GetCgroupPath(), cfg); err != nil {
		h.log.Error().Err(err).
			Str("component", "controlplane").
			Str("cgroup_path", req.GetCgroupPath()).
			Str("container_id", req.GetContainerId()).
			Msg("EnableContainerFirewall failed")
		return nil, status.Errorf(codes.Internal, "enable failed: %v", err)
	}

	h.log.Info().
		Str("component", "controlplane").
		Str("container_id", req.GetContainerId()).
		Uint64("cgroup_id", cgroupID).
		Msg("container firewall enabled")

	return &v1.EnableContainerFirewallResponse{CgroupId: cgroupID}, nil
}

// DisableContainerFirewall detaches BPF programs and clears the
// container_map entry. Idempotent — disabling an already-disabled
// cgroup is not an error.
func (h *ControlPlaneHandler) DisableContainerFirewall(
	_ context.Context,
	req *v1.DisableContainerFirewallRequest,
) (*v1.DisableContainerFirewallResponse, error) {
	cgroupID, err := h.cgroupIDFromRequest(req.GetCgroupPath())
	if err != nil {
		return nil, err
	}
	if err := h.mgr.Disable(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "disable failed: %v", err)
	}
	h.log.Info().
		Str("component", "controlplane").
		Uint64("cgroup_id", cgroupID).
		Msg("container firewall disabled")
	return &v1.DisableContainerFirewallResponse{CgroupId: cgroupID}, nil
}

// BypassContainer sets the bypass flag for a container's cgroup, letting
// its traffic skip firewall enforcement. If the request includes a
// positive timeout_seconds, the CP schedules an automatic Unbypass after
// that many seconds — the timer lives in the CP process, so it survives
// the calling CLI exiting between Bypass and Unbypass.
func (h *ControlPlaneHandler) BypassContainer(
	_ context.Context,
	req *v1.BypassContainerRequest,
) (*v1.BypassContainerResponse, error) {
	cgroupID, err := h.cgroupIDFromRequest(req.GetCgroupPath())
	if err != nil {
		return nil, err
	}
	if err := h.mgr.Bypass(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "bypass failed: %v", err)
	}

	if timeout := req.GetTimeoutSeconds(); timeout > 0 {
		h.scheduleUnbypass(cgroupID, time.Duration(timeout)*time.Second)
	}

	return &v1.BypassContainerResponse{CgroupId: cgroupID}, nil
}

// scheduleUnbypass spawns a detached goroutine that sleeps for d and
// then calls Manager.Unbypass(cgroupID). The timer lives for the CP
// process lifetime — on CP shutdown, any pending unbypass is lost and
// the bypass flag stays set until either a manual unbypass or the
// container is disabled. This is documented as a v1 limitation; v2 can
// persist pending unbypass timers to disk for crash resilience.
func (h *ControlPlaneHandler) scheduleUnbypass(cgroupID uint64, d time.Duration) {
	h.log.Info().
		Uint64("cgroup_id", cgroupID).
		Dur("timeout", d).
		Msg("bypass timer scheduled")
	go func() {
		timer := time.NewTimer(d)
		defer timer.Stop()
		<-timer.C
		if err := h.mgr.Unbypass(cgroupID); err != nil {
			h.log.Error().
				Err(err).
				Uint64("cgroup_id", cgroupID).
				Msg("auto-unbypass failed")
			return
		}
		h.log.Info().
			Uint64("cgroup_id", cgroupID).
			Msg("bypass auto-expired")
	}()
}

// UnbypassContainer clears the bypass flag, returning the container to
// normal enforcement.
func (h *ControlPlaneHandler) UnbypassContainer(
	_ context.Context,
	req *v1.UnbypassContainerRequest,
) (*v1.UnbypassContainerResponse, error) {
	cgroupID, err := h.cgroupIDFromRequest(req.GetCgroupPath())
	if err != nil {
		return nil, err
	}
	if err := h.mgr.Unbypass(cgroupID); err != nil {
		return nil, status.Errorf(codes.Internal, "unbypass failed: %v", err)
	}
	return &v1.UnbypassContainerResponse{CgroupId: cgroupID}, nil
}

// SyncFirewallRoutes atomically replaces the global route_map with the
// provided route set.
func (h *ControlPlaneHandler) SyncFirewallRoutes(
	_ context.Context,
	req *v1.SyncFirewallRoutesRequest,
) (*v1.SyncFirewallRoutesResponse, error) {
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
	return &v1.SyncFirewallRoutesResponse{Applied: uint32(len(routes))}, nil
}

// UpdateDnsCache writes a single dns_cache entry. Retained for
// diagnostic tools — production writes go through the dnsbpf CoreDNS
// plugin.
func (h *ControlPlaneHandler) UpdateDnsCache(
	_ context.Context,
	req *v1.UpdateDnsCacheRequest,
) (*v1.UpdateDnsCacheResponse, error) {
	ip := net.ParseIP(req.GetIp())
	if ip == nil || ip.To4() == nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid IPv4 address: %q", req.GetIp())
	}
	if err := h.mgr.UpdateDNSCache(ebpf.IPToUint32(ip), req.GetDomainHash(), req.GetTtlSeconds()); err != nil {
		return nil, status.Errorf(codes.Internal, "update dns cache failed: %v", err)
	}
	return &v1.UpdateDnsCacheResponse{}, nil
}

// GarbageCollectDns removes expired dns_cache entries and returns the count.
func (h *ControlPlaneHandler) GarbageCollectDns(
	_ context.Context,
	_ *v1.GarbageCollectDnsRequest,
) (*v1.GarbageCollectDnsResponse, error) {
	removed := h.mgr.GarbageCollectDNS()
	return &v1.GarbageCollectDnsResponse{Removed: uint32(removed)}, nil
}

// LookupContainer reads the container_map entry for a cgroup (diagnostics).
func (h *ControlPlaneHandler) LookupContainer(
	_ context.Context,
	req *v1.LookupContainerRequest,
) (*v1.LookupContainerResponse, error) {
	cgroupID, err := h.cgroupIDFromRequest(req.GetCgroupPath())
	if err != nil {
		return nil, err
	}
	cfg, err := h.mgr.LookupContainer(cgroupID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "lookup failed: %v", err)
	}
	return &v1.LookupContainerResponse{
		CgroupId: cgroupID,
		Config: &v1.ContainerConfigSnapshot{
			EnvoyIp:       ebpf.Uint32ToIP(cfg.EnvoyIp).String(),
			CorednsIp:     ebpf.Uint32ToIP(cfg.CorednsIp).String(),
			GatewayIp:     ebpf.Uint32ToIP(cfg.GatewayIp).String(),
			NetAddr:       ebpf.Uint32ToIP(cfg.NetAddr).String(),
			NetMask:       ebpf.Uint32ToIP(cfg.NetMask).String(),
			HostProxyIp:   ebpf.Uint32ToIP(cfg.HostProxyIp).String(),
			HostProxyPort: uint32(cfg.HostProxyPort),
			EgressPort:    uint32(cfg.EgressPort),
		},
	}, nil
}

// ResolveHostname performs a libc DNS lookup from the CP container's
// network namespace. The firewall manager uses this to resolve
// host.docker.internal without having to exec into the container.
func (h *ControlPlaneHandler) ResolveHostname(
	ctx context.Context,
	req *v1.ResolveHostnameRequest,
) (*v1.ResolveHostnameResponse, error) {
	host := req.GetHostname()
	if host == "" {
		return nil, status.Error(codes.InvalidArgument, "hostname is required")
	}
	lookup := h.resolveHostFn
	if lookup == nil {
		lookup = net.DefaultResolver.LookupHost
	}
	// Enforce a modest timeout so a slow upstream resolver doesn't
	// stall the gRPC handler indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := lookup(ctx, host)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "resolve %s: %v", host, err)
	}
	return &v1.ResolveHostnameResponse{Addresses: addrs}, nil
}

// cgroupIDFromRequest validates the cgroup path and converts it to a
// kernel cgroup ID. Used by every per-container RPC.
func (h *ControlPlaneHandler) cgroupIDFromRequest(cgroupPath string) (uint64, error) {
	if cgroupPath == "" {
		return 0, status.Error(codes.InvalidArgument, "cgroup_path is required")
	}
	id, err := ebpf.CgroupID(cgroupPath)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "invalid cgroup path: %v", err)
	}
	return id, nil
}

// Compile-time interface check — fails the build if a ControlPlaneService
// method is added to the proto without a corresponding handler method here.
var _ v1.ControlPlaneServiceServer = (*ControlPlaneHandler)(nil)
