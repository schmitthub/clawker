package agent

import (
	"context"
	"crypto/sha256"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	regmocks "github.com/schmitthub/clawker/internal/controlplane/agentregistry/mocks"
)

// agentMethodPath is the full gRPC method path the interceptor matches
// against the opt-out map. Building it from agentv1.ServiceName keeps
// the tests resilient to a future rename of the proto service.
func agentMethodPath(method string) string {
	return "/" + agentv1.ServiceName + "/" + method
}

// fixturePeerCtx returns a ctx that looks like a real mTLS-authenticated
// gRPC call: peer cert with the supplied raw bytes, peer IP set so
// peerIdentityAndIP succeeds. Reuses the package-local ctxWithPeer
// helper from handler_test.go.
func fixturePeerCtx(certRaw []byte) context.Context {
	return ctxWithPeer(certRaw, "test-cn", net.IPv4(10, 0, 0, 1))
}

// --- Unary interceptor cases ---

func TestIdentityInterceptor_Unary_OptedOut_SkipsLookup(t *testing.T) {
	// Connect is on the opt-out list — the interceptor MUST NOT touch
	// the registry. Confirm by configuring a Lookup that fatally
	// fails the test if called.
	reg := &regmocks.RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte) (*agentregistry.Entry, error) {
			t.Fatal("Lookup must NOT be called for opted-out methods")
			return nil, nil
		},
	}
	unary, _ := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	called := false
	resp, err := unary(
		context.Background(),
		"req",
		&grpc.UnaryServerInfo{FullMethod: agentMethodPath("Connect")},
		func(ctx context.Context, req any) (any, error) {
			called = true
			// No entry attached on the opt-out path.
			_, ok := EntryFromContext(ctx)
			assert.False(t, ok, "opt-out methods must NOT have an entry attached")
			return "ok", nil
		},
	)
	require.NoError(t, err)
	assert.True(t, called, "handler must run on opt-out path")
	assert.Equal(t, "ok", resp)
}

func TestIdentityInterceptor_Unary_RegistryHit_AttachesEntry(t *testing.T) {
	certRaw := []byte("cert-der")
	wantThumb := sha256.Sum256(certRaw)
	wantEntry := &agentregistry.Entry{
		AgentName:    "clawker.alpha",
		ContainerID:  "ctr-xyz",
		Thumbprint:   wantThumb,
		RegisteredAt: time.Unix(100, 0),
	}

	var lookupArg [sha256.Size]byte
	reg := &regmocks.RegistryMock{
		LookupFunc: func(thumbprint [sha256.Size]byte) (*agentregistry.Entry, error) {
			lookupArg = thumbprint
			return wantEntry, nil
		},
	}
	unary, _ := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	var gotEntry *agentregistry.Entry
	_, err := unary(
		fixturePeerCtx(certRaw),
		"req",
		&grpc.UnaryServerInfo{FullMethod: agentMethodPath("Events")},
		func(ctx context.Context, _ any) (any, error) {
			gotEntry, _ = EntryFromContext(ctx)
			return nil, nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, wantThumb, lookupArg, "interceptor must hash peer cert and pass to Lookup")
	require.NotNil(t, gotEntry)
	assert.Equal(t, wantEntry.AgentName, gotEntry.AgentName)
}

func TestIdentityInterceptor_Unary_LookupMiss_PermissionDenied(t *testing.T) {
	reg := &regmocks.RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte) (*agentregistry.Entry, error) {
			return nil, agentregistry.ErrUnknownAgent
		},
	}
	unary, _ := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	_, err := unary(
		fixturePeerCtx([]byte("cert")),
		"req",
		&grpc.UnaryServerInfo{FullMethod: agentMethodPath("Events")},
		func(_ context.Context, _ any) (any, error) {
			t.Fatal("handler must NOT run on lookup miss")
			return nil, nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestIdentityInterceptor_Unary_NoPeerCert_PermissionDenied(t *testing.T) {
	// Bare context — peerIdentityAndIP fails. Reject without touching
	// the registry.
	reg := &regmocks.RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte) (*agentregistry.Entry, error) {
			t.Fatal("Lookup must NOT be called when peer info is missing")
			return nil, nil
		},
	}
	unary, _ := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	_, err := unary(
		context.Background(),
		"req",
		&grpc.UnaryServerInfo{FullMethod: agentMethodPath("Events")},
		func(_ context.Context, _ any) (any, error) {
			t.Fatal("handler must NOT run when peer info is missing")
			return nil, nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// --- Stream interceptor cases ---

// streamFake satisfies grpc.ServerStream with a custom Context only.
// All other methods inherit from the nil-embedded ServerStream and
// will panic if exercised — surfacing any test that drifts beyond
// what the production interceptor calls (which is exclusively Context).
type streamFake struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *streamFake) Context() context.Context { return s.ctx }

func TestIdentityInterceptor_Stream_OptedOut_SkipsLookup(t *testing.T) {
	reg := &regmocks.RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte) (*agentregistry.Entry, error) {
			t.Fatal("Lookup must NOT be called for opted-out methods")
			return nil, nil
		},
	}
	_, stream := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	ss := &streamFake{ctx: context.Background()}
	called := false
	err := stream(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: agentMethodPath("Connect")},
		func(_ any, ss grpc.ServerStream) error {
			called = true
			_, ok := EntryFromContext(ss.Context())
			assert.False(t, ok, "opt-out streaming methods must NOT have an entry attached")
			return nil
		},
	)
	require.NoError(t, err)
	assert.True(t, called)
}

