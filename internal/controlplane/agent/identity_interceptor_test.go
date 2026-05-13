package agent

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
)

var (
	testProjectSlug = auth.MustProjectSlug("p")
	testAgentName   = auth.MustAgentName("alpha")
)

const (
	testAgentFullName = "clawker.p.alpha"
	testContainerID   = "ctr-xyz"
	testPeerIP        = "10.0.0.1"
)

// fakePeerLookup is a stub ContainerByPeerIP for tests.
type fakePeerLookup struct {
	fn func(ctx context.Context, ip netip.Addr) (ResolvedContainer, error)
}

func (f *fakePeerLookup) LookupByIP(ctx context.Context, ip netip.Addr) (ResolvedContainer, error) {
	return f.fn(ctx, ip)
}

func okPeerLookup() *fakePeerLookup {
	return &fakePeerLookup{
		fn: func(_ context.Context, _ netip.Addr) (ResolvedContainer, error) {
			return ResolvedContainer{
				ContainerID: testContainerID,
				Project:     testProjectSlug,
				AgentName:   testAgentName,
			}, nil
		},
	}
}

func errPeerLookup(err error) *fakePeerLookup {
	return &fakePeerLookup{
		fn: func(_ context.Context, _ netip.Addr) (ResolvedContainer, error) {
			return ResolvedContainer{}, err
		},
	}
}

// ctxWithPeer builds a fake gRPC context carrying TLS peer info — the
// minimum surface peerIdentityFromContext reads (PeerCertificates +
// peer.Addr). An empty agentFullName omits the URI SAN entirely.
func ctxWithPeer(cn, agentFullName string, ip net.IP) context.Context {
	cert := &x509.Certificate{Raw: []byte("cert-der"), Subject: pkix.Name{CommonName: cn}}
	if agentFullName != "" {
		u, err := url.Parse(auth.AgentSANScheme + agentFullName)
		if err == nil {
			cert.URIs = []*url.URL{u}
		}
	}
	tlsInfo := credentials.TLSInfo{}
	tlsInfo.State.PeerCertificates = []*x509.Certificate{cert}
	addr := &net.TCPAddr{IP: ip, Port: 1234}
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr:     addr,
		AuthInfo: tlsInfo,
	})
}

// fixturePeerCtx returns a ctx for an mTLS-authenticated agent peer
// presenting the production clawkerd CN, the canonical agent SAN
// matching testAgentFullName, and an IPv4 source IP matching what
// the default fakePeerLookup is wired to accept.
func fixturePeerCtx() context.Context {
	return ctxWithPeer(consts.ContainerClawkerd, testAgentFullName, net.ParseIP(testPeerIP))
}

func registerMethod() string {
	return "/" + agentv1.ServiceName + "/Register"
}

// --- Unary happy path ---

// TestIdentityInterceptor_NilPeerLookup_ReturnsError pins the
// constructor contract: a nil resolver returns a wiring error rather
// than panicking. Panicking would strand pinned eBPF programs with no
// supervisor (root CLAUDE.md hard rule); returning an error lets
// main.go log event=agent_identity_unavailable and degrade the
// AgentService surface while CP/firewall/admin stay up.
func TestIdentityInterceptor_NilPeerLookup_ReturnsError(t *testing.T) {
	unary, stream, err := IdentityInterceptor(nil, nil)
	require.Error(t, err)
	assert.Nil(t, unary, "no interceptor must be returned on wiring error")
	assert.Nil(t, stream, "no interceptor must be returned on wiring error")
}

func TestIdentityInterceptor_HappyPath_AttachesResolvedContainer(t *testing.T) {
	unary, _, _ := IdentityInterceptor(okPeerLookup(), nil)

	var (
		gotResolved ResolvedContainer
		gotOK       bool
	)
	_, err := unary(
		fixturePeerCtx(),
		"req",
		&grpc.UnaryServerInfo{FullMethod: registerMethod()},
		func(ctx context.Context, _ any) (any, error) {
			gotResolved, gotOK = ResolvedContainerFromContext(ctx)
			return nil, nil
		},
	)
	require.NoError(t, err)
	require.True(t, gotOK, "handler ctx must carry ResolvedContainer")
	assert.Equal(t, testContainerID, gotResolved.ContainerID)
	assert.Equal(t, testProjectSlug, gotResolved.Project)
	assert.Equal(t, testAgentName, gotResolved.AgentName)
}

