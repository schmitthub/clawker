package mocks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	v1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// recvFrame is one queued Recv() outcome.
type recvFrame struct {
	resp *v1.Response
	err  error
}

// FakeSessionStream is an in-memory fake for ClawkerdService Session streams.
// It is safe for concurrent Send/Recv use in tests.
type FakeSessionStream struct {
	ctx context.Context

	mu           sync.Mutex
	sent         []*v1.Command
	sendErr      error
	closeSendErr error
	header       metadata.MD
	headerErr    error
	trailer      metadata.MD

	// closed is set by CloseSend (the client half-close). Once set, Send
	// returns errSendAfterCloseSend and Push* are discarded — modelling a
	// real half-closed gRPC client stream and ensuring a feeder that
	// outlives the Executor can never send on a closed channel and panic.
	// Guarded by mu (which also serializes the channel closes below against
	// in-flight Send/Push), so CloseSend is idempotent.
	closed bool
	recvCh chan recvFrame

	// sentCh mirrors every Command passed to Send so a test-side server
	// simulator can consume the client's outbound stream reactively (as a
	// real gRPC server reads frames as the client writes them) instead of
	// snapshotting after the fact. CloseSend closes it, ending the half.
	sentCh chan *v1.Command
}

// Buffer sizes for the fake's internal channels. Sized for the command/
// response volume unit tests drive (the longest static plan is well under
// fakeSentBuffer); a test that wires no consumer simply fills the buffer and
// the ctx.Done escape in Send/pushFrame prevents a wedge. Promote to a
// constructor option if a caller ever needs to drive larger volumes.
const (
	fakeRecvBuffer = 16
	fakeSentBuffer = 64
)

// errSendAfterCloseSend models gRPC's refusal to Send on a client stream
// whose send direction was already half-closed via CloseSend.
var errSendAfterCloseSend = errors.New("fake session stream: Send after CloseSend")

// Ensure FakeSessionStream satisfies the generated session stream alias.
var _ v1.ClawkerdService_SessionClient = (*FakeSessionStream)(nil)

// NewFakeSessionStream returns a reusable in-memory session stream fake.
func NewFakeSessionStream(ctx context.Context) *FakeSessionStream {
	if ctx == nil {
		ctx = context.Background()
	}
	return &FakeSessionStream{
		ctx:    ctx,
		recvCh: make(chan recvFrame, fakeRecvBuffer),
		sentCh: make(chan *v1.Command, fakeSentBuffer),
	}
}

// Sent returns the channel of Commands the client has sent, in send order.
// A test-side server simulator ranges over it to reply to each Command as it
// arrives; the channel is closed by CloseSend (the client half-close).
func (f *FakeSessionStream) Sent() <-chan *v1.Command {
	return f.sentCh
}

// FeedResponses runs the server simulator: a goroutine that consumes the
// client's outbound command stream and, for each Command, pushes the frames
// respond(idx, cmd) returns (idx is the 0-based position over every command
// sent; return nil to leave a command unanswered). It is protocol-agnostic —
// it feeds any command type and makes no assumptions about which commands
// expect a reply. Because it ranges over Sent(), it reacts to each command as
// it is sent (the client sends command N+1 only after N's terminal response
// lands) rather than snapshotting after the fact. It exits when CloseSend
// closes the stream. Returns a channel closed when the goroutine exits, so a
// test can await teardown after CloseSend.
func (f *FakeSessionStream) FeedResponses(respond func(idx int, cmd *v1.Command) []*v1.Response) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		idx := 0
		for cmd := range f.Sent() {
			for _, resp := range respond(idx, cmd) {
				f.PushResponse(resp)
			}
			idx++
		}
	}()
	return done
}

// FeedSteps is the Session-protocol convenience over FeedResponses for driving
// the Executor's plans. It skips CloseStdin frames — clawkerd answers those as
// part of the preceding command, reusing its command_id, so a standalone
// response would be a duplicate — and numbers the remaining commands as
// stepIdx for the responder.
func (f *FakeSessionStream) FeedSteps(respond func(stepIdx int, cmd *v1.Command) []*v1.Response) <-chan struct{} {
	stepIdx := 0
	return f.FeedResponses(func(_ int, cmd *v1.Command) []*v1.Response {
		if _, isClose := cmd.GetPayload().(*v1.Command_CloseStdin); isClose {
			return nil
		}
		out := respond(stepIdx, cmd)
		stepIdx++
		return out
	})
}

// FeedDone answers every step Command with a terminal Done{0}.
func (f *FakeSessionStream) FeedDone() <-chan struct{} {
	return f.FeedSteps(func(_ int, cmd *v1.Command) []*v1.Response {
		return []*v1.Response{DoneResp(cmd.GetCommandId(), 0)}
	})
}

// DoneResp builds a terminal Done response for commandID with the given exit
// code.
func DoneResp(commandID string, exitCode int32) *v1.Response {
	return &v1.Response{
		CommandId: commandID,
		Payload:   &v1.Response_Done{Done: &v1.Done{FinalExitCode: exitCode}},
	}
}

// PushResponse queues one Response for the next Recv call.
func (f *FakeSessionStream) PushResponse(resp *v1.Response) {
	f.pushFrame(recvFrame{resp: resp})
}

