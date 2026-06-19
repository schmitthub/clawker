package agent

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/controlplane/dockerevents"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/logger"
)

// newAgentTopic constructs a real *pubsub.Topic[AgentEvent] for tests
// and registers cleanup. The pipe is the production transport — tests
// drive real Publish/Subscribe rather than a mock, since the pipe is
// generic (moq cannot mock it) and cheap in-memory.
func newAgentTopic(t *testing.T) *pubsub.Topic[AgentEvent] {
	t.Helper()
	topic, err := pubsub.NewTopic[AgentEvent](logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = topic.Close() })
	return topic
}

// newDockerTopic constructs a real *pubsub.Topic[DockerEvent] for tests
// and registers cleanup.
func newDockerTopic(t *testing.T) *pubsub.Topic[dockerevents.DockerEvent] {
	t.Helper()
	topic, err := pubsub.NewTopic[dockerevents.DockerEvent](logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = topic.Close() })
	return topic
}

// agentRecorder is a thread-safe recording subscriber for AgentEvents.
// It captures every delivered payload so tests can assert the
// discriminated (Type, Action, Reason) the producer published. Delivery
// runs on the topic's own drain goroutine, so the mutex guards the slice
// against the test goroutine reading concurrently.
type agentRecorder struct {
	mu     sync.Mutex
	events []AgentEvent
}

// recordAgent subscribes a recorder to the topic and returns it.
func recordAgent(topic *pubsub.Topic[AgentEvent]) *agentRecorder {
	r := &agentRecorder{}
	topic.Subscribe(func(evt pubsub.Event[AgentEvent]) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, evt.Payload)
	})
	return r
}

// firstWith returns the first recorded event matching (type, action) and
// whether one was found.
func (r *agentRecorder) firstWith(typ EventType, action Action) (AgentEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.Message.Type == typ && e.Message.Action == action {
			return e, true
		}
	}
	return AgentEvent{}, false
}

// withAction returns every recorded event matching (type, action), in
// arrival order.
func (r *agentRecorder) withAction(typ EventType, action Action) []AgentEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []AgentEvent
	for _, e := range r.events {
		if e.Message.Type == typ && e.Message.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// dialerWithTopic builds a *Dialer wired to a fresh agent topic + the
// supplied registry, and returns it alongside a recorder already
// subscribed to the topic. Used by the driveRegister / dispatchAgentEvents
// tests that drive the dialer's publish paths directly.
func dialerWithTopic(t *testing.T, reg Registry) (*Dialer, *agentRecorder) {
	t.Helper()
	topic := newAgentTopic(t)
	rec := recordAgent(topic)
	d := &Dialer{
		log:    logger.Nop(),
		topic:  topic,
		agents: reg,
	}
	return d, rec
}

// fakeSessionStream implements clawkerdv1.ClawkerdService_SessionClient
// (= grpc.BidiStreamingClient[Command, Response]) deterministically.
//
// Send pushes onto a buffered channel so tests can assert which commands
// were dispatched. Recv blocks until either a queued response is
// available, the stream ctx is cancelled (returns ctx.Err), or a preset
// sendErr is returned.
type fakeSessionStream struct {
	ctx    context.Context
	sent   chan *clawkerdv1.Command
	recvCh chan recvFrame

	sendErr atomicError
}

type recvFrame struct {
	resp *clawkerdv1.Response
	err  error
}

// atomicError is a tiny mutex-guarded error cell so tests can flip
// Send-error behavior between calls.
type atomicError struct {
	mu  sync.Mutex
	err error
}

func (a *atomicError) Set(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.err = err
}

func (a *atomicError) Load() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

func newFakeStream(ctx context.Context) *fakeSessionStream {
	return &fakeSessionStream{
		ctx:    ctx,
		sent:   make(chan *clawkerdv1.Command, 8),
		recvCh: make(chan recvFrame, 8),
	}
}

func (f *fakeSessionStream) Send(c *clawkerdv1.Command) error {
	if err := f.sendErr.Load(); err != nil {
		return err
	}
	select {
	case f.sent <- c:
		return nil
	case <-f.ctx.Done():
		return f.ctx.Err()
	}
}

func (f *fakeSessionStream) Recv() (*clawkerdv1.Response, error) {
	select {
	case fr := <-f.recvCh:
		return fr.resp, fr.err
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	}
}

func (f *fakeSessionStream) Header() (metadata.MD, error) { return metadata.MD{}, nil }
func (f *fakeSessionStream) Trailer() metadata.MD         { return metadata.MD{} }
func (f *fakeSessionStream) CloseSend() error             { return nil }
func (f *fakeSessionStream) Context() context.Context     { return f.ctx }
func (f *fakeSessionStream) SendMsg(any) error            { return nil }
func (f *fakeSessionStream) RecvMsg(any) error            { return nil }

// pushRegisterDone enqueues a RegisterDone response with the given
// command_id and ok value.
func (f *fakeSessionStream) pushRegisterDone(commandID string, ok bool, errMsg string) {
	f.recvCh <- recvFrame{resp: &clawkerdv1.Response{
		CommandId: commandID,
		Payload: &clawkerdv1.Response_RegisterDone{RegisterDone: &clawkerdv1.RegisterDone{
			Ok: ok, Error: errMsg,
		}},
	}}
}

// pushUnsolicited enqueues a non-RegisterDone response (e.g. Started) to
// verify driveRegister's Recv loop discards mismatching frames.
func (f *fakeSessionStream) pushUnsolicited() {
	f.recvCh <- recvFrame{resp: &clawkerdv1.Response{
		CommandId: "other",
		Payload:   &clawkerdv1.Response_Started{Started: &clawkerdv1.Started{}},
	}}
}

// happyEstablishResult is the fixture for a Match-classified peer.
func happyEstablishResult(stream *fakeSessionStream, peerCN string, peerThumb [sha256.Size]byte) establishResult {
	return establishResult{
		Stream:   stream,
		Agent:    "dev",
		Project:  "myapp",
		Addr:     "10.0.0.1:7700",
		Attempt:  1,
		Outcome:  outcomeSuccess,
		PeerInfo: peerInfo{PeerAgentFullName: peerCN, PeerThumbprint: peerThumb},
	}
}
