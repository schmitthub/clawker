package firewall

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- Closure Result types ---
//
// Every queued closure returns a concrete Result on success. The RPC
// wrapper type-asserts the ActionResult.Value, maps it onto the
// generated proto type, and returns that to the gRPC caller. Empty
// marker structs (EnableResult, DisableResult, BypassResult,
// TeardownResult) let the queue's generic any-return surface carry a
// distinguishable type without bloating the public API when there's
// nothing meaningful to return.

// StackReloadResult is produced by reconcileStackClosure. Restarted is
// true when the live Envoy+CoreDNS pair was reloaded; false when the
// stack was down at queue-time and only the on-disk state changed.
// Callers map this onto the wire field `stack_restarted`.
type StackReloadResult struct {
	Restarted bool
}

// InitResult captures the network topology EnsureRunning settled on.
type InitResult struct {
	EnvoyIP, CoreDNSIP, NetworkID string
}

// TeardownResult, EnableResult, DisableResult, BypassResult are empty
// markers — their RPCs report success/failure only.
type TeardownResult struct{}
type EnableResult struct{}
type DisableResult struct{}
type BypassResult struct{}

// ListRulesResult carries the normalized rule snapshot.
type ListRulesResult struct {
	Rules []config.EgressRule
}

// StatusResult mirrors the firewall.Status summary.
type StatusResult struct {
	Status
}

// ResolveResult carries the answer set of a DNS lookup run inside the
// CP's netns.
type ResolveResult struct {
	Addresses []string
}

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
// Every Firewall* RPC submits its body as a closure to a single shared
// ActionQueue so rapid-fire calls don't collide mid-restart (see
// initiative memory `firewall-queue-initiative` for the full design).
// Rule-CRUD and rotate-CA RPCs split store-side work (pre-Submit,
// synchronous) from stack reconcile work (queued), so a durable rule
// mutation is never lost even when the subsequent reload fails.
type Handler struct {
	adminv1.UnimplementedAdminServiceServer

	ebpf       ebpf.EBPFManager
	stack      StackLifecycle
	store      *storage.Store[EgressRulesFile]
	cfg        config.Config
	resolve    ContainerResolver
	log        *logger.Logger
	queue      *ActionQueue
	bus        *overseer.Overseer
	certDirF   func() (string, error)
	listAgents func(ctx context.Context) ([]string, error)

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
	Queue    *ActionQueue

	// Bus is the overseer bus the handler publishes ebpf-axis lifecycle
	// events on after a successful FirewallEnable. Nil-tolerant for unit
	// tests that exercise the RPC surface without a running bus —
	// FirewallEnable simply skips the publish when Bus is nil.
	Bus *overseer.Overseer

	// CertDirFn optionally overrides FirewallCertSubdir resolution —
	// tests pass a temp dir so RotateCA does not touch the real data path.
	// nil defaults to cfg.FirewallCertSubdir.
	CertDirFn func() (string, error)

	// ListAgents returns canonical container IDs of every running
	// managed agent the CP knows about. FirewallInit uses it to rebuild
	// per-container enforcement after a CP restart: FlushAll wipes
	// container_map on shutdown, so agents that outlived the previous
	// CP instance would otherwise egress unenforced until they were
	// restarted. Nil means "no re-enrollment" (test wiring and flows
	// that never want Init to touch agent state).
	ListAgents func(ctx context.Context) ([]string, error)
}

// maxBypassTimeout caps how long a single FirewallBypass call can
// suppress enforcement. Any caller that needs a longer window must
// repeat the call — keeps a single lost-CLI bypass from persisting for
// days.
const maxBypassTimeout = time.Hour

// NewHandler wires a firewall Handler. Panics on missing EBPF, Resolver,
// or Queue — every RPC routes through the queue and hits eBPF/Resolver,
// so nil there would surface as a confusing nil-deref deep inside the
// gRPC interceptor chain. Stack / Store / Cfg are optional at
// construction so tests that only exercise the ebpf-backed RPCs can skip
// them; calls that need them fall through to a panic at the first nil
// access with a clear line number.
func NewHandler(deps HandlerDeps) *Handler {
	switch {
	case deps.EBPF == nil:
		panic("firewall: NewHandler requires a non-nil EBPFManager")
	case deps.Resolver == nil:
		panic("firewall: NewHandler requires a non-nil ContainerResolver")
	case deps.Queue == nil:
		panic("firewall: NewHandler requires a non-nil ActionQueue")
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
		queue:          deps.Queue,
		bus:            deps.Bus,
		certDirF:       certDirFn,
		listAgents:     deps.ListAgents,
		cgroupIDFn:     ebpf.CgroupID,
		bypassTimers:   make(map[string]*bypassEntry),
		storedCgroupID: make(map[string]uint64),
	}
}

// submit routes a closure through the queue and type-asserts the
// Result. Centralizes the "queue rejected" branch so every RPC
// uniformly surfaces ErrQueueClosed when the CP is draining.
func (h *Handler) submit(kind ActionKind, fn ActionFunc) (any, error) {
	res := <-h.queue.Submit(kind, fn)
	if res.Err != nil {
		if errors.Is(res.Err, ErrClosed) {
			return nil, fmt.Errorf("%w: %v", ErrQueueClosed, res.Err)
		}
		return nil, res.Err
	}
	return res.Value, nil
}

