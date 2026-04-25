package agentregistry

import (
	"context"

	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/informer"
	"github.com/schmitthub/clawker/internal/logger"
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
//
// log is required (pass logger.Nop() in tests that don't care about the
// audit trail). A nil logger is replaced with logger.Nop() so callers
// can't accidentally trip a nil deref inside the panic recovery path.
func Subscribe(ctx context.Context, reg Registry, inf informer.Interface, log *logger.Logger) func() {
	if log == nil {
		log = logger.Nop()
	}
	_, ch, cancel := inf.Subscribe(informer.Filter{
		Kinds: []string{dockerevents.KindContainer},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Recover-and-resume so a panic in EvictByContainerID (or
		// any future per-delta hook) doesn't silently kill the
		// consumer. A dead consumer would leave registered agents
		// alive in the registry after their containers exit, and
		// stale Thumbprint entries would keep authorizing per-agent
		// RPCs against a container that is gone. Mirrors the recover
		// pattern in cmd/clawker-cp/main.go's informer stats
		// heartbeat goroutine, but loops back into the consumer so
		// subsequent deltas are still processed — the dropped delta
		// that triggered the panic is lost, but the next one is not.
		for {
			if drainOnce(ctx, ch, reg, log) {
				return
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

// drainOnce runs the delta consumer until ctx is done, the channel is
// closed, or a panic in handleDelta unwinds. Returns true when the
// consumer is finished for good (ctx canceled or channel closed) and
// false when it should be restarted (panic recovered). Split out from
// Subscribe so the deferred recover has its own stack frame — defining
// it inline would require an immediately-invoked closure for the same
// effect and read worse.
func drainOnce(ctx context.Context, ch <-chan informer.Delta, reg Registry, log *logger.Logger) (terminate bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("agentregistry subscribe consumer panicked; resuming")
			terminate = false
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return true
		case d, ok := <-ch:
			if !ok {
				return true
			}
			handleDelta(d, reg)
		}
	}
}
