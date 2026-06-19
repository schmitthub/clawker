package agent

import (
	"context"

	mobyclient "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/config"
)

// ContainerLister enumerates managed `purpose=agent` container IDs from
// the docker daemon. It is the production source behind every agent-axis
// container listing in the CP orchestrator:
//
//   - Firewall handler + AgentWatcher list running-only (ListOpts{}).
//   - Reaper / initial-dial reconciler list running + stopped
//     (ListOpts{All: true}) — a stopped container can be `docker
//     start`-ed back into life, and its registry row should survive that
//     transition.
//
// The label filter is non-overridable: every List call narrows to BOTH
// the managed label AND purpose=agent, so a caller can't accidentally
// widen scope past purpose=agent. Construct once at CP startup; share
// across consumers.
type ContainerLister struct {
	dc  mobyclient.APIClient
	cfg config.Config
}

// ListOpts controls the scope of a ContainerLister.List call. All=false
// (the zero value) restricts to running containers; All=true includes
// stopped/exited containers.
type ListOpts struct {
	All bool
}

// NewContainerLister wraps a moby APIClient as the managed-agent
// container enumerator. The label filter keys/values are read from cfg
// so the lister stays consistent with the rest of the CP's resource
// filtering.
func NewContainerLister(dc mobyclient.APIClient, cfg config.Config) *ContainerLister {
	return &ContainerLister{dc: dc, cfg: cfg}
}

// List returns the container IDs of every managed purpose=agent
// container matching opts. The label filter is non-overridable — both
// the managed label and purpose=agent are always applied so the scope
// can never widen past purpose=agent.
func (l *ContainerLister) List(ctx context.Context, opts ListOpts) ([]string, error) {
	filter := mobyclient.Filters{}.
		Add("label", l.cfg.LabelManaged()+"="+l.cfg.ManagedLabelValue()).
		Add("label", l.cfg.LabelPurpose()+"="+l.cfg.PurposeAgent())
	result, err := l.dc.ContainerList(ctx, mobyclient.ContainerListOptions{
		All:     opts.All,
		Filters: filter,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(result.Items))
	for _, c := range result.Items {
		ids = append(ids, c.ID)
	}
	return ids, nil
}