// resultAs is a comma-ok wrapper for the queue-result type assertion. A
// wrong-type result indicates a handler/closure wiring bug; returning an
// error instead of panicking keeps CP up so eBPF stays supervised — see
// the "CP crashing is a security incident" invariant in CLAUDE.md.
func resultAs[T any](val any) (T, error) {
	var zero T
	typed, ok := val.(T)
	if !ok {
		return zero, fmt.Errorf("internal: queue result type mismatch: got %T, want %T", val, zero)
	}
	return typed, nil
}

// --- Global lifecycle ---

// FirewallInit brings the firewall stack (Envoy + CoreDNS) up via a
// queued bringup action. BPF programs are loaded once at CP startup via
// ebpf.Manager.Load; this RPC is the idempotent "stack up" signal.
//
// After the stack is healthy, Init re-enrolls every running managed
// agent it can find. On a cold CP start that follows a previous CP's
// FlushAll, container_map is empty — without re-enrollment, long-lived
// agents that outlived the previous CP would egress unenforced
// (fail-open by BPF design). Re-enrollment is in-closure so it
// serializes with concurrent Enable/Disable/Bypass through the same
// ActionBringup work unit.
func (h *Handler) FirewallInit(ctx context.Context, _ *adminv1.FirewallInitRequest) (*adminv1.FirewallInitResult, error) {
	val, err := h.submit(ActionBringup, func(qctx context.Context) (any, error) {
		if err := h.stack.EnsureRunning(qctx); err != nil {
			return nil, err
		}
		st, err := h.stack.Status(qctx)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStackProbe, err)
		}
		// Seed the global route_map from the persisted rules store. The
		// stack containers (Envoy + CoreDNS) just came up with their
		// listeners derived from the store; route_map must mirror that
		// state or BPF connect4 lookups miss and traffic falls through to
		// the default Envoy redirect — wrong listener for non-TLS
		// (TCP/SSH) traffic, which then resets. Skipped silently when no
		// store is wired (test handlers).
		//
		// Subsequent rule mutations go through reconcileStackClosure,
		// which re-runs SyncRoutes after Stack.Reload. FirewallInit is
		// the only path that brings up a fresh stack against an
		// already-persisted rules store, so it owns the post-bringup
		// route sync.
		//
		// SyncRoutes failure is logged-and-continued rather than fatal:
		// the stack is already live and persistent, tearing it down here
		// would produce a more broken state than a partial route_map.
		// FirewallReload replays the full sync from the same store, so
		// the recovery path is a single RPC away.
		if h.store != nil {
			routes := h.routesFromStore()
			if err := h.ebpf.SyncRoutes(routes); err != nil {
				h.log.Error().Err(err).
					Int("routes_attempted", len(routes)).
					Msg("firewall init: route_map seed failed; stack is up but routing may be stale. Retry with FirewallReload.")
			}
		}
		h.reenrollAgents(qctx)
		return InitResult{
			EnvoyIP:   st.EnvoyIP,
			CoreDNSIP: st.CoreDNSIP,
			NetworkID: st.NetworkID,
		}, nil
	})
	if err != nil {
		return nil, toStatus(err)
	}
	r, err := resultAs[InitResult](val)
	if err != nil {
		return nil, toStatus(err)
	}
	return &adminv1.FirewallInitResult{
		EnvoyIp:   r.EnvoyIP,
		CorednsIp: r.CoreDNSIP,
		NetworkId: r.NetworkID,
	}, nil
}

// reenrollAgents rebuilds per-container BPF enforcement for every
// running managed agent. Best-effort: a bad container or missing dep
// logs at warn and continues the loop so one stuck agent cannot block
// a successful FirewallInit for the rest. Nil listAgents (test wiring,
// no-docker handler builds) short-circuits silently. The loop uses
// shared network/host-proxy state resolved once per Init so a large
// agent fleet doesn't pay per-container discovery cost.
func (h *Handler) reenrollAgents(ctx context.Context) {
	if h.listAgents == nil || h.cfg == nil {
		return
	}
	agents, err := h.listAgents(ctx)
	if err != nil {
		h.log.Warn().Err(err).Msg("firewall ebpf: re-enroll: list managed containers failed, skipping")
		return
	}
	if len(agents) == 0 {
		return
	}
	netInfo, err := h.stack.NetworkInfo(ctx)
	if err != nil {
		h.log.Warn().Err(err).Msg("firewall ebpf: re-enroll: network info failed, skipping")
		return
	}
	hostProxyIP, hostProxyPort, err := h.resolveHostProxy(ctx)
	if err != nil {
		h.log.Warn().Err(err).Msg("firewall ebpf: re-enroll: resolve host proxy failed, skipping")
		return
	}
	bpfCfg, err := ebpf.NewContainerConfig(
		netInfo.EnvoyIP,
		netInfo.CoreDNSIP,
		netInfo.Gateway.String(),
		netInfo.CIDR,
		hostProxyIP,
		hostProxyPort,
		uint16(h.cfg.EnvoyEgressPort()),
	)
	if err != nil {
		h.log.Warn().Err(err).Msg("firewall ebpf: re-enroll: build container config failed, skipping")
		return
	}
	var enrolled, failed int
	for _, cid := range agents {
		rid, cgroupPath, exists, resolveErr := h.resolve(ctx, cid)
		if resolveErr != nil {
			h.log.Warn().Err(resolveErr).Str("container_id", cid).Msg("firewall ebpf: re-enroll: resolve failed, skipping container")
			failed++
			continue
		}
		if !exists {
			// Container vanished between list and resolve — fine, move on.
			continue
		}
		cgroupID, err := h.cgroupIDFn(cgroupPath)
		if err != nil {
			h.log.Warn().Err(err).Str("container_id", rid).Msg("firewall ebpf: re-enroll: cgroup id failed, skipping container")
			failed++
			continue
		}
		if err := h.ebpf.Install(cgroupID, cgroupPath, bpfCfg); err != nil {
			h.log.Warn().Err(err).Str("container_id", rid).Msg("firewall ebpf: re-enroll: install failed, skipping container")
			failed++
			continue
		}
		h.cgroupIDMu.Lock()
		h.storedCgroupID[rid] = cgroupID
		h.cgroupIDMu.Unlock()
		// netlogger LabelCache hydration covers BOTH the startup
		// re-enrollment sweep here AND runtime FirewallEnable. Without
		// this publish, agents that outlived the previous CP get their
		// container_map row rebuilt but netlogger records carry empty
		// container_id/agent/project until the user manually rebounces
		// the container.
		h.publishEnrolled(rid, cgroupID)
		h.log.Info().Str("container_id", rid).Uint64("cgroup_id", cgroupID).Msg("firewall ebpf: container re-enrolled in container_map")
		enrolled++
	}
	h.log.Info().Int("enrolled", enrolled).Int("failed", failed).Int("total", len(agents)).Msg("firewall ebpf: re-enroll complete")
}

