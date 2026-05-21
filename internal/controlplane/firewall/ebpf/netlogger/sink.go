package netlogger

import "context"

// Sink consumes enriched egress events. Implementations MUST NOT block
// — the processor goroutine is single-threaded and a blocking sink
// stalls the kernel→userspace drain pipeline, refilling the ringbuf
// and causing kernel-fault drops counted in events_drops.
//
// The OTel SDK's BatchProcessor satisfies this contract by design
// (OnEmit returns immediately and batches in its own goroutine).
type Sink interface {
	Emit(ctx context.Context, ev Event)
}

// nopSink discards every event. Used internally as the default when
// no OtelLoggerProvider is wired (test injections, degraded CP boot).
type nopSink struct{}

func (nopSink) Emit(context.Context, Event) {}
