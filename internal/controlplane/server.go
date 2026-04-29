// Package controlplane implements the clawker control plane — a privileged
// long-lived gRPC service that owns authoritative state for managed
// containers. Serves the AdminService surface (CLI ↔ CP) and supplies
// the auth + lifecycle plumbing shared with the agent listener
// (clawkerd ↔ CP, registered separately by cmd/clawker-cp).
package controlplane

import (
	"context"
	"encoding/hex"
	"sort"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	fwhandler "github.com/schmitthub/clawker/internal/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/logger"
)

// adminServer composes the domain-specific handlers into the single
// AdminServiceServer surface. The firewall handler embeds
// UnimplementedAdminServiceServer so new RPCs default to
// codes.Unimplemented; explicit methods on adminServer (e.g.
// ListAgents) override that fallback.
type adminServer struct {
	// *fwhandler.Handler embeds adminv1.UnimplementedAdminServiceServer,
	// so new proto RPCs fail open with codes.Unimplemented via promotion
	// rather than blocking the whole CP on a partial domain rewrite.
	*fwhandler.Handler

	agents agentregistry.Registry
	log    *logger.Logger
}

// compile-time: any future additions to AdminServiceServer must be
// covered by one of the embedded domain handlers or this assertion fails.
var _ adminv1.AdminServiceServer = (*adminServer)(nil)

// NewAdminServer returns the composite AdminServiceServer wired from
// the supplied domain handlers.
//
//   - agents may be nil — when nil, ListAgents returns an empty result
//     so the CLI's `controlplane agents` command renders cleanly even
//     on a CP build that hasn't wired the agent registry yet.
//   - log defaults to logger.Nop() when nil. Production wiring passes
//     the CP's structured logger.
func NewAdminServer(fw *fwhandler.Handler, agents agentregistry.Registry, log *logger.Logger) adminv1.AdminServiceServer {
	if log == nil {
		log = logger.Nop()
	}
	return &adminServer{Handler: fw, agents: agents, log: log}
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
	// Snapshot is documented to return entries sorted by (Project,
	// AgentName); preserve that ordering on the wire.
	sort.Slice(snap, func(i, j int) bool {
		if snap[i].Project != snap[j].Project {
			return snap[i].Project < snap[j].Project
		}
		return snap[i].AgentName < snap[j].AgentName
	})

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
