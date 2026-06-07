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

// TestTokenSource_SkewProbeRetryAndCache covers the stateful probe branch in
// tokenSource.token: a failed probe must not block the token fetch and must
// be retried on the next refresh; a successful probe caches the skew and is
// not re-run; an implausible measurement is discarded (not cached). The pure
// clockSkew math is covered separately — this exercises the integration the
// "self-heals on retry" comment promises, which no E2E would surface (it would
// just silently mint with skew=0).
func TestTokenSource_SkewProbeRetryAndCache(t *testing.T) {
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
	ts := newTokenSource(key, url, tlsCfg, probe, nil)

	forceRefresh := func() { ts.expiresAt = time.Now().Add(-time.Hour) }

	// 1. Probe fails: token still fetched, skew not cached.
	nextErr = errors.New("cp not ready")
	if tok, err := ts.token(context.Background()); err != nil || tok != "tok" {
		t.Fatalf("token on probe failure = (%q, %v), want (\"tok\", nil)", tok, err)
	}
	if ts.skewKnown {
		t.Fatal("skew must not be cached after a failed probe")
	}
	if probeCalls != 1 {
		t.Fatalf("probeCalls = %d, want 1", probeCalls)
	}

	// 2. Implausible positive measurement (CP far ahead): discarded, still not
	// cached, probe re-run.
	forceRefresh()
	nextErr = nil
	nextSkew = maxPlausibleClockSkew + time.Hour
	if _, err := ts.token(context.Background()); err != nil {
		t.Fatalf("token: %v", err)
	}
	if ts.skewKnown {
		t.Fatal("implausible positive skew must be discarded, not cached")
	}
	if probeCalls != 2 {
		t.Fatalf("probeCalls = %d, want 2 (retried after discard)", probeCalls)
	}

	// 3. Implausible NEGATIVE measurement (CP far behind — the post-sleep
	// Docker-VM-lag direction): also discarded. Guards the absDuration bound
	// so a regression to a raw `s > max` comparison can't slip negative drift
	// through.
	forceRefresh()
	nextSkew = -(maxPlausibleClockSkew + time.Hour)
	if _, err := ts.token(context.Background()); err != nil {
		t.Fatalf("token: %v", err)
	}
	if ts.skewKnown {
		t.Fatal("implausible negative skew must be discarded, not cached")
	}
	if probeCalls != 3 {
		t.Fatalf("probeCalls = %d, want 3 (retried after discard)", probeCalls)
	}

	// 4. Plausible measurement: cached.
	forceRefresh()
	nextSkew = 7 * time.Second
	if _, err := ts.token(context.Background()); err != nil {
		t.Fatalf("token: %v", err)
	}
	if !ts.skewKnown || ts.skew != 7*time.Second {
		t.Fatalf("skew = (%s, known=%v), want (7s, true)", ts.skew, ts.skewKnown)
	}
	if probeCalls != 4 {
		t.Fatalf("probeCalls = %d, want 4", probeCalls)
	}

	// 5. Once known, the probe is not re-run on subsequent refreshes.
	forceRefresh()
	if _, err := ts.token(context.Background()); err != nil {
		t.Fatalf("token: %v", err)
	}
	if probeCalls != 4 {
		t.Fatalf("probeCalls = %d, want 4 (skewKnown short-circuits the probe)", probeCalls)
	}
}

// TestClockSkew verifies the offset math used to align the CLI's minted
// assertion to the CP's clock. The local reference is the request-window
// midpoint so symmetric round-trip latency cancels out.
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
