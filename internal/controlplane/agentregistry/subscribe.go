package agentregistry

import (
	"context"

	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/informer"
)

// Subscribe wires the registry to container deltas published by the
// dockerevents feeder via the informer. The returned cleanup must be
// deferred by the caller; it cancels the informer subscription and
// waits for the consumer goroutine to drain.
//
// Eviction triggers:
//   - DeltaRemoved (Docker destroy/remove) — container is gone.
//   - DeltaUpdated where Lifecycle becomes "stopped" (Docker die / stop /
//     kill) — the container is no longer running. clawkerd's mTLS
//     connection has dropped; the registry entry must follow.
//
// Pause/unpause are not eviction triggers: the agent process is alive,
// just frozen, and the existing mTLS connection remains valid.
func Subscribe(ctx context.Context, reg Registry, inf informer.Interface) func() {
	_, ch, cancel := inf.Subscribe(informer.Filter{
		Kinds: []string{dockerevents.KindContainer},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case d, ok := <-ch:
				if !ok {
					return
				}
				handleDelta(d, reg)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func handleDelta(d informer.Delta, reg Registry) {
	switch d.Kind {
	case informer.DeltaRemoved:
		// DeltaRemoved soft-deletes: After carries the resource with
		// Lifecycle=LifecycleGone. Before is set to the prior state if
		// the resource was previously visible. Either side gives us the
		// container ID we need.
		switch {
		case d.After != nil:
			reg.EvictByContainerID(d.After.ID)
		case d.Before != nil:
			reg.EvictByContainerID(d.Before.ID)
		}
	case informer.DeltaUpdated:
		if d.After != nil && d.After.Lifecycle == dockerevents.LifecycleStopped {
			reg.EvictByContainerID(d.After.ID)
		}
	}
}