// FirewallRemove is global teardown. Pre-Submit: cancel pending bypass
// timers so they can't fire against maps that are about to be flushed.
// Queued closure: stop stack, flush eBPF state, delete generated config
// files on disk, clear storedCgroupID. Unlike the pre-queue
// implementation, the egress-rules file is preserved — the store is
// authoritative across teardown so a user's removals after `firewall
// down` apply to the next `firewall up`.
func (h *Handler) FirewallRemove(ctx context.Context, _ *adminv1.FirewallRemoveRequest) (*adminv1.FirewallRemoveResult, error) {
	_, err := h.submit(ActionTeardown, func(qctx context.Context) (any, error) {
		// CancelAllBypassTimers runs inside the closure so no concurrent
		// FirewallBypass call can install a new timer between a pre-
		// Submit cancel and the queued FlushAll — everything mutating
		// bypass state is serialized behind the single queue worker.
		h.CancelAllBypassTimers()

		var errs []error
		if err := h.stack.Stop(qctx); err != nil {
			errs = append(errs, fmt.Errorf("stack stop: %w", err))
		}
		if err := h.ebpf.FlushAll(); err != nil {
			errs = append(errs, fmt.Errorf("ebpf flush: %w", err))
		}
		if err := removeGeneratedConfigs(); err != nil {
			errs = append(errs, fmt.Errorf("remove generated configs: %w", err))
		}

		h.cgroupIDMu.Lock()
		h.storedCgroupID = make(map[string]uint64)
		h.cgroupIDMu.Unlock()

		if err := errors.Join(errs...); err != nil {
			return nil, err
		}
		return TeardownResult{}, nil
	})
	if err != nil {
		return nil, toStatus(fmt.Errorf("firewall remove: %w", err))
	}
	return &adminv1.FirewallRemoveResult{}, nil
}

// removeGeneratedConfigs deletes envoy.yaml and Corefile from the
// firewall data dir. Missing-file is not an error: teardown can run when
// the stack never came up, and the next FirewallInit regenerates both
// files. Path resolution failures propagate — they indicate a
// misconfigured CP, not a missing artifact.
func removeGeneratedConfigs() error {
	envoyPath, err := consts.EnvoyConfigPath()
	if err != nil {
		return fmt.Errorf("resolving envoy config path: %w", err)
	}
	if err := os.Remove(envoyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing envoy.yaml: %w", err)
	}
	corefilePath, err := consts.CorefilePath()
	if err != nil {
		return fmt.Errorf("resolving corefile path: %w", err)
	}
	if err := os.Remove(corefilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing Corefile: %w", err)
	}
	return nil
}

// --- Per-container enforcement ---

// FirewallEnable enrolls a container. Pre-Submit resolves container_id →
// cgroup_path and builds the BPF container_config from CP-side state
// (network discovery, host-proxy resolution, egress port); the queued
// closure installs the resulting config via ebpf.Install and cancels any
// pending bypass timer.
func (h *Handler) FirewallEnable(ctx context.Context, req *adminv1.FirewallEnableRequest) (*adminv1.FirewallEnableResult, error) {
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

	bpfCfg, err := ebpf.NewContainerConfig(
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

	_, err = h.submit(ActionEnable, func(_ context.Context) (any, error) {
		if err := h.ebpf.Install(cgroupID, cgroupPath, bpfCfg); err != nil {
			return nil, fmt.Errorf("install: %w", err)
		}
		// Mid-bypass Enable must cancel the dead-man timer so it does
		// not fire a redundant Enable on an already-correct state.
		h.cancelBypassTimer(cid)
		return EnableResult{}, nil
	})
	if err != nil {
		return nil, toStatus(err)
	}

	h.publishEnrolled(cid, cgroupID)

	h.log.Info().
		Str("container_id", cid).
		Uint64("cgroup_id", cgroupID).
		Msg("firewall enabled")
	return &adminv1.FirewallEnableResult{}, nil
}

// FirewallDisable sets the per-container BPF bypass flag. Pre-Submit
// resolves the target cgroup_id (or falls back to the last-known value
// when Docker says the container is gone); the queued closure performs
// the ebpf.Disable.
func (h *Handler) FirewallDisable(ctx context.Context, req *adminv1.FirewallDisableRequest) (*adminv1.FirewallDisableResult, error) {
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
			return &adminv1.FirewallDisableResult{}, nil
		}
		cgroupID = stored
	}

	_, err = h.submit(ActionDisable, func(_ context.Context) (any, error) {
		if err := h.ebpf.Disable(cgroupID); err != nil {
			return nil, fmt.Errorf("disable: %w", err)
		}
		return DisableResult{}, nil
	})
	if err != nil {
		return nil, toStatus(err)
	}

	h.log.Info().
		Str("container_id", cid).
		Uint64("cgroup_id", cgroupID).
		Msg("firewall disabled")
	return &adminv1.FirewallDisableResult{}, nil
}

