package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// fakeSessionStream implements clawkerdv1.ClawkerdService_SessionClient
// (= grpc.BidiStreamingClient[Command, Response]) deterministically.
//
// Send pushes onto a buffered channel so tests can assert which
// commands were dispatched. Recv blocks until either a queued response
// is available, the stream ctx is cancelled (returns ctx.Err), or a
// preset recvErr is returned.
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

// pushUnsolicited enqueues a non-RegisterDone response (e.g. Started)
// to verify driveRegister's Recv loop discards mismatching frames.
func (f *fakeSessionStream) pushUnsolicited() {
	f.recvCh <- recvFrame{resp: &clawkerdv1.Response{
		CommandId: "other",
		Payload:   &clawkerdv1.Response_Started{Started: &clawkerdv1.Started{}},
	}}
}

// eventCapture subscribes to AgentRegistered + AgentUntrusted BEFORE
// the producer runs, so no event can race past the subscription. Tests
// call .drain(wait) after the producer to collect events in arrival
// order.
type eventCapture struct {
	regSub  overseer.Subscription[AgentRegistered]
	untrSub overseer.Subscription[AgentUntrusted]
}

func subscribeEvents(t *testing.T, bus *overseer.Overseer) *eventCapture {
	t.Helper()
	regSub, ok := overseer.Subscribe[AgentRegistered](bus, "test-reg")
	require.True(t, ok)
	untrSub, ok := overseer.Subscribe[AgentUntrusted](bus, "test-unt")
	require.True(t, ok)
	t.Cleanup(func() {
		regSub.Unsubscribe()
		untrSub.Unsubscribe()
	})
	return &eventCapture{regSub: regSub, untrSub: untrSub}
}

func (ec *eventCapture) drain(wait time.Duration) (registered []AgentRegistered, untrusted []AgentUntrusted) {
	deadline := time.After(wait)
	for {
		select {
		case e := <-ec.regSub.C:
			registered = append(registered, e)
		case e := <-ec.untrSub.C:
			untrusted = append(untrusted, e)
		case <-deadline:
			return
		}
	}
}

// newDriveRegisterDialer wires a Dialer with a started bus and the
// supplied registry. Returns the dialer and an eventCapture that has
// already subscribed to the relevant event types.
func newDriveRegisterDialer(t *testing.T, reg Registry) (*Dialer, *eventCapture) {
	t.Helper()
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	require.NoError(t, bus.Start(t.Context()))
	t.Cleanup(func() { _ = bus.Close() })
	d := &Dialer{
		log:    logger.Nop(),
		bus:    bus,
		agents: reg,
	}
	return d, subscribeEvents(t, bus)
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

// --- driveRegister --------------------------------------------------------

// TestDriveRegister_HappyPath: clawkerd replies with RegisterDone{ok:true};
// dialer publishes AgentRegistered{Ok:true}, no AgentUntrusted.
func TestDriveRegister_HappyPath(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer"))
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return &Entry{
				ContainerID: "abc",
				AgentName:   "dev",
				Project:     "myapp",
				Thumbprint:  thumb,
			}, nil
		},
	}
	d, ec := newDriveRegisterDialer(t, reg)

	streamCtx, cancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	stream.pushRegisterDone("register-abc", true, "")

	res := happyEstablishResult(stream, "clawker.myapp.dev", thumb)
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	registered, untrusted := ec.drain(100 * time.Millisecond)
	require.Len(t, registered, 1)
	assert.True(t, registered[0].Ok)
	assert.Equal(t, "abc", registered[0].ContainerID)
	assert.Empty(t, untrusted)
}

// TestDriveRegister_RegisterDoneFailure_PublishesUntrusted: clawkerd
// replies with RegisterDone{ok:false, error:"..."}; dialer publishes
// AgentRegistered{Ok:false} AND AgentUntrusted{ReasonRegisterFailed}
// so containment subscribers can branch on a single typed event.
func TestDriveRegister_RegisterDoneFailure_PublishesUntrusted(t *testing.T) {
	d, ec := newDriveRegisterDialer(t, &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) { return nil, ErrUnknownAgent },
	})

	streamCtx, cancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	stream.pushRegisterDone("register-abc", false, "Hydra rejected assertion")

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	registered, untrusted := ec.drain(100 * time.Millisecond)
	require.Len(t, registered, 1)
	assert.False(t, registered[0].Ok)
	require.Len(t, untrusted, 1)
	assert.Equal(t, overseer.UntrustedReasonRegisterFailed, untrusted[0].Reason)
}

