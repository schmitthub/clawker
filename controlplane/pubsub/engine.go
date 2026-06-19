// Package pubsub is the Clawker Control Plane's type-safe, in-memory pub/sub
// engine: a dumb pipe that transports strongly-typed enveloped events and
// nothing else. It has no notion of state, stores, or any domain — it knows
// about envelopes and subscribers, never about agents, containers, or
// firewalls. Domains depend on this package; this package depends only on
// internal/logger (for audit lines) and github.com/rs/zerolog (for the
// LogObjectMarshaler type the single internal audit hook embeds — the one
// tolerated type assertion; there is no payload-identity interface).
//
// The pipe emits exactly one structured audit line per accepted Publish via a
// single internal hook NewTopic attaches per topic; the orchestrator wires
// ZERO hooks and there is no public hook surface. The audit line keys on the
// envelope Source + Timestamp, never on a payload method.
//
// Resilience is the package's reason to be careful. In the Clawker CP a panic
// is a security incident, not an availability one: it kills PID 1, skips
// drain-to-zero, and leaves eBPF programs pinned and unsupervised while the
// user believes the firewall is enforcing. Because the bus sits on the hot
// path of every CP event, the engine never fans out via a bare
// `go handler(event)`: every handler invocation runs under recover, each
// subscriber owns a bounded buffer with counted drop-oldest on overflow,
// Publish is non-blocking with a back-pressure signal, and construction
// returns (*Topic[T], error) rather than panicking.
package pubsub

import (
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/schmitthub/clawker/internal/logger"
)

// DefaultBuffer is the per-subscriber queue depth used when no WithBuffer
// option is supplied. It bounds how many undelivered events a slow consumer
// may hold before drop-oldest begins evicting.
const DefaultBuffer = 256

// Audit log messages and structured field keys. The pipe wires no per-producer
// hook and spells no domain identity; these are the only strings it emits.
const (
	logMsgSubscriberPanic = "pubsub: subscriber panicked"
	logMsgDrainPanic      = "pubsub: subscriber drain loop panicked"
	logMsgHookPanic       = "pubsub: audit hook panicked"

	logFieldPanic     = "panic"
	logFieldEvent     = "event"
	logFieldSource    = "source"
	logFieldTimestamp = "timestamp"

	eventSubscriberPanic = "pubsub_subscriber_panic"
	eventDrainPanic      = "pubsub_drain_panic"
	eventHookPanic       = "pubsub_hook_panic"
)

// Sentinel errors let callers classify construction and delivery outcomes
// without string matching.
var (
	// ErrNilLogger is returned by NewTopic when no logger is provided. The
	// engine logs recovered panics for audit, so a logger is mandatory.
	ErrNilLogger = errors.New("pubsub: logger must not be nil")
	// ErrInvalidBuffer is returned by NewTopic when the configured
	// per-subscriber buffer is not a positive integer.
	ErrInvalidBuffer = errors.New("pubsub: buffer size must be positive")
	// ErrTopicClosed classifies a publish to a closed topic. Publish itself
	// signals this via a false return; the sentinel is for callers that want
	// to reason about the cause.
	ErrTopicClosed = errors.New("pubsub: topic is closed")
)

// Event wraps a strongly-typed payload with routing metadata. The four fields
// are the entire envelope: the raw domain struct rides in Payload, separating
// domain data from routing metadata. Payload is a plain T with no method
// contract — consumers read it natively (event.Payload.Action) with no type
// assertion and no JSON.
type Event[T any] struct {
	// ID identifies this event instance.
	ID string
	// Timestamp is the event time as Unix nanoseconds (time.Now().UnixNano()),
	// a compact, monotonic-friendly in-memory value.
	Timestamp int64
	// Source names the producer that published the event.
	Source string
	// Payload is the strongly-typed domain struct.
	Payload T
}

// auditHook is the single, internal publish-side audit emitter. It is NOT
// exported and NOT orchestrator-wired: NewTopic attaches exactly one per topic,
// so a producer (the orchestrator) never hand-wires hooks and the pipe carries
// no per-producer middleware surface. The hook keys each line on the ENVELOPE
// (Source + Timestamp) — never on a payload identity method — and embeds the
// payload's structured fields when it implements zerolog.LogObjectMarshaler.
// The zerolog.LogObjectMarshaler assertion is the ONE type assertion the pipe
// tolerates; there is no payload-identity interface (no EventName/OccurredAt).
type auditHook[T any] func(Event[T])

