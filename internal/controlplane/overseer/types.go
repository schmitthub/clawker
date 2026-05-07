// Package overseer is the typed event bus + in-memory worldview state
// for the clawker control plane. The pantheon framing puts CP in the
// Sauron seat: it observes, reconciles, holds the realm's current
// truth. Overseer is that seat.
//
// Producers (dockerevents, agent, future eBPF) publish typed events
// via Publish[T]. Consumers subscribe to a concrete event type via
// Subscribe[T] and receive a typed channel. The bus also maintains an
// in-memory State projection (containers, agent sessions) populated
// from events whose types implement the unexported state-applier
// interface — Snapshot returns a deep copy.
//
// The package is deliberately consumer-agnostic at the bus layer:
// event types live in producer packages (e.g., dockerevents.DockerEvent),
// which import overseer for State definitions when their event
// implements ApplyTo. Overseer never imports any producer package — it
// is a leaf, like the informer it replaces.
package overseer

import (
	"time"

	"github.com/rs/zerolog"

	"github.com/schmitthub/clawker/internal/logger"
)

// Event is the contract every published value implements. EventName
// names the event for log lines; OccurredAt provides the canonical
// timestamp consumed by State.LastUpdatedAt and any time-ordered
// reasoning a consumer wants to do. zerolog.LogObjectMarshaler is the
// per-type log payload — NewLoggerHook EmbedObjects every event so
// type-specific fields (container_id, agent, project, address, ...)
// land in log lines without reflection or producer-specific hook
// wiring.
type Event interface {
	EventName() string
	OccurredAt() time.Time
	zerolog.LogObjectMarshaler
}

// Options configures an Overseer. Zero values are valid.
type Options struct {
	// PublishBufferSize bounds the bus's input queue. Defaults to 2048,
	// matching the informer's WriteQueueSize. A full queue blocks
	// Publish callers (back-pressure to producers).
	PublishBufferSize int
	// SubscriberBuffer bounds each subscriber's typed channel. Defaults
	// to 256, matching the informer's SubscriberBuffer. Full buffer →
	// drop-oldest, increment DroppedTotal. Overseer never blocks on a
	// slow consumer.
	SubscriberBuffer int
	// Logger receives audit lines (every dropped delivery, every close).
	// Nil defaults to logger.Nop().
	Logger *logger.Logger
	// PublishHook runs synchronously on the bus loop after every
	// successfully-applied event, before subscriber fan-out. Intended for
	// unconditional cross-cutting concerns — primarily structured
	// logging, where a single hook spares each producer from manually
	// pairing log lines with Publish calls. Panics are recovered to
	// Logger; a panicking hook does NOT block subsequent events. Nil =
	// no-op. Use NewLoggerHook for the canonical "log every event"
	// implementation.
	PublishHook func(Event)
	// Now is an injectable clock for deterministic tests. Defaults to
	// time.Now. Used to stamp LastUpdatedAt when no event-supplied time
	// is more recent.
	Now func() time.Time
}

const (
	defaultPublishBuffer    = 2048
	defaultSubscriberBuffer = 256
)

// Stats is a snapshot of bus counters at read time. Intended for the
// CP's stats-heartbeat log line and test assertions; not a substitute
// for a real metrics pipeline.
type Stats struct {
	Subscribers     int
	PublishedTotal  uint64
	DroppedTotal    uint64
	QueueDepth      int
	QueueCapacity   int
	ContainersKnown int
	SessionsKnown   int
}