// TestDriveRegister_DiscardsUnsolicitedFrames: a Started Response with
// the wrong command_id arrives BEFORE the RegisterDone — the inner
// recv loop must skip it and continue waiting. A regression that
// returned the first frame regardless would publish failure here.
func TestDriveRegister_DiscardsUnsolicitedFrames(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer"))
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return &Entry{
				ContainerID: "abc",
				AgentName:   "dev",
				Project:     "myapp",
				Thumbprint:  thumb,
			}, nil
		},
	}
	d, ec := newDriveRegisterDialer(t, reg)

	streamCtx, cancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	stream.pushUnsolicited()
	stream.pushRegisterDone("register-abc", true, "")

	res := happyEstablishResult(stream, "clawker.myapp.dev", thumb)
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	registered, _ := ec.drain(100 * time.Millisecond)
	require.Len(t, registered, 1)
	assert.True(t, registered[0].Ok)
}

// TestDriveRegister_SendFailure_PublishesFailure: stream.Send fails
// (broken transport); driveRegister surfaces failure events and does
// NOT block waiting for a Recv that will never come.
func TestDriveRegister_SendFailure_PublishesFailure(t *testing.T) {
	d, ec := newDriveRegisterDialer(t, &RegistryMock{})

	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := newFakeStream(streamCtx)
	stream.sendErr.Set(errors.New("broken pipe"))

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	registered, untrusted := ec.drain(100 * time.Millisecond)
	require.Len(t, registered, 1)
	assert.False(t, registered[0].Ok)
	assert.Contains(t, registered[0].Reason, "send RegisterRequired")
	require.Len(t, untrusted, 1)
}

// TestDriveRegister_RecvError_PublishesFailure: Recv returns a
// transport error mid-wait → publishRegisterFailure.
func TestDriveRegister_RecvError_PublishesFailure(t *testing.T) {
	d, ec := newDriveRegisterDialer(t, &RegistryMock{})

	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := newFakeStream(streamCtx)
	stream.recvCh <- recvFrame{err: io.ErrUnexpectedEOF}

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	registered, untrusted := ec.drain(100 * time.Millisecond)
	require.Len(t, registered, 1)
	assert.False(t, registered[0].Ok)
	assert.Contains(t, registered[0].Reason, "Recv RegisterDone")
	require.Len(t, untrusted, 1)
}

// TestDriveRegister_ResponseErrorSurfaces pins the
// Response_Error-during-register branch: when clawkerd rejects
// RegisterRequired with a typed Error frame addressed to our
// command_id, driveRegister surfaces the ErrorCode + message in the
// AgentRegistered.Reason and AgentUntrusted.Detail rather than swallowing
// it and timing out. Without this branch, an INVALID_REQUEST rejection
// would manifest as an opaque "RegisterDone timeout" — operators would
// see the symptom but not the cause.
func TestDriveRegister_ResponseErrorSurfaces(t *testing.T) {
	d, ec := newDriveRegisterDialer(t, &RegistryMock{})

	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := newFakeStream(streamCtx)

	commandID := "register-abc"
	stream.recvCh <- recvFrame{resp: &clawkerdv1.Response{
		CommandId: commandID,
		Payload: &clawkerdv1.Response_Error{Error: &clawkerdv1.Error{
			Code:    clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
			Message: "missing client_assertion",
		}},
	}}

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = cancel

	start := time.Now()
	d.driveRegister(t.Context(), "abc", res, logger.Nop())
	require.Less(t, time.Since(start), registerRequiredTimeout/2,
		"a typed Response_Error must short-circuit the wait — not fall through to the timeout")

	registered, untrusted := ec.drain(100 * time.Millisecond)
	require.Len(t, registered, 1)
	require.False(t, registered[0].Ok,
		"register must fail on a typed Response_Error; downstream Reason assertions assume failure")
	assert.Contains(t, registered[0].Reason, "ERROR_CODE_INVALID_REQUEST")
	assert.Contains(t, registered[0].Reason, "missing client_assertion")
	require.Len(t, untrusted, 1)
}

