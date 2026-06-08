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
// The pre-mint clock-sync wait fails fast with a wrapped error on non-
// convergence, so a lagging CP clock surfaces directly to the caller as a
// "waiting for CP clock sync" error instead of an opaque later Hydra "Token
// used before issued" 500 — no logger is needed on this path.
func Dial(ctx context.Context, adminPort, hydraPort int, opts ...grpc.DialOption) (adminv1.AdminServiceClient, *grpc.ClientConn, error) {
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
	// probe returns the CP's current wall-clock time over the public
	// GetSystemTime RPC (mTLS, no bearer token). The token source loops it
	// to *wait* until the CP clock has caught up to the host before minting.
	// Built as a closure so the tokenSource stays transport-agnostic and
	// unit-testable.
	probe := func(pctx context.Context) (time.Time, error) {
		return probeCPTime(pctx, target, grpcTLSCfg)
	}
	ts := newTokenSource(signingKey, hydraTokenURL, tokenTLSCfg, probe)

	// Eagerly fetch the first token with bounded retry so dial tolerates
	// concurrent CP bring-up (e.g. one goroutine starts a container while
	// another issues an admin RPC). CP cold-start takes ~5-10s; a 15s
	// window covers that without hanging on truly dead CPs. The clock-sync
	// wait runs lazily inside the same loop (first token attempt), so a
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
// dial path and the standalone CP-time probe present identical
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
// ProbeCPTime, which needs the same transport credentials as Dial
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

// ProbeCPTime returns the control plane's current wall-clock time via a
// single mTLS GetSystemTime round trip (the public bootstrap RPC, no
// bearer token). Callers gate assertion minting on it: an assertion's iat
// is in the host clock, and Hydra/fosite validates iat against the CP
// clock with zero leeway, so a caller waits until the CP clock is no
// longer behind the host before minting. Bounded internally by
// cpTimeProbeTimeout; respects ctx cancellation/deadline.
func ProbeCPTime(ctx context.Context, adminPort int) (time.Time, error) {
	tlsCfg, err := clientGRPCTLSConfig()
	if err != nil {
		return time.Time{}, err
	}
	return probeCPTime(ctx, fmt.Sprintf("127.0.0.1:%d", adminPort), tlsCfg)
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
	// probe returns the CP's current wall-clock time (nil → wait is skipped,
	// e.g. in unit tests). Looped by waitForSync before the first mint to
	// wait until the CP clock has caught up to the host; never used to shift
	// iat.
	probe func(context.Context) (time.Time, error)

	mu        sync.Mutex
	cached    string
	expiresAt time.Time
	// synced latches once the CP clock has been observed within
	// clockSyncTolerance of the host, so the wait runs once per source and
	// every subsequent refresh mints immediately.
	synced bool
}

// clockSyncWaitTimeout is a defensive backstop on how long the first mint
// waits for the CP clock to converge before failing. On the eager-dial path
// the caller ctx (initialTokenDeadline, 15s) bounds the wait and fires first;
// this matches it so a caller without a deadline still can't hang. A just-woken
// Docker Desktop VM clock reconverges in seconds — if it has not within this
// window the CLI throws (no degrade, no best-effort mint: a mint under drift
// would only earn the "token used before issued" 500 the wait exists to avoid).
// Var (not const) so tests can shrink the wait — the only seam, never
// reassigned in production.
var clockSyncWaitTimeout = initialTokenDeadline

// clockSyncInterval paces the convergence poll between probes. Var for the
// same test-shrinking reason as clockSyncWaitTimeout.
var clockSyncInterval = 500 * time.Millisecond

// tokenRefreshMargin is how far before expiry we proactively refresh.
// Hydra's default access token TTL is 1 hour; refreshing 30s early
// ensures long-running operations (bypass timers) never hit an expired
// token mid-flight.
const tokenRefreshMargin = 30 * time.Second

// cpTimeProbeTimeout caps a single GetSystemTime probe at a hard upper
// bound while still honoring the caller's context cancellation/deadline.
// waitForSync can re-enter the probe from an interceptor on any admin RPC
// (until synced latches), so without this cap the probe could inherit that
// RPC's arbitrary (or absent) deadline and hang. A CP-time probe is one
// fast local round trip to the CP.
const cpTimeProbeTimeout = 5 * time.Second

func newTokenSource(signingKey *ecdsa.PrivateKey, tokenURL string, tlsCfg *tls.Config, probe func(context.Context) (time.Time, error)) *tokenSource {
	return &tokenSource{
		signingKey: signingKey,
		tokenURL:   tokenURL,
		tlsCfg:     tlsCfg,
		probe:      probe,
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

	// Wait for the CP clock to converge with the host before the first mint.
	// The host clock is the source of truth (Docker forces the CP/VM clock to
	// track it); Hydra/fosite validates the assertion iat against the CP clock
	// with zero leeway, so we WAIT for reconvergence rather than shifting iat.
	// Minting while the clock is still drifted would just earn the "token used
	// before issued" 500 the wait exists to avoid, so non-convergence (or a
	// cancelled caller ctx) fails fast with a clear cause instead. synced
	// latches only on success, so a transient failure (CP cold-starting) self-
	// heals on the next refresh.
	if !ts.synced {
		if err := ts.waitForSync(ctx); err != nil {
			return "", fmt.Errorf("waiting for CP clock sync: %w", err)
		}
		ts.synced = true
	}

	tok, expiresIn, err := fetchAccessToken(ctx, ts.signingKey, ts.tokenURL, ts.tlsCfg)
	if err != nil {
		return "", err
	}
	ts.cached = tok
	ts.expiresAt = time.Now().Add(expiresIn)
	return tok, nil
}

// waitForSync polls the CP clock until it is no longer behind the host (the
// CP's wall-clock at or after the host's now) or clockSyncWaitTimeout
// elapses. A probe error (CP still cold-starting) or a CP clock still behind
// the host (VM clock lagging post-sleep) both keep the loop going, so one
// wait covers both bring-up and reconvergence. Returns nil once the CP has
// caught up; the caller's ctx cancellation/deadline takes precedence and
// surfaces its own cause.
func (ts *tokenSource) waitForSync(ctx context.Context) error {
	if ts.probe == nil {
		return nil // no transport (unit tests) — nothing to wait on
	}
	deadline := time.Now().Add(clockSyncWaitTimeout)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cpTime, err := ts.probe(ctx)
		// Both sides compared in UTC: cpTime is UTC (Timestamp.AsTime), and the
		// host's local TZ is normalized away by .UTC() so the comparison is a
		// pure instant comparison regardless of where the host is configured.
		if err == nil && !time.Now().UTC().After(cpTime) {
			return nil // CP clock has caught up to the host
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("CP clock still behind host (CP at %s)", cpTime.UTC().Format(time.RFC3339Nano))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("CP clock did not catch up to host within %s: %w", clockSyncWaitTimeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(clockSyncInterval):
		}
	}
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

// probeCPTime dials a short-lived mTLS connection (no bearer token —
// GetSystemTime is the public bootstrap RPC) and returns the CP's current
// wall-clock time as a UTC instant (the int64 nanos parsed and normalized to
// UTC). The caller compares it against its own time.Now().UTC() to decide
// whether the CP has caught up to the host; the response-leg latency is
// already elapsed by the time the caller reads its own now, so the
// comparison errs toward waiting, never toward an early mint.
func probeCPTime(ctx context.Context, target string, tlsCfg *tls.Config) (time.Time, error) {
	// Bound the probe on its own deadline so its failure mode doesn't depend
	// on the caller RPC that triggered this refresh (see cpTimeProbeTimeout).
	ctx, cancel := context.WithTimeout(ctx, cpTimeProbeTimeout)
	defer cancel()

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return time.Time{}, fmt.Errorf("dial cp for system time: %w", err)
	}
	defer conn.Close()

	client := adminv1.NewAdminServiceClient(conn)
	resp, err := client.GetSystemTime(ctx, &adminv1.GetSystemTimeRequest{})
	if err != nil {
		return time.Time{}, fmt.Errorf("get cp system time: %w", err)
	}
	return time.Unix(0, resp.GetUnixNanos()).UTC(), nil
}

// fetchAccessToken signs a JWT assertion and exchanges it at Hydra's
// /oauth2/token endpoint for an access token. Returns the token and its
// TTL (from expires_in, defaulting to 1 hour if absent).
func fetchAccessToken(ctx context.Context, signingKey *ecdsa.PrivateKey, tokenURL string, tlsCfg *tls.Config) (string, time.Duration, error) {
	assertion, err := auth.BuildSignedAssertion(auth.AssertionClaims{
		Issuer:           consts.ClientIDCLI,
		Subject:          consts.ClientIDCLI,
		Audience:         tokenURL,
		JWTID:            uuid.NewString(),
		ExpiresInSeconds: 30,
		// Minted in the host clock (Now unset → time.Now()). The host clock is
		// the source of truth; token() has already waited until the CP clock
		// caught up to the host before reaching here, so this host-clock iat
		// is in the CP's past.
	}, signingKey)
	if err != nil {
		return "", 0, fmt.Errorf("build assertion: %w", err)
	}

	adminScope := string(adminv1.ScopeAdmin)

	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
		"scope":                 {adminScope},
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
