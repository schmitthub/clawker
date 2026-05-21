package netlogger

import (
	"github.com/prometheus/client_golang/prometheus"
)

// NOTE (deferred): the counters defined here are incremented in
// process today but never registered with a prometheus.Registerer
// because CP has no /metrics scrape endpoint. A follow-up PR will
// wire scraping (either an HTTP exporter alongside /healthz or a
// push-style pipeline through the OTel collector) along with the
// gauge dimensions the initiative_ebpf-netlogger doc listed for Task
// 3 — events_drops summed across CPUs, ratelimit_drops per cgroup,
// and the OTel-export success/failure counters from a counting
// exporter wrap. Until then, the values exist purely for in-process
// runtime introspection and are not visible outside the CP process.
//
// Metrics groups every Prom counter the netlogger pipeline bumps in
// its hot path. Counters are created unregistered; callers wire
// MustRegister with a prometheus.Registerer.
//
// Counters created with prometheus.NewCounter accept Inc calls
// whether or not they have been registered with a Registerer. The
// reader and processor goroutines bump counters unconditionally —
// tests that don't supply a registry get the same code path, just
// no scrape endpoint exposure.
//
// Field semantics:
//   - RingbufReceived: incremented once per successful ringbuf record
//     read. Diverging from QueueReceived signals a stuck processor.
//   - RingbufErrors: incremented once per non-ErrClosed read failure
//     in the reader goroutine. Sustained growth → kernel or library
//     misbehavior.
//   - QueueDropped: incremented when the bounded queue is full and
//     the reader drops the newest record (non-blocking send semantics
//     protect the ringbuf from back-pressure stalls).
//   - QueueReceived: incremented once per record the processor pulls
//     from the queue. (RingbufReceived − QueueReceived − QueueDropped)
//     is the in-flight backlog.
//   - ParseErrors: incremented when the binary decode of a raw record
//     fails. Sustained growth → BPF/Go ABI drift.
//   - EmitSucceeded: incremented once per Sink.Emit return.
//
// Kernel-side drop dimensions (events_drops PERCPU_ARRAY,
// ratelimit_drops HASH) live as gauges refreshed periodically by the
// CP wiring — they read BPF state directly rather than being bumped
// from Go code paths, so they aren't declared here.
type Metrics struct {
	RingbufReceived prometheus.Counter
	RingbufErrors   prometheus.Counter
	QueueDropped    prometheus.Counter
	QueueReceived   prometheus.Counter
	ParseErrors     prometheus.Counter
	EmitSucceeded   prometheus.Counter
}

// NewMetrics constructs an unregistered Metrics. Callers wanting
// scrape exposure pass the result through MustRegister. Tests use
// the result directly and never register.
func NewMetrics() *Metrics {
	return &Metrics{
		RingbufReceived: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "clawker_netlogger_ringbuf_received_total",
			Help: "Total egress ringbuf records successfully read from the kernel.",
		}),
		RingbufErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "clawker_netlogger_ringbuf_errors_total",
			Help: "Total non-shutdown ringbuf read errors observed by the netlogger reader.",
		}),
		QueueDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "clawker_netlogger_queue_dropped_total",
			Help: "Total records dropped because the userspace queue between reader and processor was full.",
		}),
		QueueReceived: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "clawker_netlogger_queue_received_total",
			Help: "Total records the processor pulled off the userspace queue.",
		}),
		ParseErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "clawker_netlogger_parse_errors_total",
			Help: "Total records the processor failed to decode into an Event.",
		}),
		EmitSucceeded: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "clawker_netlogger_emit_succeeded_total",
			Help: "Total enriched events handed off to the Sink without error.",
		}),
	}
}

// MustRegister registers every counter on the supplied Registerer.
// Panics on duplicate registration — matches the project-wide
// pattern.
func (m *Metrics) MustRegister(reg prometheus.Registerer) {
	reg.MustRegister(
		m.RingbufReceived,
		m.RingbufErrors,
		m.QueueDropped,
		m.QueueReceived,
		m.ParseErrors,
		m.EmitSucceeded,
	)
}