// newAuditHook builds the per-topic audit emitter NewTopic attaches. It emits
// one structured Info line per published event, keyed on the ENVELOPE: the
// "source" field and "event" message come from ev.Source, and "timestamp" from
// ev.Timestamp (Unix nanoseconds → time.Unix(0, ts)). When the payload
// implements zerolog.LogObjectMarshaler its per-type identity fields are
// embedded; the assertion to that interface is the ONLY type assertion the pipe
// performs and the only thing it asks of a payload. A payload implementing it
// is the norm (domains keep MarshalZerologObject); one that does not still
// produces a Source-keyed line. No payload-identity interface (EventName/
// OccurredAt) is consulted — that legacy contract is gone.
func newAuditHook[T any](log *logger.Logger) auditHook[T] {
	if log == nil {
		log = logger.Nop()
	}
	return func(ev Event[T]) {
		entry := log.Info().
			Str(logFieldSource, ev.Source).
			Time(logFieldTimestamp, time.Unix(0, ev.Timestamp))
		if m, ok := any(ev.Payload).(zerolog.LogObjectMarshaler); ok {
			entry = entry.EmbedObject(m)
		}
		entry.Msg(ev.Source)
	}
}

// Stats is a snapshot of a topic's pipe counters. It carries ONLY transport
// metrics — the engine holds no application state, so there are no
// domain-specific fields here.
type Stats struct {
	// Subscribers is the number of currently-registered handlers.
	Subscribers int
	// QueueCapacity is the per-subscriber bounded buffer depth.
	QueueCapacity int
	// PublishedTotal is the count of events accepted by Publish (returned true).
	PublishedTotal int64
	// DroppedTotal is the count of events evicted by drop-oldest across all
	// subscriber buffers.
	DroppedTotal int64
	// PanicsRecovered is the count of subscriber panics contained by recover.
	PanicsRecovered int64
	// HookPanicsRecovered is the count of audit-hook panics contained by
	// recover. Tracked separately from PanicsRecovered so an operator can tell
	// a panicking payload MarshalZerologObject (in the audit hook) from a buggy
	// subscriber.
	HookPanicsRecovered int64
}

// Option configures a Topic at construction time.
type Option func(*topicOptions)

type topicOptions struct {
	buffer int
}

// WithBuffer sets the per-subscriber bounded buffer depth. A non-positive
// value makes NewTopic return ErrInvalidBuffer rather than panicking.
func WithBuffer(n int) Option {
	return func(o *topicOptions) { o.buffer = n }
}

// subscriber owns one handler and its bounded delivery queue. Each subscriber
// drains on its own goroutine so a slow consumer isolates itself: it can fill
// and drop-oldest from its own buffer without blocking the bus or starving
// other subscribers.
type subscriber[T any] struct {
	handler func(Event[T])
	queue   chan Event[T]
}

// Topic manages subscribers for a SPECIFIC generic schema type T. It is the
// only public surface of the pipe; there is no multi-topic bus that erases to
// any on the public API.
type Topic[T any] struct {
	log    *logger.Logger
	buffer int

	// hook is the single internal audit emitter. It is set once by NewTopic
	// and never mutated thereafter, so it needs no lock — Publish reads it on
	// the hot path without contending with subscribe/close.
	hook auditHook[T]

	mu          sync.RWMutex
	subscribers []*subscriber[T]
	closed      bool

	// Counters are guarded by counterMu, separate from mu, so delivery and
	// publish accounting never contend with subscribe/close.
	counterMu           sync.Mutex
	publishedTotal      int64
	droppedTotal        int64
	panicsRecovered     int64
	hookPanicsRecovered int64

	// wg tracks the per-subscriber drain goroutines so Close can wait for a
	// clean shutdown.
	wg sync.WaitGroup
}

// NewTopic constructs a topic for payload type T. It returns (*Topic[T],
// error) and never panics: a nil logger or a non-positive buffer degrades to a
// typed error the orchestrator can surface as event=<subsystem>_unavailable.
func NewTopic[T any](log *logger.Logger, opts ...Option) (*Topic[T], error) {
	if log == nil {
		return nil, ErrNilLogger
	}
	o := topicOptions{buffer: DefaultBuffer}
	for _, opt := range opts {
		opt(&o)
	}
	if o.buffer <= 0 {
		return nil, ErrInvalidBuffer
	}
	return &Topic[T]{
		log:    log,
		buffer: o.buffer,
		hook:   newAuditHook[T](log),
	}, nil
}

// Subscribe registers handler as a typed consumer. The handler signature must
// match the topic's type parameter T exactly — the compiler rejects a
// mismatch. Each subscriber gets its own bounded buffer and drain goroutine.
// Subscribing to a closed topic is a no-op.
func (t *Topic[T]) Subscribe(handler func(Event[T])) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	sub := &subscriber[T]{
		handler: handler,
		queue:   make(chan Event[T], t.buffer),
	}
	t.subscribers = append(t.subscribers, sub)
	t.wg.Add(1)
	go t.drain(sub)
}