// FirewallBypass = Disable (queued) + CP dead-man timer that submits a
// queued Enable on expiry. The restore path reuses the same drift guard
// as direct FirewallEnable so enforcement returns on the container's
// current cgroup_id, not the one it had at bypass-time.
func (h *Handler) FirewallBypass(ctx context.Context, req *adminv1.FirewallBypassRequest) (*adminv1.FirewallBypassResult, error) {
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
		return nil, toStatus(fmt.Errorf("%w: %q", ErrContainerGone, req.GetContainerId()))
	}
	cgroupID, err := h.cgroupIDFn(cgroupPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "compute cgroup id: %v", err)
	}

	_, err = h.submit(ActionBypass, func(_ context.Context) (any, error) {
		if err := h.ebpf.Disable(cgroupID); err != nil {
			return nil, fmt.Errorf("disable: %w", err)
		}
		return BypassResult{}, nil
	})
	if err != nil {
		return nil, toStatus(err)
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
	return &adminv1.FirewallBypassResult{}, nil
}

// --- Rules + lifecycle ---

// FirewallAddRules persists new egress rules then reconciles the stack.
// Store mutation is synchronous and pre-Submit so the rule is durable
// the moment the RPC accepts it; the queued closure then regenerates
// Envoy/CoreDNS config and restarts both. When the stack is down at
// queue-time the closure short-circuits to StackReloadResult{Restarted:
// false} — the rule is still saved, next FirewallInit picks it up.
func (h *Handler) FirewallAddRules(ctx context.Context, req *adminv1.FirewallAddRulesRequest) (*adminv1.FirewallAddRulesResult, error) {
	rules := adminv1.EgressRulesFromProto(req.GetRules())
	// Validate every rule up front and report ALL problems at once. A bad rule
	// from clawker.yaml (malformed port, inverted action, junk destination)
	// fails the whole launch here — the error rides the RPC back to the CLI so
	// the user sees it — rather than being accepted as ADDED and silently dropped
	// when NormalizeAndDedup later canonicalizes the store.
	var invalid []error
	for i, r := range rules {
		if err := ValidateRule(r); err != nil {
			invalid = append(invalid, fmt.Errorf("rule %d (dst=%q): %v", i, r.Dst, err))
		}
	}
	if len(invalid) > 0 {
		return nil, toStatus(fmt.Errorf("%w: %v", ErrRuleInvalid, errors.Join(invalid...)))
	}
	statuses, err := h.addRulesToStore(rules)
	if err != nil {
		return nil, toStatus(fmt.Errorf("%w: %v", ErrRuleStoreWrite, err))
	}
	if !anyAddChange(statuses) {
		return &adminv1.FirewallAddRulesResult{Statuses: toProtoAddStatuses(statuses)}, nil
	}

	val, err := h.submit(ActionReconcile, h.reconcileStackClosure)
	if err != nil {
		return nil, toStatus(err)
	}
	rr, err := resultAs[StackReloadResult](val)
	if err != nil {
		return nil, toStatus(err)
	}
	return &adminv1.FirewallAddRulesResult{
		Statuses:       toProtoAddStatuses(statuses),
		StackRestarted: rr.Restarted,
	}, nil
}

// FirewallRemoveRule deletes a single rule matched by (dst, proto, port)
// pre-Submit then reconciles the stack. A miss on the store lookup
// returns Status=REMOVE_RULE_STATUS_NOT_FOUND on the result (NOT a
// codes.NotFound gRPC error) so a typo or wrong-proto/port surfaces as
// a typed outcome on the wire — the CLI renders the not-found message
// and exits non-zero. Genuine store-I/O failures still come back as
// gRPC errors. No ValidateDst here: anything that fails to match an
// existing key — typo, malformed hostname, or legitimate absence —
// collapses into the same NOT_FOUND outcome, which is exactly the
// behavior the CLI needs to render.
func (h *Handler) FirewallRemoveRule(ctx context.Context, req *adminv1.FirewallRemoveRuleRequest) (*adminv1.FirewallRemoveRuleResult, error) {
	rule := config.EgressRule{
		Dst:   req.GetDst(),
		Proto: req.GetProto(),
		Port:  req.GetPort(),
	}
	pathMode := req.GetPath() != ""
	var matched bool
	var err error
	if pathMode {
		matched, err = h.removePathRuleFromStore(rule, req.GetPath())
	} else {
		matched, err = h.removeRuleFromStore(rule)
	}
	if err != nil {
		return nil, toStatus(fmt.Errorf("%w: %v", ErrRuleStoreWrite, err))
	}
	if !matched {
		return &adminv1.FirewallRemoveRuleResult{
			Status: toProtoRemoveStatus(removeStatusNotFound),
		}, nil
	}

	val, err := h.submit(ActionReconcile, h.reconcileStackClosure)
	if err != nil {
		return nil, toStatus(err)
	}
	rr, err := resultAs[StackReloadResult](val)
	if err != nil {
		return nil, toStatus(err)
	}
	status := removeStatusRemoved
	if pathMode {
		status = removeStatusPathRemoved
	}
	return &adminv1.FirewallRemoveRuleResult{
		StackRestarted: rr.Restarted,
		Status:         toProtoRemoveStatus(status),
	}, nil
}

