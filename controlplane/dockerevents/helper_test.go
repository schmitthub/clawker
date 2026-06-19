package dockerevents

import (
	"testing"

	"github.com/moby/moby/api/types/events"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/logger"
)

const (
	testManagedKey   = "dev.clawker.managed"
	testManagedValue = "true"
)

// recorder is a recording subscriber on a real *pubsub.Topic[DockerEvent].
// It captures every delivered envelope into a buffered channel so tests can
// assert on the published Event[DockerEvent] without reaching into feeder
// internals. Delivery is asynchronous (the topic drains each subscriber on its
// own goroutine), so the channel — not a synchronous slice — is the contract.
type recorder struct {
	topic *pubsub.Topic[DockerEvent]
	ch    chan pubsub.Event[DockerEvent]
}

// newRecorder builds a topic with a recording subscriber wired in. The buffer
// is generous so a test that publishes several envelopes before reading never
// loses one to drop-oldest. The topic is closed via t.Cleanup so the drain
// goroutine exits before the test returns.
func newRecorder(t *testing.T) *recorder {
	t.Helper()
	topic, err := pubsub.NewTopic[DockerEvent](logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = topic.Close() })

	rec := &recorder{
		topic: topic,
		ch:    make(chan pubsub.Event[DockerEvent], 64),
	}
	topic.Subscribe(func(e pubsub.Event[DockerEvent]) {
		rec.ch <- e
	})
	return rec
}

// recvByAction drains the recorder until it observes an envelope whose payload
// Type+Action match, or the test deadline elapses. Unrelated envelopes (the
// test seeds them deliberately) are skipped.
func (r *recorder) recvByAction(t *testing.T, wantType events.Type, wantAction events.Action) pubsub.Event[DockerEvent] {
	t.Helper()
	return recvEventByAction(t, r.ch, wantType, wantAction)
}