// TestDriveRegister_TimeoutCancelsStream: when no RegisterDone arrives
// within the wait window, driveRegister cancels the stream-scoped
// ctx so the inner Recv goroutine exits BEFORE driveRegister returns.
// Without the cancel, drainStream would race the leftover goroutine
// for stream.Recv() — undefined behavior.
//
// We don't wait the full registerRequiredTimeout (30s); instead we
// pre-cancel the parent ctx to force the timeout path quickly.
func TestDriveRegister_TimeoutCancelsStream(t *testing.T) {
	d, ec := newDriveRegisterDialer(t, &RegistryMock{})

	streamCtx, streamCancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	// Do NOT enqueue a RegisterDone — Recv blocks indefinitely until ctx
	// fires. Pre-cancel parent ctx so waitCtx.Done fires immediately.

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = streamCancel

	parentCtx, parentCancel := context.WithCancel(context.Background())
	parentCancel() // trigger waitCtx.Done immediately

	d.driveRegister(parentCtx, "abc", res, logger.Nop())

	// Stream ctx must have been cancelled by driveRegister so the
	// inner Recv goroutine exits.
	assert.Error(t, streamCtx.Err(), "stream ctx must be cancelled on timeout to unblock inner Recv")

	registered, untrusted := ec.drain(100 * time.Millisecond)
	require.Len(t, registered, 1)
	assert.False(t, registered[0].Ok)
	assert.Contains(t, registered[0].Reason, "RegisterDone timeout")
	require.Len(t, untrusted, 1)
}

// TestDriveRegister_RegistryLookupError_DistinguishedFromMissingRow:
// a sqlite-side error after RegisterDone reports differently from
// "no row" so containment subscribers can tell I/O failures apart
// from "agent lied about Register success".
func TestDriveRegister_RegistryLookupError_DistinguishedFromMissingRow(t *testing.T) {
	d, ec := newDriveRegisterDialer(t, &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return nil, errors.New("disk i/o error")
		},
	})

	streamCtx, cancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	stream.pushRegisterDone("register-abc", true, "")

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	registered, untrusted := ec.drain(100 * time.Millisecond)
	require.Len(t, registered, 1)
	assert.False(t, registered[0].Ok)
	assert.Contains(t, registered[0].Reason, "registry lookup error")
	require.Len(t, untrusted, 1)
}

// TestDriveRegister_MissingRowAfterRegisterDone: success-reported but
// row absent — clawkerd-side regression. Distinguished from I/O
// error in the published reason.
func TestDriveRegister_MissingRowAfterRegisterDone(t *testing.T) {
	d, ec := newDriveRegisterDialer(t, &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) { return nil, ErrUnknownAgent },
	})

	streamCtx, cancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	stream.pushRegisterDone("register-abc", true, "")

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	registered, _ := ec.drain(100 * time.Millisecond)
	require.Len(t, registered, 1)
	assert.False(t, registered[0].Ok)
	assert.Contains(t, registered[0].Reason, "registry row missing")
}

// --- dispatchAgentEvents (load-bearing asymmetric-trust tests) -----------

// TestDispatchAgentEvents_Match_PublishesNoExtraEvent: when the peer
// matches a registry row, dispatchAgentEvents publishes nothing
// extra (SessionConnected already populated worldview).
func TestDispatchAgentEvents_Match_PublishesNoExtraEvent(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer"))
	expectedCN := auth.CanonicalAgentCN(auth.MustProjectSlug("myapp"), auth.MustAgentName("dev"))
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return &Entry{
				ContainerID: "abc",
				AgentName:   "dev",
				Project:     "myapp",
				Thumbprint:  thumb,
			}, nil
		},
	}
	d, ec := newDriveRegisterDialer(t, reg)

	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := newFakeStream(streamCtx)
	res := happyEstablishResult(stream, expectedCN, thumb)
	res.StreamCancel = cancel

	d.dispatchAgentEvents(t.Context(), "abc", res, logger.Nop())

	registered, untrusted := ec.drain(50 * time.Millisecond)
	assert.Empty(t, registered, "Match must NOT publish AgentRegistered")
	assert.Empty(t, untrusted, "Match must NOT publish AgentUntrusted")
}