// FirewallListRules returns the current normalized+deduped rule set.
// Routed through the queue under ActionRead so a read never races ahead
// of pending writes (read-after-write consistency).
func (h *Handler) FirewallListRules(ctx context.Context, _ *adminv1.FirewallListRulesRequest) (*adminv1.FirewallListRulesResult, error) {
	val, err := h.submit(ActionRead, func(_ context.Context) (any, error) {
		rules, warnings := NormalizeAndDedup(h.store.Read().Rules)
		for _, w := range warnings {
			h.log.Warn().Str("normalize_warning", w).Msg("firewall: rule normalization warning")
		}
		return ListRulesResult{Rules: rules}, nil
	})
	if err != nil {
		return nil, toStatus(err)
	}
	r, err := resultAs[ListRulesResult](val)
	if err != nil {
		return nil, toStatus(err)
	}
	return &adminv1.FirewallListRulesResult{Rules: adminv1.EgressRulesToProto(r.Rules)}, nil
}

// AllResolvableDomains returns every domain name CoreDNS will serve a
// zone for under the current firewall rule set — the union of allow-rule
// destinations (after normalization, skipping IP/CIDR destinations and
// deny rules) and the internal hosts CoreDNS forwards out of band
// (`docker.internal` + the monitoring service hostnames). The set is
// constructed with the same passes [GenerateCorefile] uses, so the
// returned slice and the zones in the active Corefile are identical by
// construction. Order is unspecified.
//
// netlogger's reverse-DNS map calls this on its refresh timer to
// rebuild the `domain_hash → domain` table dnsbpf populates as it
// answers queries. Reads bypass the action queue: an eventually
// consistent view (lagging by at most one refresh interval) is fine
// for attribution on security telemetry; queue contention would buy
// nothing observable.
func (h *Handler) AllResolvableDomains() []string {
	if h.store == nil {
		return h.reservedHosts()
	}
	rules, _ := NormalizeAndDedup(h.store.Read().Rules)
	return h.resolvableDomains(rules)
}

// reservedHosts is the internal-host prefix shared by every resolvable-domain
// path: docker.internal plus the monitoring service hostnames CoreDNS forwards
// out of band. CoreDNS serves these regardless of the rule set.
func (h *Handler) reservedHosts() []string {
	return append([]string{"docker.internal"}, consts.MonitoringServiceHostnames...)
}

// resolvableDomains derives the CoreDNS zone set (reserved hosts + allow-rule
// destinations, skipping IP/CIDR and deny rules) from an already-normalized
// rule slice. It does no store I/O so a caller can feed it one snapshot and
// reuse the same slice for [SeedDomainsFromRules], keeping both halves of the
// reverse-DNS union consistent (see [Handler.ReverseDNSDomains]).
func (h *Handler) resolvableDomains(rules []config.EgressRule) []string {
	internalHosts := h.reservedHosts()
	out := make([]string, 0, len(internalHosts))
	seen := make(map[string]bool, len(rules)+len(internalHosts))
	for _, host := range internalHosts {
		seen[host] = true
		out = append(out, host)
	}
	for _, r := range rules {
		if !isAllowDomain(r) {
			continue
		}
		domain := normalizeDomain(r.Dst)
		if seen[domain] {
			continue
		}
		seen[domain] = true
		out = append(out, domain)
	}
	return out
}

// ReverseDNSDomains returns every string whose [ebpf.DomainHash] can appear in
// the BPF dns_cache: the CoreDNS-served zones ([AllResolvableDomains]) plus the
// IP-literal seeds [SyncRoutes] writes for bare-IP routes
// ([SeedDomainsFromRules]). It is the netlogger reverse-DNS DomainSource.
//
// AllResolvableDomains alone is incomplete: it deliberately omits IP/CIDR rules
// (they are not CoreDNS zones), but SyncRoutes still seeds dns_cache[ip] =
// DomainHash(ip) for every bare-IP rule. Sourcing the reverse map from the
// domain set only left each such seed permanently unattributed
// (event=netlogger_reverse_dns_unattributed every refresh tick) and stamped its
// egress records with an empty dst_host despite the destination being known.
// Unioning the seeds in attributes those records to the IP literal and silences
// the false-positive warning. Order is unspecified; the two sets never collide
// (an IP literal is never a domain zone).
//
// The rule snapshot is read once and feeds both the zone derivation and the
// IP-seed derivation, so the two halves can never straddle a mid-flight rule
// mutation (and the normalize pass runs once, not twice).
func (h *Handler) ReverseDNSDomains() []string {
	if h.store == nil {
		return h.reservedHosts()
	}
	rules, _ := NormalizeAndDedup(h.store.Read().Rules)
	out := h.resolvableDomains(rules)
	return append(out, SeedDomainsFromRules(rules, h.envoyPorts())...)
}

// FirewallReload regenerates configs and restarts Envoy+CoreDNS without
// mutating the rule set. No pre-Submit work — it is a pure reconcile
// signal against the current store contents.
func (h *Handler) FirewallReload(ctx context.Context, _ *adminv1.FirewallReloadRequest) (*adminv1.FirewallReloadResult, error) {
	val, err := h.submit(ActionReconcile, h.reconcileStackClosure)
	if err != nil {
		return nil, toStatus(err)
	}
	rr, err := resultAs[StackReloadResult](val)
	if err != nil {
		return nil, toStatus(err)
	}
	return &adminv1.FirewallReloadResult{StackRestarted: rr.Restarted}, nil
}

