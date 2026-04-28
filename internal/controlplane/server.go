// Package controlplane implements the clawker control plane — a privileged
// long-lived gRPC service that owns authoritative state for managed
// containers. Serves the AdminService surface (CLI ↔ CP) and supplies
// the auth + lifecycle plumbing shared with the agent listener
// (clawkerd ↔ CP, registered separately by cmd/clawker-cp).
package controlplane

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/controlplane/agentslots"
	fwhandler "github.com/schmitthub/clawker/internal/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/logger"
)

// adminServer composes the domain-specific handlers into the single
// AdminServiceServer surface. The firewall handler embeds
// UnimplementedAdminServiceServer so new RPCs default to
// codes.Unimplemented; explicit methods on adminServer (e.g.
// ListAgents, AnnounceAgent) override that fallback.
type adminServer struct {
	// *fwhandler.Handler embeds adminv1.UnimplementedAdminServiceServer,
	// so new proto RPCs fail open with codes.Unimplemented via promotion
	// rather than blocking the whole CP on a partial domain rewrite.
	*fwhandler.Handler

	agents agentregistry.Registry
	slots  agentslots.Registry
	clock  func() time.Time
	log    *logger.Logger
}

// compile-time: any future additions to AdminServiceServer must be
// covered by one of the embedded domain handlers or this assertion fails.
var _ adminv1.AdminServiceServer = (*adminServer)(nil)

// NewAdminServer returns the composite AdminServiceServer wired from
// the supplied domain handlers.
//
//   - slots is required: AnnounceAgent has no fallback path when the
//     slot registry is missing. Panic at construction so a wiring
//     regression surfaces during startup, not as opaque codes.Internal
//     responses on every announce. Mirrors agent.NewHandler's posture
//     for its required dependencies.
//   - agents may be nil — when nil, ListAgents returns an empty result
//     so the CLI's `controlplane agents` command renders cleanly even
//     on a CP build that hasn't wired the agent registry yet.
//   - clock defaults to time.Now when nil. Tests inject a fixed-time
//     function so AnnounceAgent's ExpiresAt is deterministic.
//   - log defaults to logger.Nop() when nil. Production wiring passes
//     the CP's structured logger so AnnounceAgent failures surface in
//     the operator log; tests pass nil.
func NewAdminServer(fw *fwhandler.Handler, agents agentregistry.Registry, slots agentslots.Registry, clock func() time.Time, log *logger.Logger) adminv1.AdminServiceServer {
	if slots == nil {
		panic("controlplane: NewAdminServer requires non-nil slots registry")
	}
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = logger.Nop()
	}
	return &adminServer{Handler: fw, agents: agents, slots: slots, clock: clock, log: log}
}

// AnnounceAgent reserves a slot keyed by container_id. The slot is
// the CP's record that the clawker CLI specifically initiated this
// start — it carries no auth-bearing material. Agent identity
// verification flows separately through agentregistry (CLI-written,
// CP-read) when CP dials the running clawkerd. Slots are consumed by
// agentdial on the next successful dial of the container's clawkerd
// listener; missing slots produce an "unattested start" data point
// for downstream decision-making, not a connection refusal.
//
// Wire-level error mapping:
//   - empty container_id → codes.InvalidArgument
//   - duplicate container_id → codes.AlreadyExists
//   - any other Reserve failure → codes.Internal (logged at Warn so
//     operators have a triage trail; the wire response stays generic)
func (s *adminServer) AnnounceAgent(_ context.Context, req *adminv1.AnnounceAgentRequest) (*adminv1.AnnounceAgentResult, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if req.ContainerId == "" {
		return nil, status.Error(codes.InvalidArgument, "container_id required")
	}

	now := s.clock()
	if err := s.slots.Reserve(agentslots.Slot{ContainerID: req.ContainerId}); err != nil {
		if errors.Is(err, agentslots.ErrSlotExists) {
			return nil, status.Error(codes.AlreadyExists, "container already announced")
		}
		log := s.log
		if log == nil {
			log = logger.Nop()
		}
		log.Warn().
			Err(err).
			Str("container_id", req.ContainerId).
			Msg("admin: slot reservation failed")
		return nil, status.Error(codes.Internal, "slot reservation failed")
	}

	return &adminv1.AnnounceAgentResult{
		ExpiresAtUnix: now.Add(consts.AgentSlotTTL).Unix(),
	}, nil
}

// ListAgents returns a deterministic snapshot of every agent currently
// registered with the control plane. The thumbprint is exported as
// lowercase hex so a debugger can match `dev.clawker.cert-thumbprint`
// labels (or the bootstrap material on disk) against the entry the CP
// holds. RegisteredAt and LastSeen are emitted as Unix seconds (UTC) to
// avoid pulling google.protobuf.Timestamp into the AdminService surface
// for one read-only RPC.
func (s *adminServer) ListAgents(_ context.Context, _ *adminv1.ListAgentsRequest) (*adminv1.ListAgentsResult, error) {
	if s.agents == nil {
		return &adminv1.ListAgentsResult{}, nil
	}
	snap := s.agents.Snapshot()
	// Snapshot is already sorted by AgentName but a defensive sort here
	// keeps the wire output deterministic even if the registry's sort
	// invariant ever weakens.
	sort.Slice(snap, func(i, j int) bool { return snap[i].AgentName < snap[j].AgentName })

	out := make([]*adminv1.Agent, len(snap))
	for i, e := range snap {
		out[i] = &adminv1.Agent{
			AgentName:        e.AgentName,
			Project:          e.Project,
			ContainerId:      e.ContainerID,
			CertThumbprint:   hex.EncodeToString(e.Thumbprint[:]),
			RegisteredAtUnix: e.RegisteredAt.Unix(),
			LastSeenUnix:     e.LastSeen.Unix(),
		}
	}
	return &adminv1.ListAgentsResult{Agents: out}, nil
}