// TestDispatchAgentEvents_OutcomesPinned pins the typed Reason for
// each non-Match outcome — a regression that swapped CNMismatch/
// ThumbprintMismatch in the switch would silently mis-classify
// containment policy.
func TestDispatchAgentEvents_OutcomesPinned(t *testing.T) {
	cases := []struct {
		name       string
		reg        *RegistryMock
		peer       peerInfo
		wantReason overseer.UntrustedReason
	}{
		{
			name: "ThumbprintMismatch",
			reg: &RegistryMock{
				LookupByContainerIDFunc: func(string) (*Entry, error) {
					return &Entry{
						ContainerID: "abc",
						AgentName:   "dev",
						Project:     "myapp",
						Thumbprint:  sha256.Sum256([]byte("registered-cert")),
					}, nil
				},
			},
			peer:       peerInfo{PeerAgentFullName: "clawker.myapp.dev", PeerThumbprint: sha256.Sum256([]byte("live-cert"))},
			wantReason: overseer.UntrustedReasonThumbprintMismatch,
		},
		{
			name: "CNMismatch",
			reg: &RegistryMock{
				LookupByContainerIDFunc: func(string) (*Entry, error) {
					thumb := sha256.Sum256([]byte("c"))
					return &Entry{
						ContainerID: "abc",
						AgentName:   "dev",
						Project:     "actual",
						Thumbprint:  thumb,
					}, nil
				},
			},
			peer:       peerInfo{PeerAgentFullName: "clawker.different.dev", PeerThumbprint: sha256.Sum256([]byte("c"))},
			wantReason: overseer.UntrustedReasonCNMismatch,
		},
		{
			name: "LookupError",
			reg: &RegistryMock{
				LookupByContainerIDFunc: func(string) (*Entry, error) {
					return nil, errors.New("disk i/o failure")
				},
			},
			peer:       peerInfo{PeerAgentFullName: "clawker.x.y", PeerThumbprint: sha256.Sum256([]byte("p"))},
			wantReason: overseer.UntrustedReasonCertInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, ec := newDriveRegisterDialer(t, tc.reg)
			streamCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stream := newFakeStream(streamCtx)
			res := establishResult{
				Stream:       stream,
				StreamCancel: cancel,
				Agent:        "dev",
				Project:      "myapp",
				PeerInfo:     tc.peer,
			}

			d.dispatchAgentEvents(t.Context(), "abc", res, logger.Nop())

			_, untrusted := ec.drain(100 * time.Millisecond)
			require.Len(t, untrusted, 1)
			assert.Equal(t, tc.wantReason, untrusted[0].Reason)
		})
	}
}

// TestDispatchAgentEvents_AsymmetricTrust_StreamStaysOpen pins the
// load-bearing invariant of this branch: a ThumbprintMismatch
// (untrusted classification) must NOT cancel or close the Session
// stream — CP must remain reachable to dispatch containment commands
// against the untrusted agent. We drive dispatchAgentEvents with a
// mismatch and then verify the stream is still usable for further
// commands (CloseSend not invoked, ctx not cancelled).
func TestDispatchAgentEvents_AsymmetricTrust_StreamStaysOpen(t *testing.T) {
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return &Entry{
				ContainerID: "abc",
				AgentName:   "dev",
				Project:     "myapp",
				Thumbprint:  sha256.Sum256([]byte("registered")),
			}, nil
		},
	}
	d, ec := newDriveRegisterDialer(t, reg)
	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := newFakeStream(streamCtx)

	res := establishResult{
		Stream:       stream,
		StreamCancel: cancel,
		Agent:        "dev",
		Project:      "myapp",
		PeerInfo: peerInfo{
			PeerAgentFullName: "clawker.myapp.dev",
			PeerThumbprint:    sha256.Sum256([]byte("live")),
		},
	}

	d.dispatchAgentEvents(t.Context(), "abc", res, logger.Nop())

	// Stream ctx must still be live — no cancellation on cert grounds.
	assert.NoError(t, streamCtx.Err(), "stream must stay open after ThumbprintMismatch (asymmetric trust)")

	_, untrusted := ec.drain(100 * time.Millisecond)
	require.Len(t, untrusted, 1)
	assert.Equal(t, overseer.UntrustedReasonThumbprintMismatch, untrusted[0].Reason)
}