// Publish enqueues event to every current subscriber and returns whether the
// event was accepted. It is non-blocking: a closed topic returns false
// immediately, and a full subscriber buffer triggers counted drop-oldest
// rather than blocking the producer into deadlock. The hook, if any, runs once
// before fan-out.
func (t *Topic[T]) Publish(event Event[T]) bool {
	// Hold the read lock across the ENTIRE fan-out. Close takes the write lock
	// (mutually exclusive with RLock) before it closes any subscriber queue, so
	// while this Publish holds RLock no queue can be closed underneath it — the
	// "send on closed channel" race is structurally impossible. A Publish that
	// arrives after Close sees closed==true and returns false. enqueue is
	// non-blocking (select-default + drop-oldest), so holding RLock never blocks
	// the producer, and RLock is shared so concurrent publishers still proceed.
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed {
		return false
	}

	// The single internal audit hook (set once by NewTopic) runs before
	// fan-out so the audit line precedes any subscriber side effects. It is
	// nil only on a zero-value Topic that never went through NewTopic.
	if t.hook != nil {
		t.runHook(t.hook, event)
	}

	for _, sub := range t.subscribers {
		t.enqueue(sub, event)
	}

	t.counterMu.Lock()
	t.publishedTotal++
	t.counterMu.Unlock()
	return true
}

// runHook invokes the internal audit hook under recover. The hook only reads
// the envelope and a tolerated zerolog.LogObjectMarshaler payload, but it runs
// on the hot path of every accepted Publish, in the producer goroutine — a
// pathological payload MarshalZerologObject could panic. An unrecovered panic
// here would propagate up through Publish and kill PID 1 — the same
// eBPF-stranding security incident the subscriber path is so careful to
// contain. A recovered hook panic is counted (separately from subscriber
// panics) and audited; it never reaches the producer.
func (t *Topic[T]) runHook(hook auditHook[T], event Event[T]) {
	defer func() {
		if r := recover(); r != nil {
			t.counterMu.Lock()
			t.hookPanicsRecovered++
			t.counterMu.Unlock()
			t.log.Error().
				Str(logFieldEvent, eventHookPanic).
				Interface(logFieldPanic, r).
				Msg(logMsgHookPanic)
		}
	}()
	hook(event)
}

// enqueue performs a non-blocking send into the subscriber's bounded buffer.
// On overflow it drops the OLDEST queued event (newest-wins) and counts the
// eviction, so a slow consumer sheds load from its own queue without ever
// blocking the producer.
func (t *Topic[T]) enqueue(sub *subscriber[T], event Event[T]) {
	for {
		select {
		case sub.queue <- event:
			return
		default:
			// Buffer full: evict the oldest, count it, retry the send. The
			// retry can still lose the race to a concurrent drain pull, in
			// which case the next iteration sends into the freed slot.
			select {
			case <-sub.queue:
				t.counterMu.Lock()
				t.droppedTotal++
				t.counterMu.Unlock()
			default:
			}
		}
	}
}

// drain is the per-subscriber delivery loop. It runs each handler invocation
// under recover so a panicking subscriber is contained to its own event and
// cannot kill PID 1 (which would strand eBPF). The loop itself also recovers
// and exits cleanly when the queue is closed by Close.
func (t *Topic[T]) drain(sub *subscriber[T]) {
	defer t.wg.Done()
	defer func() {
		// The drain loop must never be the thing that takes down the daemon.
		// A panic here (e.g. a pathological queue state) is recovered and
		// audited; the goroutine exits, the rest of the bus lives on.
		if r := recover(); r != nil {
			t.log.Error().
				Str(logFieldEvent, eventDrainPanic).
				Interface(logFieldPanic, r).
				Msg(logMsgDrainPanic)
		}
	}()
	for event := range sub.queue {
		t.deliver(sub.handler, event)
	}
}

// deliver runs one handler under recover. A recovered panic is counted and
// audited, never propagated — one panicking subscriber must not take down the
// daemon and strand eBPF (see the package and resilience docs). A bare
// `go handler(event)` is forbidden precisely because it cannot offer this
// containment.
func (t *Topic[T]) deliver(handler func(Event[T]), event Event[T]) {
	defer func() {
		if r := recover(); r != nil {
			t.counterMu.Lock()
			t.panicsRecovered++
			t.counterMu.Unlock()
			t.log.Error().
				Str(logFieldEvent, eventSubscriberPanic).
				Interface(logFieldPanic, r).
				Msg(logMsgSubscriberPanic)
		}
	}()
	handler(event)
}

// Stats returns a snapshot of the topic's pipe counters.
func (t *Topic[T]) Stats() Stats {
	t.mu.RLock()
	subs := len(t.subscribers)
	t.mu.RUnlock()

	t.counterMu.Lock()
	defer t.counterMu.Unlock()
	return Stats{
		Subscribers:         subs,
		QueueCapacity:       t.buffer,
		PublishedTotal:      t.publishedTotal,
		DroppedTotal:        t.droppedTotal,
		PanicsRecovered:     t.panicsRecovered,
		HookPanicsRecovered: t.hookPanicsRecovered,
	}
}

// Close shuts the topic down: it stops accepting publishes, closes every
// subscriber queue so the drain goroutines exit cleanly, and waits for them.
// It is idempotent — a second Close is a no-op and never panics.
func (t *Topic[T]) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	subs := t.subscribers
	t.subscribers = nil
	for _, sub := range subs {
		close(sub.queue)
	}
	t.mu.Unlock()

	// Wait outside the lock so a draining handler that touches the topic
	// cannot deadlock against Close.
	t.wg.Wait()
	return nil
}
