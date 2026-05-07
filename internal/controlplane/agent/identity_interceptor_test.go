package agent

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"

	"github.com/schmitthub/clawker/internal/logger"
)

// ctxWithPeer builds a fake gRPC context carrying TLS peer info — the
// minimum surface peerIdentityFromContext reads (PeerCertificates +
// peer.Addr).
func ctxWithPeer(certRaw []byte, cn string, ip net.IP) context.Context {
	cert := &x509.Certificate{Raw: certRaw, Subject: pkix.Name{CommonName: cn}}
	tlsInfo := credentials.TLSInfo{}
	tlsInfo.State.PeerCertificates = []*x509.Certificate{cert}
	addr := &net.TCPAddr{IP: ip, Port: 1234}
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr:     addr,
		AuthInfo: tlsInfo,
	})
}

// hypotheticalIdentityRequiredMethod is a stand-in path used by tests
// that exercise the identity-required (registry-lookup) branch of the
// interceptor. AgentService currently has no inbound RPCs, so this
// path matches no real method — the interceptor accepts any
// FullMethod string and routes anything not in the opt-out map
// through the lookup path.
func hypotheticalIdentityRequiredMethod() string {
	return "/" + agentv1.ServiceName + "/FutureIdentityRequiredRPC"
}

// fixturePeerCtx returns a ctx that looks like a real mTLS-authenticated
// gRPC call: peer cert with the supplied raw bytes, peer IP set so
// peerIdentityFromContext succeeds.
func fixturePeerCtx(certRaw []byte) context.Context {
	return ctxWithPeer(certRaw, "test-cn", net.IPv4(10, 0, 0, 1))
}

// --- Unary interceptor cases ---

func TestIdentityInterceptor_Unary_RegistryHit_AttachesEntry(t *testing.T) {
	certRaw := []byte("cert-der")
	wantThumb := sha256.Sum256(certRaw)
	wantEntry := &Entry{
		AgentName:    "alpha",
		Project:      "p",
		ContainerID:  "ctr-xyz",
		Thumbprint:   wantThumb,
		RegisteredAt: time.Unix(100, 0),
	}

	var (
		lookupArg [sha256.Size]byte
		gotCN     string
	)
	reg := &RegistryMock{
		LookupFunc: func(thumbprint [sha256.Size]byte, cn string) (*Entry, error) {
			lookupArg = thumbprint
			gotCN = cn
			return wantEntry, nil
		},
	}
	unary, _ := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	var gotEntry *Entry
	_, err := unary(
		fixturePeerCtx(certRaw),
		"req",
		&grpc.UnaryServerInfo{FullMethod: hypotheticalIdentityRequiredMethod()},
		func(ctx context.Context, _ any) (any, error) {
			gotEntry, _ = EntryFromContext(ctx)
			return nil, nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, wantThumb, lookupArg, "interceptor must hash peer cert and pass to Lookup")
	assert.Equal(t, "test-cn", gotCN, "interceptor must forward peer cert CN to Lookup for the cross-check")
	require.NotNil(t, gotEntry)
	assert.Equal(t, wantEntry.AgentName, gotEntry.AgentName)
}

func TestIdentityInterceptor_Unary_LookupMiss_PermissionDenied(t *testing.T) {
	reg := &RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte, _ string) (*Entry, error) {
			return nil, ErrUnknownAgent
		},
	}
	unary, _ := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	_, err := unary(
		fixturePeerCtx([]byte("cert")),
		"req",
		&grpc.UnaryServerInfo{FullMethod: hypotheticalIdentityRequiredMethod()},
		func(_ context.Context, _ any) (any, error) {
			t.Fatal("handler must NOT run on lookup miss")
			return nil, nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestIdentityInterceptor_Unary_NoPeerCert_PermissionDenied(t *testing.T) {
	reg := &RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte, _ string) (*Entry, error) {
			t.Fatal("Lookup must NOT be called when peer info is missing")
			return nil, nil
		},
	}
	unary, _ := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	_, err := unary(
		context.Background(),
		"req",
		&grpc.UnaryServerInfo{FullMethod: hypotheticalIdentityRequiredMethod()},
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
type streamFake struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *streamFake) Context() context.Context { return s.ctx }

// TestIdentityInterceptor_Stream_RegistryHit_WrappedContextCarriesEntry
// guards the load-bearing wrapper pitfall: if Context() is promoted from
// the embedded ServerStream instead of overridden on the wrapper, the
// handler reads the original ctx without the entry, silently breaking
// identity binding for every streaming RPC.
func TestIdentityInterceptor_Stream_RegistryHit_WrappedContextCarriesEntry(t *testing.T) {
	certRaw := []byte("cert-der-stream")
	wantThumb := sha256.Sum256(certRaw)
	wantEntry := &Entry{
		AgentName:    "beta",
		Project:      "p",
		ContainerID:  "ctr-stream",
		Thumbprint:   wantThumb,
		RegisteredAt: time.Unix(200, 0),
	}
	reg := &RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte, _ string) (*Entry, error) {
			return wantEntry, nil
		},
	}
	_, stream := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	ss := &streamFake{ctx: fixturePeerCtx(certRaw)}
	var gotEntry *Entry
	err := stream(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: hypotheticalIdentityRequiredMethod()},
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
	reg := &RegistryMock{
		LookupFunc: func(_ [sha256.Size]byte, _ string) (*Entry, error) {
			t.Fatal("Lookup must NOT be called when peer info is missing")
			return nil, nil
		},
	}
	_, stream := IdentityInterceptor(reg, IdentityOptedOutMethods(), nil)

	ss := &streamFake{ctx: context.Background()}
	err := stream(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: hypotheticalIdentityRequiredMethod()},
		func(_ any, _ grpc.ServerStream) error {
			t.Fatal("handler must NOT run when peer info is missing")
			return nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestIdentityOptedOut_EmptyForCurrentBranch pins the empty-opt-out
// TestWithEntry_NilPanics locks the fail-fast contract: attempting to
// attach a nil entry would round-trip back from EntryFromContext as
// (nil, true) without the defensive nil-check in EntryFromContext —
// silent identity vacuum followed by a downstream nil-deref panic.
func TestWithEntry_NilPanics(t *testing.T) {
	assert.Panics(t, func() { WithEntry(context.Background(), nil) },
		"WithEntry with a nil entry must panic")
}

// TestIdentityInterceptor_StaleOptedOutKey_Panics locks the runtime
// validation contract: a key in the opt-out map that doesn't match any
// real AgentService RPC must panic at construction so a typo or stale
// rename surfaces during startup instead of silently locking a real
// method out.
func TestIdentityInterceptor_StaleOptedOutKey_Panics(t *testing.T) {
	reg := NewRegistry(logger.Nop())
	stale := map[string]bool{
		"/clawker.agent.v1.AgentService/NotARealMethod": true,
	}
	assert.Panics(t, func() { IdentityInterceptor(reg, stale, logger.Nop()) },
		"stale opt-out key must panic at construction")
}
