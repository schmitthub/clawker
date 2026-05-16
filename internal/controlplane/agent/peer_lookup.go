package agent

import (
	"context"
	"errors"
	"net/netip"

	"github.com/schmitthub/clawker/internal/auth"
)

// ResolvedContainer is the authoritative identity of an agent
// container as resolved from its live mTLS peer IP. Project and
// AgentName are typed so a zero-value instance cannot carry empty
// strings into a downstream identity comparison; the resolver
// constructs these via auth.NewProjectSlug / auth.NewAgentName and
// rejects malformed labels at the seam.
type ResolvedContainer struct {
	ContainerID string
	Project     auth.ProjectSlug
	AgentName   auth.AgentName
}

// ErrNoContainerForPeerIP is returned when no `purpose=agent`
// container on the clawker-net network has an endpoint IP matching
// the requested peer IP. Callers MUST treat this as a hard
// authentication failure.
var ErrNoContainerForPeerIP = errors.New("no purpose=agent container with matching clawker-net IP")

// ErrInvalidAgentLabels is returned when the container matched by
// peer IP carries malformed or missing dev.clawker.project /
// dev.clawker.agent labels. Distinguishing this from a clean no-match
// lets the trust gate emit a daemon-state diagnostic instead of a
// generic auth reject.
var ErrInvalidAgentLabels = errors.New("agent container has invalid identity labels")

// ErrAmbiguousPeerIP is returned when two or more `purpose=agent`
// containers on clawker-net advertise endpoints with the same peer
// IP. Docker can transiently leave stale endpoints in
// NetworkSettings.Networks during restart cycles, and grounding the
// trust anchor on the first match would create a race window. Fail
// closed instead — operators see a distinct event and the trust gate
// rejects the RPC until the daemon state converges.
var ErrAmbiguousPeerIP = errors.New("multiple purpose=agent containers match peer IP")

// ContainerByPeerIP resolves a live mTLS peer IP to the
// `purpose=agent` container owning that IP on clawker-net. Returns
// ErrNoContainerForPeerIP when nothing matches,
// ErrInvalidAgentLabels when the matching container's labels can't
// form a valid identity, ErrAmbiguousPeerIP when two or more
// containers share the peer IP (Docker restart-race window — fails
// closed), or a wrapped daemon error.
type ContainerByPeerIP interface {
	LookupByIP(ctx context.Context, ip netip.Addr) (ResolvedContainer, error)
}
