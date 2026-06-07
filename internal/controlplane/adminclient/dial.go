// Package adminclient constructs the CLI's gRPC client to the control
// plane's AdminService. It composes auth primitives (mTLS material +
// signed JWT assertions) with CP-specific network topology
// (127.0.0.1:adminPort target, Hydra token endpoint, ServerName).
//
// Auth primitives live in internal/auth — this package owns the wiring
// that turns those primitives into a working AdminServiceClient.
package adminclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

// Dial connects to the CP's gRPC AdminService with mTLS + OAuth2.
//
//  1. Load signing key + CA cert + client cert from auth material
//  2. Build TLS config trusting the CLI CA
//  3. Create a tokenSource that auto-refreshes via Hydra /oauth2/token
//  4. Dial gRPC with mTLS + auto-refreshing bearer token in metadata
//
// Callers may pass additional grpc.DialOption values (e.g. keepalive,
// observability interceptors via grpc.WithChainUnaryInterceptor). The
// auth/TLS baseline is appended last:
//
//   - WithTransportCredentials: single-slot, last-wins — baseline mTLS
//     cannot be disabled by caller intent.
//   - Auth bearer-token interceptor: registered via
//     grpc.WithChainUnaryInterceptor so it composes additively with
//     caller chain interceptors; a caller's own grpc.WithUnaryInterceptor
//     (if any) is prepended by grpc-go as the outermost wrapper.
//
// Do NOT pass grpc.WithUnaryInterceptor — grpc-go stores it in a single
// field with last-wins semantics, so your interceptor will be silently
// dropped (baseline auth wins). Use grpc.WithChainUnaryInterceptor.
//
// log receives the structured clock-skew degrade lines (nil → no-op). The
// clock-skew probe is best-effort; when it can't be measured the assertion
// is minted in the local clock domain, which the log surfaces so an operator
// can tie a later Hydra "Token used before issued" back to a skew-probe
// failure instead of re-debugging the opaque 500.
func Dial(ctx context.Context, adminPort, hydraPort int, log *logger.Logger, opts ...grpc.DialOption) (adminv1.AdminServiceClient, *grpc.ClientConn, error) {
	signingKey, err := auth.LoadSigningKey()
	if err != nil {
		return nil, nil, fmt.Errorf("load signing key: %w", err)
	}

	caCert, err := auth.CACert()
	if err != nil {
		return nil, nil, fmt.Errorf("load CA cert: %w", err)
	}

	clientCert, err := auth.LoadClientCert()
	if err != nil {
		return nil, nil, fmt.Errorf("load client cert: %w", err)
	}

	certPool := x509.NewCertPool()
	certPool.AddCert(caCert)

	// Plain TLS for Hydra token endpoint (no client cert).
	tokenTLSCfg := &tls.Config{
		RootCAs:    certPool,
		ServerName: consts.ContainerCP,
		MinVersion: tls.VersionTLS13,
	}

	// mTLS for gRPC AdminService (presents client cert).
	grpcTLSCfg := mtlsConfig(certPool, clientCert)

	target := fmt.Sprintf("127.0.0.1:%d", adminPort)

	hydraTokenURL := fmt.Sprintf("https://127.0.0.1:%d/oauth2/token", hydraPort)
	// probeSkew measures the host↔CP clock offset over the public
	// GetSystemTime RPC (mTLS, no bearer token) so the token source can
	// mint assertions in Hydra's clock domain. Built as a closure so the
	// tokenSource stays transport-agnostic and unit-testable.
	probeSkew := func(pctx context.Context) (time.Duration, error) {
		return measureClockSkew(pctx, target, grpcTLSCfg)
	}
	ts := newTokenSource(signingKey, hydraTokenURL, tokenTLSCfg, probeSkew, log)

	// Eagerly fetch the first token with bounded retry so dial tolerates
	// concurrent CP bring-up (e.g. one goroutine starts a container while
	// another issues an admin RPC). CP cold-start takes ~5-10s; a 15s
	// window covers that without hanging on truly dead CPs. The clock-skew
	// probe runs lazily inside the same loop (first token attempt), so a
	// transient probe failure during bring-up self-heals on retry rather
	// than poisoning the whole window.
	if err := retryInitialToken(ctx, ts); err != nil {
		return nil, nil, fmt.Errorf("fetch initial access token: %w", err)
	}
	dialOpts := append([]grpc.DialOption{}, opts...)
	dialOpts = append(dialOpts,
		grpc.WithTransportCredentials(credentials.NewTLS(grpcTLSCfg)),
		// WithChainUnaryInterceptor is additive — caller chain
		// interceptors (tracing, metrics, logging) compose cleanly and
		// auth always runs. See doc comment above for WithUnaryInterceptor
		// single-slot caveat. Deadline interceptor runs first so auth
		// token fetch inherits the same deadline as the RPC.
		grpc.WithChainUnaryInterceptor(deadlineInterceptor(rpcDeadline), ts.unaryInterceptor()),
	)
	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("dial cp grpc: %w", err)
	}

	return adminv1.NewAdminServiceClient(conn), conn, nil
}

