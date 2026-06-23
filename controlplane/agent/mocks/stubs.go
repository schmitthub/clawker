package mocks

import (
	"sync"
	"testing"

	"github.com/schmitthub/clawker/controlplane/agent"
	"github.com/schmitthub/clawker/controlplane/dockerevents"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/require"
)

// NewAgentTopic constructs a real *pubsub.Topic[AgentEvent] for tests
// and registers cleanup. The pipe is the production transport — tests
// drive real Publish/Subscribe rather than a mock, since the pipe is
// generic (moq cannot mock it) and cheap in-memory.
func NewAgentTopic(t *testing.T) *pubsub.Topic[agent.AgentEvent] {
	t.Helper()
	topic, err := pubsub.NewTopic[agent.AgentEvent](logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = topic.Close() })
	return topic
}

// NewDockerTopic constructs a real *pubsub.Topic[DockerEvent] for tests
// and registers cleanup.
func NewDockerTopic(t *testing.T) *pubsub.Topic[dockerevents.DockerEvent] {
	t.Helper()
	topic, err := pubsub.NewTopic[dockerevents.DockerEvent](logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = topic.Close() })
	return topic
}

// AgentRecorder is a thread-safe recording subscriber for AgentEvents.
// It captures every delivered payload so tests can assert the
// discriminated (Type, Action, Reason) the producer published. Delivery
// runs on the topic's own drain goroutine, so the mutex guards the slice
// against the test goroutine reading concurrently.
type AgentRecorder struct {
	mu     sync.Mutex
	events []agent.AgentEvent
}

// RecordAgent subscribes a recorder to the topic and returns it.
func RecordAgent(topic *pubsub.Topic[agent.AgentEvent]) *AgentRecorder {
	r := &AgentRecorder{}
	topic.Subscribe(func(evt pubsub.Event[agent.AgentEvent]) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, evt.Payload)
	})
	return r
}

// FirstWith returns the first recorded event matching (type, action) and
// whether one was found.
func (r *AgentRecorder) FirstWith(typ agent.EventType, action agent.Action) (agent.AgentEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.Message.Type == typ && e.Message.Action == action {
			return e, true
		}
	}
	return agent.AgentEvent{}, false
}

// WithAction returns every recorded event matching (type, action), in
// arrival order.
func (r *AgentRecorder) WithAction(typ agent.EventType, action agent.Action) []agent.AgentEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []agent.AgentEvent
	for _, e := range r.events {
		if e.Message.Type == typ && e.Message.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// DialerWithTopic builds a *Dialer wired to a fresh agent topic + the
// supplied registry, and returns it alongside a recorder already
// subscribed to the topic. Used by the driveRegister / dispatchAgentEvents
// tests that drive the dialer's publish paths directly.
func DialerWithTopic(t *testing.T, reg agent.Registry) (*agent.Dialer, *AgentRecorder) {
	t.Helper()
	topic := NewAgentTopic(t)
	rec := RecordAgent(topic)
	d := &agent.Dialer{
		Log:    logger.Nop(),
		Topic:  topic,
		Agents: reg,
	}
	return d, rec
}
