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
// into a spurious ErrNoContainerForPeerIP. Mirrors inspectTimeout in
// register_handler.go.
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

// LookupByIP iterates `purpose=agent` containers and returns the
// first one whose clawker-net endpoint IP matches ip. A transient
// per-container inspect failure is logged and the iteration
// continues; if no match is found and any candidate erred during
// inspect, the wrapped daemon error is returned so callers can
// distinguish "no agent owns this IP" from "we couldn't tell".
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
	var inspectErr error
	for _, summary := range res.Items {
		inspect, err := m.cli.ContainerInspect(lookupCtx, summary.ID, mobyclient.ContainerInspectOptions{})
		if err != nil {
			m.log.Warn().Err(err).
				Str("container_id", summary.ID).
				Stringer("peer_ip", ip).
				Str("event", "peer_lookup_inspect_failed").
				Msg("docker ContainerInspect failed during peer-IP resolution; continuing to next candidate")
			inspectErr = err
			continue
		}
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
		project, perr := auth.NewProjectSlug(labels[consts.LabelProject])
		if perr != nil {
			m.log.Error().Err(perr).
				Str("container_id", c.ID).
				Stringer("peer_ip", ip).
				Str("event", "peer_lookup_invalid_labels").
				Msg("matched container has malformed project label")
			return ResolvedContainer{}, fmt.Errorf("%w: container %s project: %w", ErrInvalidAgentLabels, c.ID, perr)
		}
		agentName, aerr := auth.NewAgentName(labels[consts.LabelAgent])
		if aerr != nil {
			m.log.Error().Err(aerr).
				Str("container_id", c.ID).
				Stringer("peer_ip", ip).
				Str("event", "peer_lookup_invalid_labels").
				Msg("matched container has malformed agent label")
			return ResolvedContainer{}, fmt.Errorf("%w: container %s agent: %w", ErrInvalidAgentLabels, c.ID, aerr)
		}
		return ResolvedContainer{
			ContainerID: c.ID,
			Project:     project,
			AgentName:   agentName,
		}, nil
	}
	if inspectErr != nil {
		return ResolvedContainer{}, fmt.Errorf("peer lookup: no match and at least one inspect failed: %w", inspectErr)
	}
	return ResolvedContainer{}, ErrNoContainerForPeerIP
}
