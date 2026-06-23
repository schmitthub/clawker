package agent

import (
	"github.com/google/uuid"

	"github.com/schmitthub/clawker/controlplane/pubsub"
)

// eventSource names this package as the producer on every published
// envelope. The pipe records it on pubsub.Event.Source for audit.
const eventSource = "agent"

// Publish wraps an AgentEvent in the pubsub envelope and offers it to
// the topic. It is the single producer-side seam: dialer, exec, and
// start all Publish through here so the envelope (ID, Timestamp, Source)
// is stamped one way. A nil topic is a no-op — a degraded CP that failed
// to construct the topic must not NPE a producer (the orchestrator logs
// the construction failure as event=<subsystem>_unavailable and wires
// nil). Publish is non-blocking and returns false on a full or closed
// topic; the caller does not block.
func Publish(topic *pubsub.Topic[AgentEvent], ev AgentEvent) bool {
	if topic == nil {
		return false
	}
	return topic.Publish(pubsub.Event[AgentEvent]{
		ID:        uuid.NewString(),
		Timestamp: ev.Message.TimeNano,
		Source:    eventSource,
		Payload:   ev,
	})
}
