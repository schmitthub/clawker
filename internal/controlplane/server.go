// Package controlplane implements the clawker control plane — a privileged
// long-lived gRPC service that owns authoritative state for managed
// containers. Today the package serves only the AdminService surface
// (called by the host CLI) and supplies the auth + lifecycle plumbing
// shared with future listeners. Per-agent registration (AgentService,
// called by container-side clawkerd instances) lands with the agent
// listener in a later branch.
package controlplane

import (
	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	fwhandler "github.com/schmitthub/clawker/internal/controlplane/firewall"
)

// adminServer composes the domain-specific handlers into the single
// AdminServiceServer surface. Today only the firewall handler is
// embedded; future domains (monitor, hostproxy, agent inventory) plug in
// by embedding additional handlers whose Go method sets collectively
// satisfy AdminServiceServer via method promotion. Method-name
// collisions are prevented at the proto layer by the
// `<Domain><Action>[<Object>]` naming convention.
type adminServer struct {
	// *fwhandler.Handler embeds adminv1.UnimplementedAdminServiceServer,
	// so new proto RPCs fail open with codes.Unimplemented via promotion
	// rather than blocking the whole CP on a partial domain rewrite.
	*fwhandler.Handler
}

// compile-time: any future additions to AdminServiceServer must be
// covered by one of the embedded domain handlers or this assertion fails.
var _ adminv1.AdminServiceServer = (*adminServer)(nil)

// NewAdminServer returns the composite AdminServiceServer wired from the
// supplied domain handlers.
func NewAdminServer(fw *fwhandler.Handler) adminv1.AdminServiceServer {
	return &adminServer{Handler: fw}
}
