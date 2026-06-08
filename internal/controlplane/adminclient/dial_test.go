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
	var nextSkew time.Duration
	var nextErr error
	probe := func(context.Context) (time.Duration, error) {
		probeCalls++
		return nextSkew, nextErr
	}
	ts := newTokenSource(key, url, tlsCfg, probe)

	forceRefresh := func() { ts.expiresAt = time.Now().Add(-time.Hour) }

	// 1. Clock out of tolerance for the whole window: token must FAIL fast
	// (no token minted) and `synced` must NOT latch — the next refresh retries
	// the wait rather than assume convergence.
	nextSkew = 10 * time.Second // > clockSyncTolerance
	if tok, err := ts.token(context.Background()); err == nil || tok != "" {
		t.Fatalf("token under non-convergence = (%q, %v), want (\"\", error)", tok, err)
	}
	if ts.synced {
		t.Fatal("synced must NOT latch when the clock never converged")
	}
	if probeCalls < 2 {
		t.Fatalf("probeCalls = %d, want >=2 (the wait must poll, not give up after one probe)", probeCalls)
	}

	// 2. Clock now within tolerance: the wait returns on the first in-tolerance
	// probe and `synced` latches.
	forceRefresh()
	callsBefore := probeCalls
	nextSkew = clockSyncTolerance - time.Millisecond
	if _, err := ts.token(context.Background()); err != nil {
		t.Fatalf("token: %v", err)
	}
	if !ts.synced {
		t.Fatal("synced must latch once the clock converged within tolerance")
	}
	if probeCalls != callsBefore+1 {
		t.Fatalf("probeCalls = %d, want %d (one in-tolerance probe ends the wait)", probeCalls, callsBefore+1)
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

	probe := func(ctx context.Context) (time.Duration, error) {
		return time.Hour, nil // perpetually out of tolerance
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

// TestClockSkew verifies the offset math the clock-sync wait uses to decide
// whether the CP clock has converged with the host. The local reference is the
// request-window midpoint so symmetric round-trip latency cancels out.
func TestClockSkew(t *testing.T) {
	// Fixed local window: t0 .. t1, midpoint = t0 + 1ms.
	t0 := time.Unix(1_000_000, 0)
	t1 := t0.Add(2 * time.Millisecond)
	mid := t0.Add(1 * time.Millisecond)

	for _, tc := range []struct {
		name string
		off  time.Duration // CP clock relative to local midpoint
	}{
		{"cp ahead 90s", 90 * time.Second},
		{"cp behind 90s", -90 * time.Second},
		{"cp aligned", 0},
		{"cp behind 5m (post-sleep drift)", -5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cpUnixNanos := mid.Add(tc.off).UnixNano()
			got := clockSkew(t0, t1, cpUnixNanos)
			// skew is what we add to the local clock to get CP time, so it
			// equals the CP-vs-midpoint offset exactly.
			if got != tc.off {
				t.Fatalf("clockSkew = %s, want %s", got, tc.off)
			}
		})
	}
}

// TestClockSkew_DiscountsLatency confirms the midpoint reference makes the
// skew independent of round-trip duration: the same true offset yields the
// same skew whether the call took 1ms or 1s.
func TestClockSkew_DiscountsLatency(t *testing.T) {
	base := time.Unix(2_000_000, 0)
	const trueOffset = 42 * time.Second

	skewFor := func(rtt time.Duration) time.Duration {
		t0 := base
		t1 := base.Add(rtt)
		mid := t0.Add(rtt / 2)
		return clockSkew(t0, t1, mid.Add(trueOffset).UnixNano())
	}

	fast := skewFor(1 * time.Millisecond)
	slow := skewFor(1 * time.Second)
	if fast != trueOffset || slow != trueOffset {
		t.Fatalf("skew should be latency-independent: fast=%s slow=%s want=%s", fast, slow, trueOffset)
	}
}