// mtlsConfig builds the mTLS client config for the gRPC AdminService:
// trusts the CLI CA, presents the CLI client cert, pins ServerName to
// the CP container CN. Shared by Dial and clientGRPCTLSConfig so the
// dial path and the standalone clock-skew probe present identical
// credentials.
func mtlsConfig(pool *x509.CertPool, clientCert tls.Certificate) *tls.Config {
	return &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{clientCert},
		ServerName:   consts.ContainerCP,
		MinVersion:   tls.VersionTLS13,
	}
}

// clientGRPCTLSConfig loads the CLI CA + client cert from auth material
// and returns the mTLS config for dialing the AdminService. Used by
// ProbeClockSkew, which needs the same transport credentials as Dial
// but without the token-exchange machinery.
func clientGRPCTLSConfig() (*tls.Config, error) {
	caCert, err := auth.CACert()
	if err != nil {
		return nil, fmt.Errorf("load CA cert: %w", err)
	}
	clientCert, err := auth.LoadClientCert()
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return mtlsConfig(pool, clientCert), nil
}

// ProbeClockSkew measures the host↔CP clock offset via a single mTLS
// GetSystemTime round trip (the public bootstrap RPC, no bearer token).
// Returned skew is the offset to add to the local clock to obtain the
// CP's clock; positive means CP is ahead of the host. Callers use it to
// gate work that mints Hydra assertions (validated against the CP clock
// with zero leeway) until host and CP are aligned. Bounded internally by
// clockSkewProbeTimeout; respects ctx cancellation/deadline.
func ProbeClockSkew(ctx context.Context, adminPort int) (time.Duration, error) {
	tlsCfg, err := clientGRPCTLSConfig()
	if err != nil {
		return 0, err
	}
	return measureClockSkew(ctx, fmt.Sprintf("127.0.0.1:%d", adminPort), tlsCfg)
}

// initialTokenDeadline bounds the retry window for the first Hydra token
// fetch in Dial. Covers concurrent CP bring-up: a typical cold start
// (image pull + Ory services healthy) lands in ~5-10s; 15s leaves
// headroom without hanging a CLI call on a truly dead CP.
const initialTokenDeadline = 15 * time.Second

// initialTokenRetryInterval is the backoff between retry attempts during
// the initial token fetch. Short enough to catch CP transitioning to
// ready quickly; long enough to avoid hammering a non-existent port.
const initialTokenRetryInterval = 500 * time.Millisecond

// rpcDeadline caps each AdminService RPC. Covers queue-wait + handler
// work (stack restart, BPF reconcile, DNS resolve, etc.) without
// hanging indefinitely when the CP is stuck or the caller forgot a
// ctx deadline. Applied only when the caller hasn't set a tighter
// deadline themselves.
const rpcDeadline = 15 * time.Second

// retryInitialToken calls ts.token with bounded retries so transient
// connection-refused errors (CP mid-bootstrap) don't fail Dial.
// Returns the last error when the deadline expires.
func retryInitialToken(ctx context.Context, ts *tokenSource) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, initialTokenDeadline)
	defer cancel()

	var lastErr error
	for {
		if _, err := ts.token(deadlineCtx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("timed out after %s waiting for CP: %w", initialTokenDeadline, lastErr)
		case <-time.After(initialTokenRetryInterval):
		}
	}
}

