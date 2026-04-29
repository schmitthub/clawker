package agentregistry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

// ContainerLister enumerates every container ID currently known to the
// docker daemon for a given purpose. The implementation MUST include
// stopped/exited containers — a stopped container can be `docker
// start`-ed back into life, and its registry row should survive that
// transition. Only `docker rm` (destroy) means the container is
// genuinely gone and the row is orphaned.
type ContainerLister func(ctx context.Context) ([]string, error)

// reapListerMaxAttempts caps the bounded retry on transient docker
// daemon failures. The Reaper runs once at CP startup, so a brief
// daemon-restart window during boot must not skip the first sweep
// entirely (the dockerevents subscription only catches NEW destroys
// from that point forward).
const reapListerMaxAttempts = 3

// Reap drops every registry row whose container_id is not present in
// the lister's snapshot. Used at CP startup to heal the registry
// against containers that were removed while the CP was down. The
// dockerevents subscription handles the steady-state case where a
// container is destroyed while CP is up.
//
// The lister is retried with exponential backoff (3 attempts:
// 100ms → 200ms → 400ms) before giving up — a transient docker
// daemon hiccup at CP startup should not cause Reap to skip.
//
// Returns the count of evicted rows for the caller's startup log.
// Per-row eviction errors are aggregated into the returned error so
// the caller can surface them; the count reflects only successful
// evictions. A nil registry or nil lister panics — both are
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

	ids, err := listWithRetry(ctx, lister, log)
	if err != nil {
		return 0, fmt.Errorf("listing containers: %w", err)
	}
	live := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		live[id] = struct{}{}
	}

	snap := reg.Snapshot()
	evicted := 0
	var evictErrs []error
	for _, e := range snap {
		if _, ok := live[e.ContainerID]; ok {
			continue
		}
		if err := reg.EvictByContainerID(e.ContainerID); err != nil {
			log.Error().
				Err(err).
				Str("container_id", e.ContainerID).
				Str("agent", e.AgentName).
				Msg("agentregistry: reap evict failed")
			evictErrs = append(evictErrs, fmt.Errorf("evict %s: %w", e.ContainerID, err))
			continue
		}
		evicted++
	}
	if evicted > 0 {
		log.Info().
			Int("evicted", evicted).
			Int("registry_size_before", len(snap)).
			Int("live_containers", len(live)).
			Msg("agentregistry: reaped orphan rows")
	}
	if len(evictErrs) > 0 {
		return evicted, errors.Join(evictErrs...)
	}
	return evicted, nil
}

// listWithRetry calls the lister with bounded exponential backoff. Stops
// early on ctx.Done(). Returns the last error if every attempt fails.
func listWithRetry(ctx context.Context, lister ContainerLister, log *logger.Logger) ([]string, error) {
	var lastErr error
	backoff := 100 * time.Millisecond
	for attempt := 1; attempt <= reapListerMaxAttempts; attempt++ {
		ids, err := lister(ctx)
		if err == nil {
			if attempt > 1 {
				log.Info().
					Int("attempt", attempt).
					Msg("agentregistry: reap lister recovered after retry")
			}
			return ids, nil
		}
		lastErr = err
		if attempt == reapListerMaxAttempts {
			break
		}
		log.Warn().
			Err(err).
			Int("attempt", attempt).
			Dur("backoff", backoff).
			Msg("agentregistry: reap lister failed; retrying")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return nil, lastErr
}