// FirewallStatus returns a health snapshot.
func (h *Handler) FirewallStatus(ctx context.Context, _ *adminv1.FirewallStatusRequest) (*adminv1.FirewallStatusResult, error) {
	val, err := h.submit(ActionRead, func(qctx context.Context) (any, error) {
		st, err := h.stack.Status(qctx)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStackProbe, err)
		}
		return StatusResult{Status: *st}, nil
	})
	if err != nil {
		return nil, toStatus(err)
	}
	st, err := resultAs[StatusResult](val)
	if err != nil {
		return nil, toStatus(err)
	}
	return &adminv1.FirewallStatusResult{
		Running:       st.Running,
		EnvoyHealth:   st.EnvoyHealth,
		CorednsHealth: st.CoreDNSHealth,
		RuleCount:     int32(st.RuleCount),
		EnvoyIp:       st.EnvoyIP,
		CorednsIp:     st.CoreDNSIP,
		NetworkId:     st.NetworkID,
	}, nil
}

// FirewallRotateCA regenerates the MITM CA + per-domain certs
// pre-Submit, then reconciles the stack so Envoy picks up the new chain.
// Cert regen is synchronous so the CLI sees a clean ErrCertRegen if the
// disk write fails, leaving the running stack on the prior certificates.
func (h *Handler) FirewallRotateCA(ctx context.Context, _ *adminv1.FirewallRotateCARequest) (*adminv1.FirewallRotateCAResult, error) {
	certDir, err := h.certDirF()
	if err != nil {
		return nil, toStatus(fmt.Errorf("%w: resolve cert dir: %v", ErrCertRegen, err))
	}
	rules, _ := NormalizeAndDedup(h.store.Read().Rules)
	if err := RotateCA(certDir, rules); err != nil {
		return nil, toStatus(fmt.Errorf("%w: %v", ErrCertRegen, err))
	}

	val, err := h.submit(ActionReconcile, h.reconcileStackClosure)
	if err != nil {
		return nil, toStatus(err)
	}
	rr, err := resultAs[StackReloadResult](val)
	if err != nil {
		return nil, toStatus(err)
	}
	return &adminv1.FirewallRotateCAResult{StackRestarted: rr.Restarted}, nil
}

// --- Utilities ---

// FirewallSyncRoutes is a break-glass re-sync of the BPF route_map.
// After the queue landed, it regenerates routes from the current store
// (rather than trusting caller-supplied routes) — coalescing with
// concurrent AddRules/Reload calls would otherwise silently discard a
// caller's stale route set. reconcileStackClosure already syncs routes,
// so routing through ActionReconcile gives SyncRoutes the stronger
// "stack and route_map are consistent with the store" guarantee at a
// cost of one extra container restart.
func (h *Handler) FirewallSyncRoutes(ctx context.Context, _ *adminv1.FirewallSyncRoutesRequest) (*adminv1.FirewallSyncRoutesResult, error) {
	val, err := h.submit(ActionReconcile, h.reconcileStackClosure)
	if err != nil {
		return nil, toStatus(err)
	}
	if _, err := resultAs[StackReloadResult](val); err != nil {
		return nil, toStatus(err)
	}
	return &adminv1.FirewallSyncRoutesResult{Applied: uint32(len(h.routesFromStore()))}, nil
}

// FirewallResolveHostname performs a DNS lookup from the CP's network
// namespace — used to resolve host.docker.internal during per-container
// enroll so the BPF container_config holds a routable address.
func (h *Handler) FirewallResolveHostname(ctx context.Context, req *adminv1.FirewallResolveHostnameRequest) (*adminv1.FirewallResolveHostnameResult, error) {
	if req.GetHostname() == "" {
		return nil, status.Error(codes.InvalidArgument, "hostname is required")
	}
	val, err := h.submit(ActionRead, func(qctx context.Context) (any, error) {
		resolve := h.resolveHostFn
		if resolve == nil {
			resolve = net.DefaultResolver.LookupHost
		}
		addrs, err := resolve(qctx, req.GetHostname())
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", req.GetHostname(), err)
		}
		return ResolveResult{Addresses: addrs}, nil
	})
	if err != nil {
		return nil, toStatus(err)
	}
	r, err := resultAs[ResolveResult](val)
	if err != nil {
		return nil, toStatus(err)
	}
	return &adminv1.FirewallResolveHostnameResult{Addresses: r.Addresses}, nil
}

// --- Closures shared across RPCs ---

