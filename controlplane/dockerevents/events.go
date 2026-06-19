// Package dockerevents subscribes to moby's container/network event
// stream and republishes it on a pubsub.Topic[DockerEvent] as a single
// typed envelope, DockerEvent, wrapping moby's events.Message verbatim.
//
// One topic. Subscribers receive every container + network event the
// daemon emits and filter on ev.Type + ev.Action. There is NO Go
// type per moby action — the action vocabulary lives where it
// belongs (moby's events.Action) and is filtered at the consumer
// boundary, not invented in our type system.
//
// Drift safety: DockerEvent is a thin wrapper over the embedded
// events.Message carrying only MarshalZerologObject. New moby actions
// render verbatim through MarshalZerologObject without code change. New
// event types (volume, image, plugin) likewise pass through if the
// feeder's stream filter is widened. Per-event identity (the unique
// Event.ID and the envelope Timestamp/Source) lives on the pubsub
// envelope, not on the payload.
package dockerevents

import (
	"github.com/moby/moby/api/types/events"
	"github.com/rs/zerolog"
)

// DockerEvent is the pub/sub payload for any docker daemon event the
// feeder republishes. It embeds events.Message verbatim and exists as a
// thin wrapper for one reason: events.Message is a third-party moby type
// we cannot attach methods to, so DockerEvent is the minimal carrier for
// MarshalZerologObject — the marshaler the single generic audit hook in
// pubsub embeds without reflection. There is no parallel schema and no
// engine-key vs label flattening at the producer side. Subscribers reach
// through to Type, Action, Actor.ID, Actor.Attributes, TimeNano directly.
type DockerEvent struct {
	events.Message
}

// MarshalZerologObject dumps the moby Message as structured log
// fields. Actor.Attributes is folded out as one `actor_attr.<k>`
// field per entry so operators can label-filter on individual
// attribute keys at the log index without a JSON parser. Type and
// Action are flat fields so subscribers' index queries pin on them
// directly.
func (e DockerEvent) MarshalZerologObject(z *zerolog.Event) {
	z.Str("type", string(e.Type)).
		Str("action", string(e.Action)).
		Str("actor_id", e.Actor.ID).
		Str("scope", e.Scope)
	for k, v := range e.Actor.Attributes {
		z.Str("actor_attr."+k, v)
	}
}