// TestIdentityInterceptor_Stream_RegistryHit_WrappedContextCarriesEntry
// guards the load-bearing wrapper pitfall: if Context() is promoted from
// the embedded ServerStream instead of overridden on the wrapper, the
// handler reads the original ctx without the entry, silently breaking
// identity binding for every streaming RPC. The assertion below
// inspects ss.Context() (NOT the resolve-time ctx) so a regression
// where the wrapper forgets to override Context() fails this test.
func TestIdentityInterceptor_Stream_RegistryHit_WrappedContextCarriesEntry(t *testing.T) {
	certRaw := []byte("cert-der-stream")
	wantThumb := sha256.Sum256(certRaw)
	wantEntry := &agentregistry.Entry{
		AgentName:    "clawker.beta",
		ContainerID:  "ctr-stream",
		Thumbprint:   wantThumb,
		RegisteredAt: time.Unix(200, 0),
	}
	reg := &regmocks.RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte) (*agentregistry.Entry, error) {
			return wantEntry, nil
		},
	}
	_, stream := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	ss := &streamFake{ctx: fixturePeerCtx(certRaw)}
	var gotEntry *agentregistry.Entry
	err := stream(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: agentMethodPath("Events")},
		func(_ any, wrapped grpc.ServerStream) error {
			gotEntry, _ = EntryFromContext(wrapped.Context())
			return nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, gotEntry, "wrapped stream's Context() must carry the registered entry")
	assert.Equal(t, wantEntry.AgentName, gotEntry.AgentName)
}

func TestIdentityInterceptor_Stream_NoPeerCert_PermissionDenied(t *testing.T) {
	// Bare context — peerIdentityAndIP fails. Reject without touching
	// the registry. Ordering matters: a regression that moved the
	// registry lookup before the peer-info check would burn introspector
	// traffic and could leak whether a thumbprint is registered.
	reg := &regmocks.RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte) (*agentregistry.Entry, error) {
			t.Fatal("Lookup must NOT be called when peer info is missing")
			return nil, nil
		},
	}
	_, stream := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	ss := &streamFake{ctx: context.Background()}
	err := stream(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: agentMethodPath("Events")},
		func(_ any, _ grpc.ServerStream) error {
			t.Fatal("handler must NOT run when peer info is missing")
			return nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestIdentityInterceptor_Stream_LookupMiss_PermissionDenied(t *testing.T) {
	reg := &regmocks.RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte) (*agentregistry.Entry, error) {
			return nil, agentregistry.ErrUnknownAgent
		},
	}
	_, stream := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	ss := &streamFake{ctx: fixturePeerCtx([]byte("cert"))}
	err := stream(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: agentMethodPath("Events")},
		func(_ any, _ grpc.ServerStream) error {
			t.Fatal("handler must NOT run on lookup miss")
			return nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestIdentityOptedOut_NoStaleEntriesAndConnectLocked enforces two
// narrow but real invariants on IdentityOptedOutMethods():
//
//  1. Every key corresponds to a real RPC in AgentService_ServiceDesc
//     (a rename/delete that left a stale opt-out behind would silently
//     let a future RPC reuse the path and bypass identity).
//  2. Connect specifically opts out — accidentally dropping it would
//     force an identity lookup before the agent is registered,
//     deadlocking every clawkerd boot.
//
// NOTE on the implicit default: identity-required is the safe default —
// any RPC NOT in optedOut falls through to the registry-lookup path.
// A future RPC added to the proto without a deliberate decision routes
// through identity-required by construction (fail-secure), so this
// test does NOT need to enforce a "must have an explicit policy" rule.
// If a future RPC legitimately needs to opt out, the maintainer must
// add it to IdentityOptedOutMethods() and a code-review conversation
// is the forcing function — not this test.
func TestIdentityOptedOut_NoStaleEntriesAndConnectLocked(t *testing.T) {
	optedOut := IdentityOptedOutMethods()
	desc := agentv1.AgentService_ServiceDesc
	const svc = "/" + agentv1.ServiceName + "/"

	protoMethods := map[string]bool{}
	for _, m := range desc.Methods {
		protoMethods[svc+m.MethodName] = true
	}
	for _, s := range desc.Streams {
		protoMethods[svc+s.StreamName] = true
	}

	for method := range optedOut {
		assert.Truef(t, protoMethods[method],
			"IdentityOptedOutMethods() contains %s which is not in AgentService_ServiceDesc — remove stale entry", method)
	}

	assert.True(t, optedOut[svc+"Connect"],
		"AgentService.Connect MUST be opted out — it authenticates itself via slot consume")
}

// TestWithEntry_NilPanics locks the fail-fast contract: attempting to
// attach a nil entry would round-trip back from EntryFromContext as
// (nil, true) without the defensive nil-check in EntryFromContext —
// silent identity vacuum followed by a downstream nil-deref panic in
// the handler. Mirrors agentregistry.Add's panic-on-misuse posture.
func TestWithEntry_NilPanics(t *testing.T) {
	assert.PanicsWithValue(t,
		"agent: WithEntry called with nil entry",
		func() {
			WithEntry(context.Background(), nil)
		})
}
