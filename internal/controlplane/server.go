// Package controlplane implements the clawker control plane — a privileged
// long-lived gRPC service that owns authoritative state for managed
// containers. Serves the AdminService surface (CLI ↔ CP) and supplies
// the auth + lifecycle plumbing shared with the agent listener
// (clawkerd ↔ CP, registered separately by cmd/clawker-cp).
package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/auth"
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

// AnnounceAgent reserves a slot for an in-flight container startup. The
// CLI calls this before issuing client.ContainerStart so the slot is
// already in place when clawkerd boots and dials Connect. Validation is
// strict — every required field is non-empty and the cert thumbprint
// is exactly 64 lowercase-hex characters — because the slot record is
// the load-bearing identity binding for the subsequent Connect
// handshake.
//
// Wire-level error mapping:
//   - missing/malformed fields → codes.InvalidArgument
//   - duplicate composite (cert_thumbprint, agent_name, project) → codes.AlreadyExists
//   - any other Reserve failure → codes.Internal (logged at Warn so
//     operators have a triage trail; the wire response stays generic)
func (s *adminServer) AnnounceAgent(_ context.Context, req *adminv1.AnnounceAgentRequest) (*adminv1.AnnounceAgentResult, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	// Validate agent_name and project at the wire boundary using the
	// SAME typed constructors AgentService.Connect uses (see
	// internal/controlplane/agent/handler.go). Without this Announce
	// would accept names that Connect later rejects — the slot is
	// reserved successfully but cannot be consumed, so a malformed name
	// burns a slot for the full TTL. agent_name is identity-bearing so
	// dot-containing or canonical-prefix forms must be rejected here
	// (otherwise an attacker gets a memory churn primitive bounded only
	// by the rate limiter we don't yet have). Project is allowed to be
	// empty (matches docker.ContainerName's 2-segment naming) — that's
	// expressed by NewProjectSlug accepting "".
	if _, err := auth.NewAgentName(req.AgentName); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "agent_name: %v", err)
	}
	if _, err := auth.NewProjectSlug(req.Project); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "project: %v", err)
	}
	if req.ContainerId == "" {
		return nil, status.Error(codes.InvalidArgument, "container_id required")
	}
	if req.CodeChallenge == "" {
		return nil, status.Error(codes.InvalidArgument, "code_challenge required")
	}
	if req.CodeChallengeMethod != string(consts.ChallengeMethodS256) {
		return nil, status.Error(codes.InvalidArgument, "code_challenge_method must be S256")
	}

	// hex.DecodeString is case-insensitive but the proto contract is
	// lowercase. Enforce explicitly so two equally-valid CLI builds
	// can't disagree on case and so a future strict comparator
	// elsewhere in the codebase doesn't silently break the tolerant
	// branch.
	if strings.ToLower(req.ExpectedCertThumbprint) != req.ExpectedCertThumbprint {
		return nil, status.Error(codes.InvalidArgument, "expected_cert_thumbprint must be 64 lowercase hex characters")
	}
	raw, err := hex.DecodeString(req.ExpectedCertThumbprint)
	if err != nil || len(raw) != sha256.Size {
		return nil, status.Error(codes.InvalidArgument, "expected_cert_thumbprint must be 64 lowercase hex characters")
	}
	var thumbprint [sha256.Size]byte
	copy(thumbprint[:], raw)

	now := s.clock()
	slot := agentslots.Slot{
		AgentName: req.AgentName,
		// Project completes the composite slot identity so two agents
		// with the same short name in different projects don't collide.
		// Empty req.Project is intentional (matches docker.ContainerName
		// 2-segment naming) and accepted by agentslots.Reserve.
		Project:                req.Project,
		ContainerID:            req.ContainerId,
		ExpectedCertThumbprint: thumbprint,
		Challenge:              req.CodeChallenge,
		ChallengeMethod:        consts.ChallengeMethod(req.CodeChallengeMethod),
		// ReservedAt / ExpiresAt are stamped inside Reserve from the
		// registry's clock; the values set here are overwritten.
	}
	if err := s.slots.Reserve(slot); err != nil {
		if errors.Is(err, agentslots.ErrSlotExists) {
			return nil, status.Error(codes.AlreadyExists, "agent already announced")
		}
		// Any other Reserve failure indicates a CLI/CP wiring bug
		// (validation only catches user-facing input; Reserve's own
		// invariants — non-S256 method, empty agent — should be caught
		// above, so reaching this branch is a real signal). Log loudly
		// so an operator sees the underlying error; wire response stays
		// generic to avoid leaking internals. Tolerate a nil log here
		// for tests that build adminServer via struct literal — the
		// production path always sets log via NewAdminServer.
		log := s.log
		if log == nil {
			log = logger.Nop()
		}
		log.Warn().
			Err(err).
			Str("agent", req.AgentName).
			Str("project", req.Project).
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
