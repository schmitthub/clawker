// Package controlplane implements the clawker control plane — a privileged
// long-lived gRPC service that owns authoritative state for managed
// containers. Serves the AdminService surface (CLI ↔ CP) and supplies
// the auth + lifecycle plumbing shared with the agent listener
// (clawkerd ↔ CP, registered separately by cmd/clawker-cp).
package controlplane

import (
	"context"
	"encoding/hex"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/controlplane/agent"
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

	agents agent.Registry
	log    *logger.Logger
}

// compile-time: any future additions to AdminServiceServer must be
// covered by one of the embedded domain handlers or this assertion fails.
var _ adminv1.AdminServiceServer = (*adminServer)(nil)

// NewAdminServer returns the composite AdminServiceServer wired from
// the supplied domain handlers. agents is required — CP is the sole
// sqlite writer in this design, so any wiring path that reaches here
// without a registry is a programming error.
//
//   - log defaults to logger.Nop() when nil. Production wiring passes
//     the CP's structured logger.
func NewAdminServer(fw *fwhandler.Handler, agents agent.Registry, log *logger.Logger) adminv1.AdminServiceServer {
	if log == nil {
		log = logger.Nop()
	}
	if agents == nil {
		panic("controlplane.NewAdminServer: agents registry is required")
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
	// Snapshot's interface contract guarantees (Project, AgentName)
	// ordering — trust it on the wire rather than re-sorting (avoids
	// duplicating the comparator across in-memory + sqlite impls and
	// this consumer).
	snap := s.agents.Snapshot()

	out := make([]*adminv1.Agent, len(snap))
	for i, e := range snap {
		out[i] = &adminv1.Agent{
			AgentName:        e.AgentName.String(),
			Project:          e.Project.String(),
			ContainerId:      e.ContainerID,
			CertThumbprint:   hex.EncodeToString(e.Thumbprint[:]),
			RegisteredAtUnix: e.RegisteredAt.Unix(),
			LastSeenUnix:     e.LastSeen.Unix(),
		}
	}
	return &adminv1.ListAgentsResult{Agents: out}, nil
}
