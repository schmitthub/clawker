package netlogger

import (
	"errors"

	"github.com/cilium/ebpf/ringbuf"

	"github.com/schmitthub/clawker/internal/logger"
)

// readerSource is the iteration surface the reader goroutine consumes.
// The cilium/ebpf *ringbuf.Reader satisfies it; tests inject a fake so
// they don't need a real BPF ringbuf (which requires CAP_BPF that the
// clawker dev container lacks).
type readerSource interface {
	ReadInto(rec *ringbuf.Record) error
}

// reader drains the events_ringbuf, copies each record into a fresh
// byte slice, and forwards it on the queue channel. The byte slice
// is owned by the queue from the moment of send; the processor parses
// it on the other side.
//
// Single goroutine — the ringbuf.Reader serialises internally and
// concurrent readers would just contend on its mutex.
//
// Shutdown: Service.Stop closes the *ringbuf.Reader; the blocked
// ReadInto returns ringbuf.ErrClosed and the loop returns.
type reader struct {
	src     readerSource
	queue   chan<- []byte
	metrics *Metrics
	log     *logger.Logger
}

// drain runs the read loop. Recovers from any panic to honour CP
// no-panic discipline — a bad record must not strand the BPF
// programs pinned with no userspace consumer.
func (r *reader) drain() {
	defer func() {
		if rec := recover(); rec != nil {
			r.log.Error().
				Interface("panic", rec).
				Str("event", "netlogger_reader_panic").
				Msg("netlogger ringbuf reader panicked — netlogger will be unavailable")
		}
	}()
	var rec ringbuf.Record
	for {
		if err := r.src.ReadInto(&rec); err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				return
			}
			r.metrics.RingbufErrors.Inc()
			r.log.Warn().
				Err(err).
				Str("event", "netlogger_ringbuf_error").
				Msg("ringbuf read error")
			continue
		}
		r.metrics.RingbufReceived.Inc()

		// Copy: ringbuf.RawSample is reused on next ReadInto. The
		// copy makes the queue's slice independently owned, which
		// keeps the processor's parse step free of any reader-side
		// aliasing concerns. Allocation per record is acceptable
		// — records are ~32 bytes and bounded at ~640/sec/cgroup
		// by the BPF rate limiter.
		buf := make([]byte, len(rec.RawSample))
		copy(buf, rec.RawSample)

		select {
		case r.queue <- buf:
		default:
			// Queue full → drop newest. The reader MUST NOT block:
			// a stalled reader stalls the ringbuf, which causes
			// upstream bpf_ringbuf_reserve to fail and increments
			// the BPF events_drops counter. Dropping at the
			// userspace queue is bounded back-pressure;
			// kernel-side drops are not.
			r.metrics.QueueDropped.Inc()
		}
	}
}
