package adminclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	hostproxyv1 "github.com/schmitthub/clawker/api/hostproxy/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

// fakeHydra returns a TLS test server that answers any /oauth2/token POST with a
// valid token JSON (counting hits), plus a tls.Config trusting its cert. It
// stands in for Hydra so tokenSource.token runs end-to-end without a real CP.
func fakeHydra(t *testing.T) (url string, tlsCfg *tls.Config, hits *atomic.Int32) {
	t.Helper()
	hits = &atomic.Int32{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv.URL, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, hits
}

// TestTokenSource_MintsAndCaches verifies token() exchanges the signed assertion
// at Hydra on first use, serves the cached token within its TTL without
// re-minting, and re-mints once the cached token is within tokenRefreshMargin of
// expiry.
func TestTokenSource_MintsAndCaches(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	url, tlsCfg, hits := fakeHydra(t)
	ts := newTokenSource(key, consts.ClientIDCLI, string(adminv1.ScopeAdmin), url, tlsCfg)

	tok, err := ts.token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if tok != "tok" {
		t.Fatalf("token = %q, want %q", tok, "tok")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hydra hits = %d, want 1 (first mint)", got)
	}

	// Within TTL: the cache short-circuits, no second Hydra exchange.
	if _, err := ts.token(context.Background()); err != nil {
		t.Fatalf("cached token: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hydra hits = %d, want 1 (cache hit must not re-mint)", got)
	}

	// Within the refresh margin of expiry: re-mint.
	ts.expiresAt = time.Now().Add(tokenRefreshMargin / 2)
	if _, err := ts.token(context.Background()); err != nil {
		t.Fatalf("re-mint near expiry: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("hydra hits = %d, want 2 (near-expiry triggers re-mint)", got)
	}
}

// TestStreamInterceptor_AttachesBearerToken pins that streaming calls
// carry the authorization metadata. grpc-go routes unary and streaming
// RPCs through DISJOINT interceptor chains, so the bearer that unary
// calls get from unaryInterceptor never reaches a stream — a regression
// dropping streamInterceptor from Dial would send WatchSOS to the CP
// with no token at all, rejected Unauthenticated on every attempt.
func TestStreamInterceptor_AttachesBearerToken(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	url, tlsCfg, _ := fakeHydra(t)
	ts := newTokenSource(key, consts.ClientIDCLI, string(adminv1.ScopeAdmin), url, tlsCfg)

	var gotAuth []string
	streamer := func(ctx context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
		md, _ := metadata.FromOutgoingContext(ctx)
		gotAuth = md.Get("authorization")
		return nil, nil //nolint:nilnil // the fake streamer's return values are unused; only the captured metadata matters
	}

	_, err = ts.streamInterceptor()(
		context.Background(), nil, nil, "/clawker.admin.v1.AdminService/WatchSOS", streamer,
	)
	if err != nil {
		t.Fatalf("stream interceptor: %v", err)
	}
	if len(gotAuth) != 1 || gotAuth[0] != "Bearer tok" {
		t.Fatalf("authorization metadata = %v, want [Bearer tok]", gotAuth)
	}
}

// TestTokenSource_HostProxyIdentity: the host proxy's token request names
// its own client (iss and sub of the assertion) and asks only for the
// hostproxy:serve scope.
func TestTokenSource_HostProxyIdentity(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var form url.Values
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if parseErr := r.ParseForm(); parseErr != nil {
			t.Errorf("parse form: %v", parseErr)
		}
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	var tlsCfg tls.Config
	tlsCfg.RootCAs = pool
	tlsCfg.MinVersion = tls.VersionTLS12
	ts := newTokenSource(key, consts.ClientIDHostProxy, string(hostproxyv1.ScopeServe), srv.URL, &tlsCfg)
	if _, tokErr := ts.token(context.Background()); tokErr != nil {
		t.Fatalf("token: %v", tokErr)
	}

	if got := form.Get("scope"); got != string(hostproxyv1.ScopeServe) {
		t.Errorf("scope = %q, want %q", got, hostproxyv1.ScopeServe)
	}
	parsed, err := josejwt.ParseSigned(form.Get("client_assertion"), []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		t.Fatalf("parse assertion: %v", err)
	}
	var claims josejwt.Claims
	if claimsErr := parsed.Claims(&key.PublicKey, &claims); claimsErr != nil {
		t.Fatalf("assertion claims: %v", claimsErr)
	}
	if claims.Issuer != consts.ClientIDHostProxy || claims.Subject != consts.ClientIDHostProxy {
		t.Errorf("assertion iss/sub = %q/%q, want %q", claims.Issuer, claims.Subject, consts.ClientIDHostProxy)
	}
}

// refusedStream is a grpc.ClientStream whose Header and RecvMsg return err,
// as a stream the CP refuses ends trailers-only with its status.
type refusedStream struct {
	grpc.ClientStream

	err error
}

func (s refusedStream) Header() (metadata.MD, error) { return nil, s.err }
func (s refusedStream) RecvMsg(any) error            { return s.err }

// newFakeHydraTokenSource returns a token source for the host proxy client
// that mints from fakeHydra, and the Hydra hit counter.
func newFakeHydraTokenSource(t *testing.T) (*tokenSource, *atomic.Int32) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tokenURL, tlsCfg, hits := fakeHydra(t)
	return newTokenSource(key, consts.ClientIDHostProxy, string(hostproxyv1.ScopeServe), tokenURL, tlsCfg), hits
}

func readHeader(s grpc.ClientStream) error {
	_, err := s.Header()
	return err
}

func readMsg(s grpc.ClientStream) error { return s.RecvMsg(nil) }

// TestStreamInterceptor_RejectedTokenMintsNew: the CP's Hydra keeps tokens in
// memory, so a restarted CP rejects a cached token as Unauthenticated. The
// next stream must carry a new token, or the host proxy never reconnects.
// Other refusals keep the cached token.
func TestStreamInterceptor_RejectedTokenMintsNew(t *testing.T) {
	tests := []struct {
		name     string
		read     func(grpc.ClientStream) error
		code     codes.Code
		wantHits int32
	}{
		{name: "header unauthenticated", read: readHeader, code: codes.Unauthenticated, wantHits: 2},
		{name: "recv unauthenticated", read: readMsg, code: codes.Unauthenticated, wantHits: 2},
		{name: "header unavailable", read: readHeader, code: codes.Unavailable, wantHits: 1},
		{name: "recv permission denied", read: readMsg, code: codes.PermissionDenied, wantHits: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, hits := newFakeHydraTokenSource(t)
			streamer := func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
				return refusedStream{ClientStream: nil, err: status.Error(tt.code, "refused")}, nil
			}

			stream, err := ts.streamInterceptor()(context.Background(), nil, nil, "/m", streamer)
			require.NoError(t, err)
			require.Equal(t, tt.code, status.Code(tt.read(stream)))
			_, err = ts.streamInterceptor()(context.Background(), nil, nil, "/m", streamer)
			require.NoError(t, err)
			require.Equal(t, tt.wantHits, hits.Load(), "hydra hits")
		})
	}
}

// TestUnaryInterceptor_RejectedTokenMintsNew is the unary twin: the CLI's
// next call after an Unauthenticated reply carries a new token.
func TestUnaryInterceptor_RejectedTokenMintsNew(t *testing.T) {
	tests := []struct {
		name     string
		code     codes.Code
		wantHits int32
	}{
		{name: "unauthenticated", code: codes.Unauthenticated, wantHits: 2},
		{name: "unavailable", code: codes.Unavailable, wantHits: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, hits := newFakeHydraTokenSource(t)
			invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				return status.Error(tt.code, "refused")
			}

			for range 2 {
				err := ts.unaryInterceptor()(context.Background(), "/m", nil, nil, nil, invoker)
				require.Equal(t, tt.code, status.Code(err))
			}
			require.Equal(t, tt.wantHits, hits.Load(), "hydra hits")
		})
	}
}