// TestIdentityInterceptor_Register_HitsTrustCheck pins the load-bearing
// invariant: Register has no identity opt-out. A peer whose IP doesn't
// resolve to a purpose=agent container cannot reach the Register
// handler. A future regression that re-introduces an opt-out for
// Register would make this test fail.
func TestIdentityInterceptor_Register_HitsTrustCheck(t *testing.T) {
	unary, _, _ := IdentityInterceptor(errPeerLookup(ErrNoContainerForPeerIP), nil)

	_, err := unary(
		fixturePeerCtx(),
		"req",
		&grpc.UnaryServerInfo{FullMethod: registerMethod()},
		func(_ context.Context, _ any) (any, error) {
			t.Fatal("Register handler must NOT run when peer IP doesn't resolve")
			return nil, nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// --- Stage 1: CN pin ---

func TestIdentityInterceptor_WrongCN_PermissionDenied(t *testing.T) {
	unary, _, _ := IdentityInterceptor(okPeerLookup(), nil)

	wrongCN := ctxWithPeer("not-clawkerd", testAgentFullName, net.ParseIP(testPeerIP))
	_, err := unary(
		wrongCN,
		"req",
		&grpc.UnaryServerInfo{FullMethod: registerMethod()},
		func(_ context.Context, _ any) (any, error) {
			t.Fatal("handler must NOT run when CN pin fails")
			return nil, nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestIdentityInterceptor_NoPeerCert_PermissionDenied(t *testing.T) {
	unary, _, _ := IdentityInterceptor(okPeerLookup(), nil)

	_, err := unary(
		context.Background(),
		"req",
		&grpc.UnaryServerInfo{FullMethod: registerMethod()},
		func(_ context.Context, _ any) (any, error) {
			t.Fatal("handler must NOT run when peer info is missing")
			return nil, nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// --- Stage 2a: empty agent SAN ---

func TestIdentityInterceptor_EmptyCertSAN_PermissionDenied(t *testing.T) {
	unary, _, _ := IdentityInterceptor(okPeerLookup(), nil)

	noSAN := ctxWithPeer(consts.ContainerClawkerd, "", net.ParseIP(testPeerIP))
	_, err := unary(
		noSAN,
		"req",
		&grpc.UnaryServerInfo{FullMethod: registerMethod()},
		func(_ context.Context, _ any) (any, error) {
			t.Fatal("handler must NOT run when cert has no agent SAN")
			return nil, nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestIdentityInterceptor_MalformedCertSAN_PermissionDenied pins
// stage 2a's malformed-SAN branch. A cert that carries a URI with
// scheme urn:clawker:agent: but an empty tail is a producer-side bug;
// the interceptor short-circuits with PermissionDenied just like the
// missing-SAN case, but the structured log surface emits
// event=agent_identity_malformed_agent_san so operators can
// distinguish it from a clean no-SAN cert.
func TestIdentityInterceptor_MalformedCertSAN_PermissionDenied(t *testing.T) {
	unary, _, _ := IdentityInterceptor(okPeerLookup(), nil)

	// Cert has the agent SAN scheme but empty tail.
	u, err := url.Parse(auth.AgentSANScheme)
	require.NoError(t, err)
	cert := &x509.Certificate{
		Raw:     []byte("cert-der"),
		Subject: pkix.Name{CommonName: consts.ContainerClawkerd},
		URIs:    []*url.URL{u},
	}
	tlsInfo := credentials.TLSInfo{}
	tlsInfo.State.PeerCertificates = []*x509.Certificate{cert}
	addr := &net.TCPAddr{IP: net.ParseIP(testPeerIP), Port: 1234}
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr:     addr,
		AuthInfo: tlsInfo,
	})

	_, err = unary(
		ctx,
		"req",
		&grpc.UnaryServerInfo{FullMethod: registerMethod()},
		func(_ context.Context, _ any) (any, error) {
			t.Fatal("handler must NOT run when agent SAN is malformed")
			return nil, nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// --- Stage 2b: peer IP resolve (table-driven across error sentinels) ---

func TestIdentityInterceptor_Stage2_ResolverErrors_PermissionDenied(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"NoContainerForPeerIP", ErrNoContainerForPeerIP},
		{"InvalidAgentLabels", ErrInvalidAgentLabels},
		{"GenericDaemonError", errors.New("docker daemon broken")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unary, _, _ := IdentityInterceptor(errPeerLookup(tc.err), nil)
			_, err := unary(
				fixturePeerCtx(),
				"req",
				&grpc.UnaryServerInfo{FullMethod: registerMethod()},
				func(_ context.Context, _ any) (any, error) {
					t.Fatal("handler must NOT run when peer lookup fails")
					return nil, nil
				},
			)
			require.Error(t, err)
			assert.Equal(t, codes.PermissionDenied, status.Code(err))
		})
	}
}

// --- Stage 3: SAN vs label compare ---

func TestIdentityInterceptor_CertSANvsLabelMismatch_PermissionDenied(t *testing.T) {
	unary, _, _ := IdentityInterceptor(okPeerLookup(), nil)

	// Cert SAN claims "clawker.p.beta" but labels resolve to "clawker.p.alpha".
	wrongSAN := ctxWithPeer(consts.ContainerClawkerd, "clawker.p.beta", net.ParseIP(testPeerIP))
	_, err := unary(
		wrongSAN,
		"req",
		&grpc.UnaryServerInfo{FullMethod: registerMethod()},
		func(_ context.Context, _ any) (any, error) {
			t.Fatal("handler must NOT run when cert SAN does not match labels")
			return nil, nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// --- Stream interceptor ---

type streamFake struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *streamFake) Context() context.Context { return s.ctx }

// TestIdentityInterceptor_Stream_HappyPath_WrappedContextCarriesResolvedContainer
// guards the load-bearing wrapper pitfall: if Context() is promoted from
// the embedded ServerStream instead of overridden on the wrapper, the
// handler reads the original ctx without the resolved container,
// silently breaking identity binding for every streaming RPC.
func TestIdentityInterceptor_Stream_HappyPath_WrappedContextCarriesResolvedContainer(t *testing.T) {
	_, stream, _ := IdentityInterceptor(okPeerLookup(), nil)

	ss := &streamFake{ctx: fixturePeerCtx()}
	var (
		gotResolved ResolvedContainer
		gotOK       bool
	)
	err := stream(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: registerMethod()},
		func(_ any, wrapped grpc.ServerStream) error {
			gotResolved, gotOK = ResolvedContainerFromContext(wrapped.Context())
			return nil
		},
	)
	require.NoError(t, err)
	require.True(t, gotOK, "wrapped stream's Context() must carry the resolved container")
	assert.Equal(t, testContainerID, gotResolved.ContainerID)
}

// TestIdentityInterceptor_Stream_NoPeerCert_PermissionDenied pins the
// stream interceptor's reject path returns the error directly instead
// of swallowing it inside the wrapper (distinct from the unary case
// because the stream wrapper is its own load-bearing seam).
func TestIdentityInterceptor_Stream_NoPeerCert_PermissionDenied(t *testing.T) {
	_, stream, _ := IdentityInterceptor(okPeerLookup(), nil)

	ss := &streamFake{ctx: context.Background()}
	err := stream(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: registerMethod()},
		func(_ any, _ grpc.ServerStream) error {
			t.Fatal("handler must NOT run when peer info is missing")
			return nil
		},
	)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// --- Ctx helper ---

// TestWithResolvedContainer_EmptyContainerIDIsNoOp pins the silent-
// identity-vacuum prevention contract: a zero-value ResolvedContainer
// passed in produces a ctx where ResolvedContainerFromContext returns
// ok=false, so downstream handlers consuming identity reject naturally.
// Replaces a panic on the serving path (forbidden by the CP no-panic
// security contract).
func TestWithResolvedContainer_EmptyContainerIDIsNoOp(t *testing.T) {
	ctx := WithResolvedContainer(context.Background(), ResolvedContainer{})
	_, ok := ResolvedContainerFromContext(ctx)
	assert.False(t, ok, "zero-value resolved container must not appear authenticated downstream")
}
