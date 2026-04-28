package agentregistry

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/logger"
)

// ContainerLister enumerates every container ID currently known to the
// docker daemon for a given purpose. The implementation MUST include
// stopped/exited containers — a stopped container can be `docker
// start`-ed back into life, and its registry row should survive that
// transition. Only `docker rm` (destroy) means the container is
// genuinely gone and the row is orphaned.
type ContainerLister func(ctx context.Context) ([]string, error)

// Reap drops every registry row whose container_id is not present in
// the lister's snapshot. Used at CP startup to heal the registry
// against containers that were removed while the CP was down. The
// dockerevents subscription handles the steady-state case where a
// container is destroyed while CP is up.
//
// Exit error means the lister failed (transient docker daemon issue).
// On success returns the count of rows evicted so the caller can log
// a summary line. A nil registry or nil lister panics — both are
// programming errors the caller can catch in development.
func Reap(ctx context.Context, reg Registry, lister ContainerLister, log *logger.Logger) (int, error) {
	if reg == nil {
		panic("agentregistry: Reap called with nil Registry")
	}
	if lister == nil {
		panic("agentregistry: Reap called with nil ContainerLister")
	}
	if log == nil {
		log = logger.Nop()
	}

	ids, err := lister(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing containers: %w", err)
	}
	live := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		live[id] = struct{}{}
	}

	snap := reg.Snapshot()
	evicted := 0
	for _, e := range snap {
		if _, ok := live[e.ContainerID]; ok {
			continue
		}
		reg.EvictByContainerID(e.ContainerID)
		evicted++
	}
	if evicted > 0 {
		log.Info().
			Int("evicted", evicted).
			Int("registry_size_before", len(snap)).
			Int("live_containers", len(live)).
			Msg("agentregistry: reaped orphan rows")
	}
	return evicted, nil
}
