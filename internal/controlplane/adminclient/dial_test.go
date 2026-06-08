package adminclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeHydra returns a TLS test server that answers any /oauth2/token POST
// with a valid token JSON, plus a tls.Config trusting its cert. It stands in
// for Hydra so tokenSource.token can run end-to-end without a real CP.
func fakeHydra(t *testing.T) (url string, tlsCfg *tls.Config) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv.URL, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

// TestTokenSource_WaitsForClockSync covers the wait-before-mint branch in
// tokenSource.token: the mint waits for the CP clock to converge with the host
// (host is the source of truth — we wait, never shift iat), latches `synced`
// once converged so later refreshes skip the wait, and on non-convergence
// fails fast (leaving `synced` false so the next refresh retries). This is the
// integration no E2E would surface — a wrong wait would silently mint under
// drift and earn a later Hydra 500.
func TestTokenSource_WaitsForClockSync(t *testing.T) {
	// Shrink the wait so the timeout branch runs in ms, not the full 15s.
	origTimeout, origInterval := clockSyncWaitTimeout, clockSyncInterval
	clockSyncWaitTimeout, clockSyncInterval = 60*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { clockSyncWaitTimeout, clockSyncInterval = origTimeout, origInterval })

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	url, tlsCfg := fakeHydra(t)

	var probeCalls int
	var nextCPTime time.Time
	var nextErr error
	probe := func(context.Context) (time.Time, error) {
		probeCalls++
		return nextCPTime, nextErr
	}
	ts := newTokenSource(key, url, tlsCfg, probe)

	forceRefresh := func() { ts.expiresAt = time.Now().UTC().Add(-time.Hour) }

	// 1. CP clock behind the host for the whole window: token must FAIL fast
	// (no token minted) and `synced` must NOT latch — the next refresh retries
	// the wait rather than assume convergence.
	nextCPTime = time.Now().UTC().Add(-10 * time.Second) // CP still behind host
	if tok, err := ts.token(context.Background()); err == nil || tok != "" {
		t.Fatalf("token under non-convergence = (%q, %v), want (\"\", error)", tok, err)
	}
	if ts.synced {
		t.Fatal("synced must NOT latch when the CP clock never caught up")
	}
	if probeCalls < 2 {
		t.Fatalf("probeCalls = %d, want >=2 (the wait must poll, not give up after one probe)", probeCalls)
	}

	// 2. CP clock now at/ahead of the host: the wait returns on the first
	// caught-up probe and `synced` latches.
	forceRefresh()
	callsBefore := probeCalls
	nextCPTime = time.Now().UTC().Add(time.Second) // CP has caught up to host
	if _, err := ts.token(context.Background()); err != nil {
		t.Fatalf("token: %v", err)
	}
	if !ts.synced {
		t.Fatal("synced must latch once the CP clock caught up to the host")
	}
	if probeCalls != callsBefore+1 {
		t.Fatalf("probeCalls = %d, want %d (one caught-up probe ends the wait)", probeCalls, callsBefore+1)
	}

	// 3. Once synced, the probe is not re-run on subsequent refreshes.
	forceRefresh()
	callsBefore = probeCalls
	if _, err := ts.token(context.Background()); err != nil {
		t.Fatalf("token: %v", err)
	}
	if probeCalls != callsBefore {
		t.Fatalf("probeCalls = %d, want %d (synced short-circuits the wait)", probeCalls, callsBefore)
	}
}

// TestTokenSource_WaitRespectsContext proves a caller's cancelled context
// surfaces its own cause instead of degrading to a mint — a wedged clock must
// not swallow the caller's deadline.
func TestTokenSource_WaitRespectsContext(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	url, tlsCfg := fakeHydra(t)

	probe := func(ctx context.Context) (time.Time, error) {
		return time.Now().UTC().Add(-time.Hour), nil // CP perpetually behind host
	}
	ts := newTokenSource(key, url, tlsCfg, probe)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := ts.token(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("token err = %v, want context.DeadlineExceeded", err)
	}
	if ts.synced {
		t.Fatal("synced must not latch when the wait was cancelled")
	}
}
