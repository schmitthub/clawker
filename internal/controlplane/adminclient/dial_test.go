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
	"sync/atomic"
	"testing"
	"time"
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
	ts := newTokenSource(key, url, tlsCfg)

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