// deadlineInterceptor enforces a default per-RPC deadline on client
// calls. If the caller already provided a context with a deadline
// (tighter or otherwise), we respect it; otherwise we apply the
// package default. Ensures every admin RPC is bounded — callers who
// just pass `cmd.Context()` can't accidentally block forever on a
// stalled CP or queue backlog.
func deadlineInterceptor(timeout time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// tokenSource manages an auto-refreshing OAuth2 access token. It caches
// the current token and re-fetches from Hydra when the token is within
// tokenRefreshMargin of expiry. Thread-safe.
type tokenSource struct {
	signingKey *ecdsa.PrivateKey
	tokenURL   string
	tlsCfg     *tls.Config
	// probeSkew measures the offset to add to the local clock to obtain the
	// CP's clock (nil → skew stays 0, e.g. in unit tests). Called lazily on
	// the first token fetch and cached once it succeeds.
	probeSkew func(context.Context) (time.Duration, error)
	log       *logger.Logger

	mu        sync.Mutex
	cached    string
	expiresAt time.Time
	skew      time.Duration
	skewKnown bool
}

// maxPlausibleClockSkew bounds a trusted GetSystemTime measurement. Real
// host↔CP drift (a Docker Desktop VM clock lagging after the host sleeps) is
// seconds to a few minutes; a measured offset beyond this signals a garbage
// or hostile clock reading we must not anchor assertions to — a bad iat would
// poison every mint for this token source's lifetime once skewKnown latches.
// Past the bound the measurement is discarded and minting falls back to the
// local clock plus the residual leeway floor.
const maxPlausibleClockSkew = 24 * time.Hour

// notableClockSkew is the offset above which an *accepted* (plausible)
// measurement gets a breadcrumb. A few seconds of VM lag is routine and the
// expected correction; a larger anchor — say minutes from a misconfigured NTP
// rather than mere sleep lag — is exactly the case where a wrong-but-plausible
// GetSystemTime reading would silently skew every subsequent mint. Logging the
// applied value here gives an operator the one thread to pull when assertions
// start carrying a surprising iat, without spamming the routine small-skew case.
const notableClockSkew = 5 * time.Second

// tokenRefreshMargin is how far before expiry we proactively refresh.
// Hydra's default access token TTL is 1 hour; refreshing 30s early
// ensures long-running operations (bypass timers) never hit an expired
// token mid-flight.
const tokenRefreshMargin = 30 * time.Second

// clockSkewProbeTimeout caps a single GetSystemTime probe at a hard upper
// bound while still honoring the caller's context cancellation/deadline.
// token() can re-enter the probe from an interceptor on any admin RPC
// (until skewKnown latches), so without this cap the probe could inherit
// that RPC's arbitrary (or absent) deadline and hang. A skew measurement
// is one fast local round trip to the CP; if it can't answer in this window
// the probe degrades to the logged clock_skew_probe_unavailable path and
// the next refresh retries.
const clockSkewProbeTimeout = 5 * time.Second

func newTokenSource(signingKey *ecdsa.PrivateKey, tokenURL string, tlsCfg *tls.Config, probeSkew func(context.Context) (time.Duration, error), log *logger.Logger) *tokenSource {
	if log == nil {
		log = logger.Nop()
	}
	return &tokenSource{
		signingKey: signingKey,
		tokenURL:   tokenURL,
		tlsCfg:     tlsCfg,
		probeSkew:  probeSkew,
		log:        log,
	}
}

// token returns a valid access token, refreshing from Hydra if the cached
// token is expired or about to expire.
func (ts *tokenSource) token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.cached != "" && time.Now().Before(ts.expiresAt.Add(-tokenRefreshMargin)) {
		return ts.cached, nil
	}

	// Establish the CP clock offset before the first mint. Best-effort: a
	// failed or implausible probe leaves skewKnown false so the next token
	// attempt retries it (the residual leeway floor in BuildSignedAssertion
	// still applies). Both degrade paths are logged: when the probe never
	// succeeds the mint silently falls back to the local clock domain, which
	// re-exposes the host↔CP drift this measurement exists to absorb — without
	// a breadcrumb a later Hydra "Token used before issued" 500 looks identical
	// to the bug this fix removes.
	if !ts.skewKnown && ts.probeSkew != nil {
		switch s, err := ts.probeSkew(ctx); {
		case err != nil:
			ts.log.Warn().Err(err).
				Str("event", "clock_skew_probe_unavailable").
				Str("component", "adminclient").
				Msg("CP clock-skew probe failed; minting assertion in local clock domain (Hydra may reject iat under host↔CP drift)")
		case absDuration(s) > maxPlausibleClockSkew:
			ts.log.Warn().
				Str("event", "clock_skew_implausible").
				Str("component", "adminclient").
				Dur("measured_skew", s).
				Msg("CP clock-skew measurement beyond plausible bound; discarding and minting in local clock domain")
		default:
			ts.skew = s
			ts.skewKnown = true
			if absDuration(s) > notableClockSkew {
				ts.log.Warn().
					Str("event", "clock_skew_applied").
					Str("component", "adminclient").
					Dur("applied_skew", s).
					Msg("anchored assertions to a large host↔CP clock correction; if this is unexpected, verify the CP clock (NTP) rather than mere VM sleep lag")
			}
		}
	}

	tok, expiresIn, err := fetchAccessToken(ctx, ts.signingKey, ts.tokenURL, ts.tlsCfg, ts.skew)
	if err != nil {
		return "", err
	}
	ts.cached = tok
	ts.expiresAt = time.Now().Add(expiresIn)
	return tok, nil
}