// PushRecvError queues one error for the next Recv call.
func (f *FakeSessionStream) PushRecvError(err error) {
	f.pushFrame(recvFrame{err: err})
}

// pushFrame enqueues one Recv outcome. After CloseSend the frame is
// discarded (the stream is torn down; a real server's late frame would not
// reach the client) rather than panicking on a closed channel. Holding mu
// across the send serializes it against CloseSend's close(recvCh); ctx.Done
// is the escape valve if a test fills the buffer with no Recv consumer.
func (f *FakeSessionStream) pushFrame(frame recvFrame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	select {
	case f.recvCh <- frame:
	case <-f.ctx.Done():
	}
}

// PushRegisterDone queues a RegisterDone response for commandID.
func (f *FakeSessionStream) PushRegisterDone(commandID string, ok bool, errMsg string) {
	f.PushResponse(&v1.Response{
		CommandId: commandID,
		Payload: &v1.Response_RegisterDone{RegisterDone: &v1.RegisterDone{
			Ok:    ok,
			Error: errMsg,
		}},
	})
}

// PushUnsolicited queues a Started response with a mismatched command id.
func (f *FakeSessionStream) PushUnsolicited() {
	f.PushResponse(&v1.Response{
		CommandId: "unsolicited",
		Payload:   &v1.Response_Started{Started: &v1.Started{}},
	})
}

// SetSendError configures the error returned by Send.
func (f *FakeSessionStream) SetSendError(err error) {
	f.mu.Lock()
	f.sendErr = err
	f.mu.Unlock()
}

// SetCloseSendError configures the error returned by CloseSend.
func (f *FakeSessionStream) SetCloseSendError(err error) {
	f.mu.Lock()
	f.closeSendErr = err
	f.mu.Unlock()
}

// SetHeader configures Header() return values.
func (f *FakeSessionStream) SetHeader(md metadata.MD, err error) {
	f.mu.Lock()
	f.header = md
	f.headerErr = err
	f.mu.Unlock()
}

// SetTrailer configures Trailer() return values.
func (f *FakeSessionStream) SetTrailer(md metadata.MD) {
	f.mu.Lock()
	f.trailer = md
	f.mu.Unlock()
}

// SentCommands returns a copy of commands passed to Send/SendMsg.
func (f *FakeSessionStream) SentCommands() []*v1.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*v1.Command, len(f.sent))
	copy(out, f.sent)
	return out
}

func (f *FakeSessionStream) Header() (metadata.MD, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.header, f.headerErr
}

func (f *FakeSessionStream) Trailer() metadata.MD {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.trailer
}

func (f *FakeSessionStream) CloseSend() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closeSendErr != nil {
		return f.closeSendErr
	}
	if !f.closed {
		f.closed = true
		close(f.recvCh)
		close(f.sentCh)
	}
	return nil
}

func (f *FakeSessionStream) Context() context.Context {
	return f.ctx
}

func (f *FakeSessionStream) Send(cmd *v1.Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	if f.closed {
		return errSendAfterCloseSend
	}
	f.sent = append(f.sent, cmd)
	// Mirror to the server-simulator channel under mu so CloseSend can never
	// close sentCh mid-send (panic); ctx.Done is the escape valve if a test
	// wires no Sent() consumer and fills the buffer.
	select {
	case f.sentCh <- cmd:
	case <-f.ctx.Done():
	}
	return nil
}

func (f *FakeSessionStream) Recv() (*v1.Response, error) {
	select {
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	case frame, ok := <-f.recvCh:
		if !ok {
			return nil, io.EOF
		}
		if frame.err != nil {
			return nil, frame.err
		}
		if frame.resp == nil {
			return nil, errors.New("fake session stream: nil queued response")
		}
		return frame.resp, nil
	}
}

func (f *FakeSessionStream) SendMsg(m any) error {
	cmd, ok := m.(*v1.Command)
	if !ok {
		return fmt.Errorf("fake session stream: SendMsg expects *v1.Command, got %T", m)
	}
	return f.Send(cmd)
}

func (f *FakeSessionStream) RecvMsg(m any) error {
	resp, err := f.Recv()
	if err != nil {
		return err
	}
	out, ok := m.(*v1.Response)
	if !ok {
		return fmt.Errorf("fake session stream: RecvMsg expects *v1.Response, got %T", m)
	}
	out.Reset()
	proto.Merge(out, resp)
	return nil
}

// NewServiceClient returns a client mock whose Session call
// returns stream and sessionErr.
func NewServiceClient(stream v1.ClawkerdService_SessionClient, sessionErr error) *ClawkerdServiceClientMock {
	return &ClawkerdServiceClientMock{
		SessionFunc: func(_ context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[v1.Command, v1.Response], error) {
			return stream, sessionErr
		},
	}
}

// NewServiceClientWithStream returns a client mock wired to
// a fresh fake stream for convenience in tests.
func NewServiceClientWithStream(ctx context.Context) (*ClawkerdServiceClientMock, *FakeSessionStream) {
	stream := NewFakeSessionStream(ctx)
	return NewServiceClient(stream, nil), stream
}
