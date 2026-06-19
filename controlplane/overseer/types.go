package overseer

import (
	"github.com/rs/zerolog"
)

type Event[T any] struct {
	Source    string
	ID        string
	Timestamp int64
	Payload   T
	zerolog.LogObjectMarshaler
}

// Handler defines a strongly-typed consumer
type Handler[T any] interface {
	Handle(event Event[T]) error
}

// Stats is a snapshot of bus counters at read time. .
type Stats struct {
	Subscribers     int
	PublishedTotal  uint64
	DroppedTotal    uint64
	QueueDepth      int
	QueueCapacity   int
	ContainersKnown int
	SessionsKnown   int
}