// unaryInterceptor returns a gRPC interceptor that fetches a fresh token
// on every call (cache-hit is fast, only re-fetches when near expiry).
func (ts *tokenSource) unaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		tok, err := ts.token(ctx)
		if err != nil {
			return fmt.Errorf("refreshing access token for %s: %w", method, err)
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tok)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// measureClockSkew dials a short-lived mTLS connection (no bearer token —
// GetSystemTime is the public bootstrap RPC) and returns the offset to add
// to the local clock to obtain the CP's clock:
//
//	skew = cpNow - localMidpoint
//
// localMidpoint is the midpoint of the request window so round-trip latency
// is discounted symmetrically. clawker-cp and Hydra share the container, so
// this offset aligns the minted assertion's iat to the exact clock fosite
// validates against — eliminating host↔CP drift (e.g. a Docker Desktop VM
// lagging after the host sleeps) rather than guessing a fixed margin.
func measureClockSkew(ctx context.Context, target string, tlsCfg *tls.Config) (time.Duration, error) {
	// Bound the probe on its own deadline so its failure mode doesn't depend
	// on the caller RPC that triggered this refresh (see clockSkewProbeTimeout).
	ctx, cancel := context.WithTimeout(ctx, clockSkewProbeTimeout)
	defer cancel()

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return 0, fmt.Errorf("dial cp for clock skew: %w", err)
	}
	defer conn.Close()

	client := adminv1.NewAdminServiceClient(conn)
	t0 := time.Now()
	resp, err := client.GetSystemTime(ctx, &adminv1.GetSystemTimeRequest{})
	if err != nil {
		return 0, fmt.Errorf("get cp system time: %w", err)
	}
	return clockSkew(t0, time.Now(), resp.GetUnixNanos()), nil
}

// clockSkew computes the offset to add to the local clock to obtain the
// CP's clock from a single GetSystemTime round trip: t0/t1 bracket the
// call locally and cpUnixNanos is the CP's reported time. The local
// reference is the request-window midpoint so symmetric round-trip latency
// cancels out.
func clockSkew(t0, t1 time.Time, cpUnixNanos int64) time.Duration {
	localMid := t0.Add(t1.Sub(t0) / 2)
	return time.Unix(0, cpUnixNanos).Sub(localMid)
}

// absDuration returns the magnitude of d, used to bound a measured clock
// skew regardless of drift direction (CP ahead of or behind the host).
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// fetchAccessToken signs a JWT assertion and exchanges it at Hydra's
// /oauth2/token endpoint for an access token. Returns the token and its
// TTL (from expires_in, defaulting to 1 hour if absent).
func fetchAccessToken(ctx context.Context, signingKey *ecdsa.PrivateKey, tokenURL string, tlsCfg *tls.Config, skew time.Duration) (string, time.Duration, error) {
	assertion, err := auth.BuildSignedAssertion(auth.AssertionClaims{
		Issuer:           consts.ClientIDCLI,
		Subject:          consts.ClientIDCLI,
		Audience:         tokenURL,
		JWTID:            uuid.NewString(),
		ExpiresInSeconds: 30,
		// Mint in the CP's clock domain: local now + measured offset. With
		// skew==0 (probe unavailable) this is plain local now, still backed
		// by the residual leeway floor in BuildSignedAssertion.
		Now: time.Now().Add(skew),
	}, signingKey)
	if err != nil {
		return "", 0, fmt.Errorf("build assertion: %w", err)
	}

	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
		"scope":                 {consts.ScopeAdmin},
	}

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("hydra token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("hydra returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", 0, fmt.Errorf("parse response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", 0, fmt.Errorf("hydra returned empty access_token")
	}

	ttl := time.Duration(tokenResp.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour // Hydra default
	}
	return tokenResp.AccessToken, ttl, nil
}
