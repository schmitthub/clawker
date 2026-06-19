package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/logger"
)

// registeredEvents returns every RegistryEventType/ActionRegistered
// AgentEvent the recorder captured, in arrival order.
func registeredEvents(rec *agentRecorder) []AgentEvent {
	return rec.withAction(RegistryEventType, ActionRegistered)
}

// untrustedEvents returns every RegistryEventType/ActionUntrusted
// AgentEvent the recorder captured, in arrival order.
func untrustedEvents(rec *agentRecorder) []AgentEvent {
	return rec.withAction(RegistryEventType, ActionUntrusted)
}

// --- driveRegister --------------------------------------------------------

// TestDriveRegister_HappyPath: clawkerd replies with RegisterDone{ok:true};
// dialer publishes a registered AgentEvent with RegisterOk=true and no
// untrusted event.
func TestDriveRegister_HappyPath(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer"))
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return &Entry{
				ContainerID: "abc",
				AgentName:   auth.MustAgentName("dev"),
				Project:     auth.MustProjectSlug("myapp"),
				Thumbprint:  thumb,
			}, nil
		},
	}
	d, rec := dialerWithTopic(t, reg)

	streamCtx, cancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	stream.pushRegisterDone("register-abc", true, "")

	res := happyEstablishResult(stream, "clawker.myapp.dev", thumb)
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	require.Eventually(t, func() bool { return len(registeredEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
	registered := registeredEvents(rec)
	assert.True(t, registered[0].Message.RegisterOk)
	assert.Equal(t, "abc", registered[0].Agent.ContainerID)
	assert.Empty(t, untrustedEvents(rec))
}

// TestDriveRegister_RegisterDoneFailure_PublishesUntrusted: clawkerd
// replies with RegisterDone{ok:false, error:"..."}; dialer publishes a
// registered AgentEvent with RegisterOk=false AND an untrusted event with
// ReasonRegisterFailed so containment subscribers can branch on the typed
// reason.
func TestDriveRegister_RegisterDoneFailure_PublishesUntrusted(t *testing.T) {
	d, rec := dialerWithTopic(t, &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) { return nil, ErrUnknownAgent },
	})

	streamCtx, cancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	stream.pushRegisterDone("register-abc", false, "Hydra rejected assertion")

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	require.Eventually(t, func() bool { return len(untrustedEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
	registered := registeredEvents(rec)
	require.Len(t, registered, 1)
	assert.False(t, registered[0].Message.RegisterOk)
	untrusted := untrustedEvents(rec)
	require.Len(t, untrusted, 1)
	assert.Equal(t, ReasonRegisterFailed, untrusted[0].Message.Reason)
}

// TestDriveRegister_DiscardsUnsolicitedFrames: a Started Response with the
// wrong command_id arrives BEFORE the RegisterDone — the inner recv loop
// must skip it and continue waiting. A regression that returned the first
// frame regardless would publish failure here.
func TestDriveRegister_DiscardsUnsolicitedFrames(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer"))
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return &Entry{
				ContainerID: "abc",
				AgentName:   auth.MustAgentName("dev"),
				Project:     auth.MustProjectSlug("myapp"),
				Thumbprint:  thumb,
			}, nil
		},
	}
	d, rec := dialerWithTopic(t, reg)

	streamCtx, cancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	stream.pushUnsolicited()
	stream.pushRegisterDone("register-abc", true, "")

	res := happyEstablishResult(stream, "clawker.myapp.dev", thumb)
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	require.Eventually(t, func() bool { return len(registeredEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
	assert.True(t, registeredEvents(rec)[0].Message.RegisterOk)
}

// TestDriveRegister_SendFailure_PublishesFailure: stream.Send fails (broken
// transport); driveRegister surfaces failure events and does NOT block
// waiting for a Recv that will never come.
func TestDriveRegister_SendFailure_PublishesFailure(t *testing.T) {
	d, rec := dialerWithTopic(t, &RegistryMock{})

	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := newFakeStream(streamCtx)
	stream.sendErr.Set(errors.New("broken pipe"))

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	require.Eventually(t, func() bool { return len(untrustedEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
	registered := registeredEvents(rec)
	require.Len(t, registered, 1)
	assert.False(t, registered[0].Message.RegisterOk)
	assert.Contains(t, registered[0].Message.Detail, "send RegisterRequired")
}

// TestDriveRegister_RecvError_PublishesFailure: Recv returns a transport
// error mid-wait → publishRegisterFailure.
func TestDriveRegister_RecvError_PublishesFailure(t *testing.T) {
	d, rec := dialerWithTopic(t, &RegistryMock{})

	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := newFakeStream(streamCtx)
	stream.recvCh <- recvFrame{err: io.ErrUnexpectedEOF}

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	require.Eventually(t, func() bool { return len(untrustedEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
	registered := registeredEvents(rec)
	require.Len(t, registered, 1)
	assert.False(t, registered[0].Message.RegisterOk)
	assert.Contains(t, registered[0].Message.Detail, "Recv RegisterDone")
}

// TestDriveRegister_ResponseErrorSurfaces pins the
// Response_Error-during-register branch: when clawkerd rejects
// RegisterRequired with a typed Error frame addressed to our command_id,
// driveRegister surfaces the ErrorCode + message in the registered
// event's Detail rather than swallowing it and timing out.
func TestDriveRegister_ResponseErrorSurfaces(t *testing.T) {
	d, rec := dialerWithTopic(t, &RegistryMock{})

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

	require.Eventually(t, func() bool { return len(registeredEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
	registered := registeredEvents(rec)
	require.False(t, registered[0].Message.RegisterOk,
		"register must fail on a typed Response_Error; downstream Detail assertions assume failure")
	assert.Contains(t, registered[0].Message.Detail, "ERROR_CODE_INVALID_REQUEST")
	assert.Contains(t, registered[0].Message.Detail, "missing client_assertion")
	require.Len(t, untrustedEvents(rec), 1)
}

// TestDriveRegister_TimeoutCancelsStream: when no RegisterDone arrives
// within the wait window, driveRegister cancels the stream-scoped ctx so
// the inner Recv goroutine exits BEFORE driveRegister returns. Without the
// cancel, drainStream would race the leftover goroutine for stream.Recv().
//
// We don't wait the full registerRequiredTimeout (30s); instead we
// pre-cancel the parent ctx to force the timeout path quickly.
func TestDriveRegister_TimeoutCancelsStream(t *testing.T) {
	d, rec := dialerWithTopic(t, &RegistryMock{})

	streamCtx, streamCancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	// Do NOT enqueue a RegisterDone — Recv blocks indefinitely until ctx
	// fires. Pre-cancel parent ctx so waitCtx.Done fires immediately.

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = streamCancel

	parentCtx, parentCancel := context.WithCancel(context.Background())
	parentCancel() // trigger waitCtx.Done immediately

	d.driveRegister(parentCtx, "abc", res, logger.Nop())

	// Stream ctx must have been cancelled by driveRegister so the inner
	// Recv goroutine exits.
	assert.Error(t, streamCtx.Err(), "stream ctx must be cancelled on timeout to unblock inner Recv")

	require.Eventually(t, func() bool { return len(registeredEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
	registered := registeredEvents(rec)
	assert.False(t, registered[0].Message.RegisterOk)
	assert.Contains(t, registered[0].Message.Detail, "RegisterDone timeout")
	require.Len(t, untrustedEvents(rec), 1)
}

// TestDriveRegister_RegistryLookupError_DistinguishedFromMissingRow: a
// sqlite-side error after RegisterDone reports differently from "no row"
// so containment subscribers can tell I/O failures apart from "agent lied
// about Register success".
func TestDriveRegister_RegistryLookupError_DistinguishedFromMissingRow(t *testing.T) {
	d, rec := dialerWithTopic(t, &RegistryMock{
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

	require.Eventually(t, func() bool { return len(registeredEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
	registered := registeredEvents(rec)
	assert.False(t, registered[0].Message.RegisterOk)
	assert.Contains(t, registered[0].Message.Detail, "registry lookup error")
	require.Len(t, untrustedEvents(rec), 1)
}

// TestDriveRegister_MissingRowAfterRegisterDone: success-reported but row
// absent — clawkerd-side regression. Distinguished from I/O error in the
// published detail.
func TestDriveRegister_MissingRowAfterRegisterDone(t *testing.T) {
	d, rec := dialerWithTopic(t, &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) { return nil, ErrUnknownAgent },
	})

	streamCtx, cancel := context.WithCancel(t.Context())
	stream := newFakeStream(streamCtx)
	stream.pushRegisterDone("register-abc", true, "")

	res := happyEstablishResult(stream, "clawker.myapp.dev", sha256.Sum256([]byte("p")))
	res.StreamCancel = cancel

	d.driveRegister(t.Context(), "abc", res, logger.Nop())

	require.Eventually(t, func() bool { return len(registeredEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
	registered := registeredEvents(rec)
	assert.False(t, registered[0].Message.RegisterOk)
	assert.Contains(t, registered[0].Message.Detail, "registry row missing")
}

// --- dispatchAgentEvents (load-bearing asymmetric-trust tests) -----------

// TestDispatchAgentEvents_Match_PublishesNoExtraEvent: when the peer
// matches a registry row, dispatchAgentEvents publishes nothing extra (the
// session-connected event already populated the worldview).
func TestDispatchAgentEvents_Match_PublishesNoExtraEvent(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer"))
	expectedAgentFullName := auth.AgentFullName(auth.MustProjectSlug("myapp"), auth.MustAgentName("dev"))
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return &Entry{
				ContainerID: "abc",
				AgentName:   auth.MustAgentName("dev"),
				Project:     auth.MustProjectSlug("myapp"),
				Thumbprint:  thumb,
			}, nil
		},
	}
	d, rec := dialerWithTopic(t, reg)

	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := newFakeStream(streamCtx)
	res := happyEstablishResult(stream, expectedAgentFullName, thumb)
	res.StreamCancel = cancel

	d.dispatchAgentEvents(t.Context(), "abc", res, logger.Nop())

	// Give the pipe a beat to deliver anything that would have been
	// published; a Match must publish neither registered nor untrusted.
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, registeredEvents(rec), "Match must NOT publish a registered event")
	assert.Empty(t, untrustedEvents(rec), "Match must NOT publish an untrusted event")
}

// TestDispatchAgentEvents_OutcomesPinned pins the typed Reason for each
// non-Match outcome — a regression that swapped ThumbprintMismatch and
// LookupError in the switch would silently mis-classify containment policy.
func TestDispatchAgentEvents_OutcomesPinned(t *testing.T) {
	cases := []struct {
		name       string
		reg        *RegistryMock
		peer       peerInfo
		wantReason Reason
	}{
		{
			name: "ThumbprintMismatch",
			reg: &RegistryMock{
				LookupByContainerIDFunc: func(string) (*Entry, error) {
					return &Entry{
						ContainerID: "abc",
						AgentName:   auth.MustAgentName("dev"),
						Project:     auth.MustProjectSlug("myapp"),
						Thumbprint:  sha256.Sum256([]byte("registered-cert")),
					}, nil
				},
			},
			peer:       peerInfo{PeerAgentFullName: "clawker.myapp.dev", PeerThumbprint: sha256.Sum256([]byte("live-cert"))},
			wantReason: ReasonThumbprintMismatch,
		},
		{
			name: "LookupError",
			reg: &RegistryMock{
				LookupByContainerIDFunc: func(string) (*Entry, error) {
					return nil, errors.New("disk i/o failure")
				},
			},
			peer:       peerInfo{PeerAgentFullName: "clawker.x.y", PeerThumbprint: sha256.Sum256([]byte("p"))},
			wantReason: ReasonCertInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, rec := dialerWithTopic(t, tc.reg)
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

			require.Eventually(t, func() bool { return len(untrustedEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
			assert.Equal(t, tc.wantReason, untrustedEvents(rec)[0].Message.Reason)
		})
	}
}

// TestDispatchAgentEvents_AsymmetricTrust_StreamStaysOpen pins the
// load-bearing invariant of this branch: a ThumbprintMismatch (untrusted
// classification) must NOT cancel or close the Session stream — CP must
// remain reachable to dispatch containment commands against the untrusted
// agent.
func TestDispatchAgentEvents_AsymmetricTrust_StreamStaysOpen(t *testing.T) {
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return &Entry{
				ContainerID: "abc",
				AgentName:   auth.MustAgentName("dev"),
				Project:     auth.MustProjectSlug("myapp"),
				Thumbprint:  sha256.Sum256([]byte("registered")),
			}, nil
		},
	}
	d, rec := dialerWithTopic(t, reg)
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

	require.Eventually(t, func() bool { return len(untrustedEvents(rec)) == 1 }, time.Second, 10*time.Millisecond)
	assert.Equal(t, ReasonThumbprintMismatch, untrustedEvents(rec)[0].Message.Reason)
}
