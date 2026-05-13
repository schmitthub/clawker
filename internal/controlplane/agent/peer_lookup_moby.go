package agent

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	mobyclient "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// peerLookupTimeout bounds the read-only docker calls inside the
// resolver. Decoupled from the per-RPC ctx (parent = Background) so a
// CP-side cancel does not abort the lookup mid-flight and turn it
// into a spurious ErrNoContainerForPeerIP. 5s is comfortably more than
// docker daemon p99 inspect latency.
const peerLookupTimeout = 5 * time.Second

// peerLookupAPI is the subset of mobyclient.APIClient the resolver
// needs — narrowed for testability.
type peerLookupAPI interface {
	ContainerList(ctx context.Context, opts mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error)
	ContainerInspect(ctx context.Context, id string, opts mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error)
}

// MobyPeerLookup is the production ContainerByPeerIP backed by the
// Docker daemon.
type MobyPeerLookup struct {
	cli peerLookupAPI
	log *logger.Logger
}

// NewMobyPeerLookup wraps a moby APIClient as a ContainerByPeerIP
// resolver. log defaults to logger.Nop() when nil.
func NewMobyPeerLookup(cli mobyclient.APIClient, log *logger.Logger) *MobyPeerLookup {
	if log == nil {
		log = logger.Nop()
	}
	return &MobyPeerLookup{cli: cli, log: log}
}

// LookupByIP walks every `purpose=agent` container and returns the
// one whose clawker-net endpoint IP matches ip. The walk is
// exhaustive: ambiguous-IP advertisements (multiple containers with
// overlapping endpoint state during restart cycles) return
// ErrAmbiguousPeerIP rather than picking the first match. A
// transient per-container inspect failure is logged and iteration
// continues. A daemon-error wrap is returned only when NO candidate
// could be inspected — a real no-match is reported as
// ErrNoContainerForPeerIP even when one inspect along the way
// failed, so the peer_lookup_no_match audit signal stays useful.
func (m *MobyPeerLookup) LookupByIP(ctx context.Context, ip netip.Addr) (ResolvedContainer, error) {
	lookupCtx, cancel := context.WithTimeout(context.Background(), peerLookupTimeout)
	defer cancel()

	filter := mobyclient.Filters{}.
		Add("label", consts.LabelPurpose+"="+consts.PurposeAgent)
	res, err := m.cli.ContainerList(lookupCtx, mobyclient.ContainerListOptions{Filters: filter})
	if err != nil {
		m.log.Error().Err(err).
			Stringer("peer_ip", ip).
			Str("event", "peer_lookup_list_failed").
			Msg("docker ContainerList failed during peer-IP resolution")
		return ResolvedContainer{}, fmt.Errorf("peer lookup: list containers: %w", err)
	}

	wanted := ip.Unmap()
	// Walk every candidate even after finding a match so we can detect
	// ambiguous peer-IP advertisements (stale endpoints during restart
	// cycles). matches holds (container_id, labels) for the typed-identity
	// validation that follows the duplicate check.
	type match struct {
		containerID string
		labels      map[string]string
	}
	var matches []match
	var inspectedOK int
	var lastInspectErr error

	for _, summary := range res.Items {
		inspect, err := m.cli.ContainerInspect(lookupCtx, summary.ID, mobyclient.ContainerInspectOptions{})
		if err != nil {
			m.log.Warn().Err(err).
				Str("container_id", summary.ID).
				Stringer("peer_ip", ip).
				Str("event", "peer_lookup_inspect_failed").
				Msg("docker ContainerInspect failed during peer-IP resolution; continuing to next candidate")
			lastInspectErr = err
			continue
		}
		inspectedOK++
		c := inspect.Container
		if c.NetworkSettings == nil {
			continue
		}
		endpoint, ok := c.NetworkSettings.Networks[consts.Network]
		if !ok || !endpoint.IPAddress.IsValid() {
			continue
		}
		if endpoint.IPAddress.Unmap() != wanted {
			continue
		}
		var labels map[string]string
		if c.Config != nil {
			labels = c.Config.Labels
		}
		matches = append(matches, match{containerID: c.ID, labels: labels})
	}

	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, mm := range matches {
			ids = append(ids, mm.containerID)
		}
		m.log.Error().
			Stringer("peer_ip", ip).
			Strs("container_ids", ids).
			Str("event", "peer_lookup_ambiguous_match").
			Msg("multiple purpose=agent containers advertise the same clawker-net IP — failing closed")
		return ResolvedContainer{}, ErrAmbiguousPeerIP
	}

	if len(matches) == 1 {
		mm := matches[0]
		project, perr := auth.NewProjectSlug(mm.labels[consts.LabelProject])
		if perr != nil {
			m.log.Error().Err(perr).
				Str("container_id", mm.containerID).
				Stringer("peer_ip", ip).
				Str("event", "peer_lookup_invalid_labels").
				Msg("matched container has malformed project label")
			return ResolvedContainer{}, fmt.Errorf("%w: container %s project: %w", ErrInvalidAgentLabels, mm.containerID, perr)
		}
		agentName, aerr := auth.NewAgentName(mm.labels[consts.LabelAgent])
		if aerr != nil {
			m.log.Error().Err(aerr).
				Str("container_id", mm.containerID).
				Stringer("peer_ip", ip).
				Str("event", "peer_lookup_invalid_labels").
				Msg("matched container has malformed agent label")
			return ResolvedContainer{}, fmt.Errorf("%w: container %s agent: %w", ErrInvalidAgentLabels, mm.containerID, aerr)
		}
		return ResolvedContainer{
			ContainerID: mm.containerID,
			Project:     project,
			AgentName:   agentName,
		}, nil
	}

	// No match. Only escalate to a daemon-error wrap when NO candidate
	// inspected cleanly — otherwise we have a complete view of the
	// `purpose=agent` containers' endpoints and the absence is
	// authoritative. Misclassifying a real no-match as "daemon error"
	// would suppress the peer_lookup_no_match audit signal operators
	// rely on for "agents connecting from unexpected IPs".
	if inspectedOK == 0 && lastInspectErr != nil {
		return ResolvedContainer{}, fmt.Errorf("peer lookup: all candidate inspects failed: %w", lastInspectErr)
	}
	return ResolvedContainer{}, ErrNoContainerForPeerIP
}
