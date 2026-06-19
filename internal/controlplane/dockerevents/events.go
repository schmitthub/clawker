// Package dockerevents subscribes to moby's container/network event
// stream and republishes it on the Overseer bus as a single typed
// envelope, DockerEvent, wrapping moby's events.Message verbatim.
//
// One topic. Subscribers receive every container + network event the
// daemon emits and filter on ev.Type + ev.Action. There is NO Go
// type per moby action — the action vocabulary lives where it
// belongs (moby's events.Action) and is filtered at the consumer
// boundary, not invented in our type system.
//
// Drift safety: the methods on DockerEvent are pure projections of
// the embedded events.Message. New moby actions render verbatim
// through EventName / MarshalZerologObject without code change. New
// event types (volume, image, plugin) likewise pass through if the
// feeder's stream filter is widened.
package dockerevents

import (
	"fmt"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/rs/zerolog"
)

// DockerEvent is the bus envelope for any docker daemon event the
// feeder republishes. Embeds events.Message verbatim. Implements
// overseer.Event via three pure projections of the embedded fields —
// no parallel schema, no engine-key vs label flattening at the
// producer side. Subscribers reach through to Type, Action,
// Actor.ID, Actor.Attributes, TimeNano directly.
type DockerEvent struct {
	events.Message
}

// EventName renders the canonical "docker.<type>.<action>" string
// (e.g. "docker.container.start", "docker.network.connect"). Used
// by NewLoggerHook for the log-line message and the `event` field;
// also used by structured-log filters at the log index.
func (e DockerEvent) EventName() string {
	return fmt.Sprintf("docker.%s.%s", e.Type, e.Action)
}

// OccurredAt converts moby's nanosecond timestamp to time.Time. Uses
// TimeNano for sub-second precision; e.Time is seconds-only.
func (e DockerEvent) OccurredAt() time.Time {
	return time.Unix(0, e.TimeNano)
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

// copyStringMap returns nil for nil, otherwise a fresh map with the
// same key/value pairs. Keeps Snapshot deep-copy honest.
func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// stripEngineKeys returns a copy of attrs with the listed engine-set
// keys removed. Engine-set keys live alongside user labels in
// Actor.Attributes; State.ContainerView.Labels should hold only
// true labels.
func stripEngineKeys(attrs map[string]string, keys ...string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	skip := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		skip[k] = struct{}{}
	}
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		if _, drop := skip[k]; drop {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