// reconcileStackClosure is the queued closure every rule-CRUD, reload,
// rotate-CA, and sync-routes RPC submits. It probes whether the stack
// is running; if not, short-circuits with Restarted=false because
// pre-Submit work is already committed and there's nothing live to
// update. If running, it runs Stack.Reload (which regenerates configs
// and restarts both containers) and then replays the store's routes
// into the BPF route_map. Step-level failures from Reload already carry
// wrapped sentinels; RouteSync wraps its own ErrRouteSync, and the two
// are joined via errors.Join so the RPC wrapper can emit one
// errdetails.ErrorInfo per step.
func (h *Handler) reconcileStackClosure(qctx context.Context) (any, error) {
	if h.stack == nil {
		// Handler wired without a Stack (common in tests): nothing to
		// reconcile, pre-Submit work already committed.
		return StackReloadResult{Restarted: false}, nil
	}
	st, err := h.stack.Status(qctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStackProbe, err)
	}
	if !st.Running {
		return StackReloadResult{Restarted: false}, nil
	}

	var errs []error
	if err := h.stack.Reload(qctx); err != nil {
		errs = append(errs, err)
	}
	// Route sync only runs when a store is wired. Test harnesses that
	// omit Store skip this branch; production wiring in cmd/clawker-cp
	// always provides one.
	if h.store != nil {
		if err := h.ebpf.SyncRoutes(h.routesFromStore()); err != nil {
			errs = append(errs, fmt.Errorf("%w: %v", ErrRouteSync, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return StackReloadResult{Restarted: true}, nil
}

// routesFromStore reads the current rules store, normalizes + dedups
// (emitting structured warnings for each dropped rule), and returns
// the BPF Route slice to feed ebpf.SyncRoutes. Returns nil when no
// store is wired — the caller is responsible for skipping the sync.
// Extracted so FirewallInit's fresh-stack seed and
// reconcileStackClosure's post-reload sync cannot diverge in how they
// translate store rules to routes.
func (h *Handler) routesFromStore() []ebpf.Route {
	if h.store == nil {
		return nil
	}
	rules, warnings := NormalizeAndDedup(h.store.Read().Rules)
	for _, w := range warnings {
		h.log.Warn().Str("normalize_warning", w).Msg("firewall: rule normalization warning")
	}
	return RoutesFromRules(rules, h.envoyPorts())
}

// envoyPorts returns the EnvoyPorts config for route building, falling
// back to zero values when no cfg is wired (test path).
func (h *Handler) envoyPorts() EnvoyPorts {
	if h.cfg == nil {
		return EnvoyPorts{}
	}
	return EnvoyPorts{
		EgressPort:  h.cfg.EnvoyEgressPort(),
		TCPPortBase: h.cfg.EnvoyTCPPortBase(),
		UDPPortBase: h.cfg.EnvoyUDPPortBase(),
	}
}

// --- Shutdown helpers (called from drain-to-zero in cmd/clawker-cp) ---

// CancelAllBypassTimers stops every pending dead-man timer and clears
// the bypass-timers map. Part of INV-B2-007 drain-to-zero: cancelling
// before eBPF FlushAll stops scheduled Enables from firing against
// maps that are about to be emptied. A fire goroutine past
// timer.Stop's check will submit to the queue; after queue.Close that
// submission returns ErrClosed and is a harmless retry-exhausted log.
// Returns the count cancelled.
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
// ErrContainerGone when Docker reports the container missing.
func (h *Handler) resolveForEnable(ctx context.Context, ref string) (cid, cgroupPath string, cgroupID uint64, err error) {
	cid, cgroupPath, exists, resolveErr := h.resolve(ctx, ref)
	if resolveErr != nil {
		return "", "", 0, status.Errorf(codes.Internal, "resolve container: %v", resolveErr)
	}
	if !exists {
		return "", "", 0, toStatus(fmt.Errorf("%w: %q", ErrContainerGone, ref))
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

	// One attempt — best-effort by design. Retrying with sleep would
	// block the shutdown path up to a cumulative few seconds per
	// in-flight timer (GracefulStop drains the queue, but a goroutine
	// sleeping between Submit calls doesn't observe that). A transient
	// enable failure is logged and the operator can reissue
	// FirewallEnable.
	// Route through the queue so the Enable can't collide with a
	// simultaneous stack reconcile or a mid-shutdown FlushAll. Post-
	// Close the queue returns ErrClosed — treat that as "CP is tearing
	// down, nothing useful an Enable can do against a flushed map"
	// and exit cleanly.
	res := <-h.queue.Submit(ActionEnable, func(_ context.Context) (any, error) {
		if err := h.ebpf.Enable(enableID); err != nil {
			return nil, fmt.Errorf("enable: %w", err)
		}
		return EnableResult{}, nil
	})
	switch {
	case res.Err == nil:
		h.log.Info().
			Uint64("cgroup_id", enableID).
			Str("container_id", entry.containerID).
			Msg("bypass timer expired, enforcement restored")
	case errors.Is(res.Err, ErrClosed):
		h.log.Info().
			Uint64("cgroup_id", enableID).
			Str("container_id", entry.containerID).
			Msg("bypass auto-enable skipped — queue closed (CP shutting down)")
	default:
		h.log.Error().Err(res.Err).
			Uint64("cgroup_id", enableID).
			Str("container_id", entry.containerID).
			Msg("bypass auto-enable failed — enforcement NOT restored, reissue FirewallEnable to recover")
	}
}

// publishEnrolled emits EBPFContainerEnrolled on the overseer bus so
// netlogger's LabelCache hydrates the cgroup_id → {container_id, labels}
// mapping. Called from BOTH runtime FirewallEnable AND startup
// reenrollAgents — netlogger records carry empty attribution until both
// paths publish. Nil-bus tolerant (unit-test wiring). Publish is
// non-blocking; the bus emits its own drop line, but we add a
// producer-side line naming the affected container_id + cgroup_id so an
// operator can locate the blast radius (one cgroup of unattributed
// netlogger records) from the structured log surface alone.
func (h *Handler) publishEnrolled(containerID string, cgroupID uint64) {
	if h.bus == nil {
		return
	}
	ok := overseer.Publish(h.bus, ebpf.EBPFContainerEnrolled{
		CgroupID:    cgroupID,
		ContainerID: containerID,
		At:          time.Now().UTC(),
	})
	if !ok {
		h.log.Warn().
			Str("container_id", containerID).
			Uint64("cgroup_id", cgroupID).
			Str("event", "netlogger_enroll_publish_dropped").
			Msg("overseer.Publish(EBPFContainerEnrolled) returned false — netlogger LabelCache will not hydrate for this cgroup; subsequent egress records will carry empty attribution")
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

// addRulesToStore normalizes incoming rules and merges them into the
// store via MergeRule. On RuleKey collision the existing entry is mutated
// in place (caller wins on Action/PathDefault; PathRules union by Path
// with caller winning on path collision). New keys are appended.
//
// Returns a per-rule status slice the same length as rules, in input
// order: addStatusAdded for a brand-new RuleKey, addStatusModified for a
// merge that actually changed the stored entry, addStatusUnchanged for
// a reflect.DeepEqual identical re-apply (write suppressed). When every
// entry is Unchanged the store.Write call is skipped so the caller can
// also skip the stack reconcile.
//
// Destination validation happens upstream in FirewallAddRules — this
// helper trusts its input and never returns ErrRuleInvalid.
func (h *Handler) addRulesToStore(rules []config.EgressRule) ([]addStatus, error) {
	normalized := make([]config.EgressRule, 0, len(rules))
	for _, r := range rules {
		normalized = append(normalized, NormalizeRule(r))
	}
	statuses := make([]addStatus, len(rules))
	var mutated bool
	if err := h.store.Set(func(f *EgressRulesFile) {
		// Canonicalize the stored rules the same way every reader does. Indexing
		// the RAW stored rules by RuleKey would miss carved spans: an opaque allow
		// range overlapping a deny is split by NormalizeAndDedup into per-span
		// rules, so a re-add of the original range matches no key and looks new.
		existing, _ := NormalizeAndDedup(f.Rules)
		before := append([]config.EgressRule(nil), existing...)
		index := make(map[string]int, len(existing))
		for i, r := range existing {
			index[RuleKey(r)] = i
		}
		for i, r := range normalized {
			key := RuleKey(r)
			if j, exists := index[key]; exists {
				merged := MergeRule(existing[j], r)
				if reflect.DeepEqual(existing[j], merged) {
					statuses[i] = addStatusUnchanged
					continue
				}
				existing[j] = merged
				statuses[i] = addStatusModified
				continue
			}
			index[key] = len(existing)
			existing = append(existing, r)
			statuses[i] = addStatusAdded
		}
		// Re-canonicalize and compare against the pre-merge canonical form. A
		// freshly-"added" opaque range can carve back to spans already present,
		// making the operation a true no-op. The canonical before/after diff —
		// not the per-rule RuleKey heuristic, which can't see the carve — is the
		// authoritative write+reconcile gate.
		after, _ := NormalizeAndDedup(existing)
		if rulesCanonicalEqual(before, after) {
			return
		}
		f.Rules = after
		mutated = true
	}); err != nil {
		return nil, fmt.Errorf("updating rules: %w", err)
	}
	if !mutated {
		// Canonical state did not move: force UNCHANGED so anyAddChange is false
		// and the caller skips the stack reconcile. Keeps a re-apply of an
		// identical (even carved) rule batch a true no-op — no write, no reload.
		for i := range statuses {
			statuses[i] = addStatusUnchanged
		}
		return statuses, nil
	}
	if err := h.store.Write(); err != nil {
		return nil, fmt.Errorf("writing rules: %w", err)
	}
	return statuses, nil
}

// removeRuleFromStore deletes the single rule whose normalized key
// matches. Returns matched=false when no stored rule shares the key, so
// the caller can map the miss to removeStatusNotFound without touching
// disk.
func (h *Handler) removeRuleFromStore(toRemove config.EgressRule) (bool, error) {
	targetKey := RuleKey(NormalizeRule(toRemove))
	var matched bool
	if err := h.store.Set(func(f *EgressRulesFile) {
		normalized, _ := NormalizeAndDedup(f.Rules)
		filtered := make([]config.EgressRule, 0, len(normalized))
		for _, r := range normalized {
			if RuleKey(r) == targetKey {
				matched = true
				continue
			}
			filtered = append(filtered, r)
		}
		f.Rules = filtered
	}); err != nil {
		return false, fmt.Errorf("removing rule: %w", err)
	}
	if !matched {
		return false, nil
	}
	if err := h.store.Write(); err != nil {
		return false, fmt.Errorf("writing rules: %w", err)
	}
	return true, nil
}

// removePathRuleFromStore removes a single PathRule entry from the rule
// identified by (dst, proto, port). The rule itself remains in the store.
//
// Returns matched=false when either the rule key does not exist or the path
// is not present in the rule's PathRules. The caller maps both to
// removeStatusNotFound on the response (surfaced on the wire as
// REMOVE_RULE_STATUS_NOT_FOUND, not a gRPC error); the CLI qualifies the
// rendered not-found message with the path so a typo never silently succeeds.
func (h *Handler) removePathRuleFromStore(toRemove config.EgressRule, path string) (bool, error) {
	targetKey := RuleKey(NormalizeRule(toRemove))
	var matched bool
	if err := h.store.Set(func(f *EgressRulesFile) {
		normalized, _ := NormalizeAndDedup(f.Rules)
		idx := -1
		for i, r := range normalized {
			if RuleKey(r) == targetKey {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		r := normalized[idx]
		filtered := make([]config.PathRule, 0, len(r.PathRules))
		for _, p := range r.PathRules {
			if p.Path == path {
				matched = true
				continue
			}
			filtered = append(filtered, p)
		}
		if !matched {
			return
		}
		r.PathRules = filtered
		normalized[idx] = r
		f.Rules = normalized
	}); err != nil {
		return false, fmt.Errorf("removing path rule: %w", err)
	}
	if !matched {
		return false, nil
	}
	if err := h.store.Write(); err != nil {
		return false, fmt.Errorf("writing rules: %w", err)
	}
	return true, nil
}
