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
	grpcTLSCfg := &tls.Config{
		RootCAs:      certPool,
		Certificates: []tls.Certificate{clientCert},
		ServerName:   consts.ContainerCP,
		MinVersion:   tls.VersionTLS13,
	}

	hydraTokenURL := fmt.Sprintf("https://127.0.0.1:%d/oauth2/token", hydraPort)
	ts := newTokenSource(signingKey, hydraTokenURL, tokenTLSCfg)

	// Eagerly fetch the first token with bounded retry so dial tolerates
	// concurrent CP bring-up (e.g. one goroutine starts a container while
	// another issues an admin RPC). CP cold-start takes ~5-10s; a 15s
	// window covers that without hanging on truly dead CPs.
	if err := retryInitialToken(ctx, ts); err != nil {
		return nil, nil, fmt.Errorf("fetch initial access token: %w", err)
	}

	target := fmt.Sprintf("127.0.0.1:%d", adminPort)
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

	mu        sync.Mutex
	cached    string
	expiresAt time.Time
}

// tokenRefreshMargin is how far before expiry we proactively refresh.
// Hydra's default access token TTL is 1 hour; refreshing 30s early
// ensures long-running operations (bypass timers) never hit an expired
// token mid-flight.
const tokenRefreshMargin = 30 * time.Second

func newTokenSource(signingKey *ecdsa.PrivateKey, tokenURL string, tlsCfg *tls.Config) *tokenSource {
	return &tokenSource{
		signingKey: signingKey,
		tokenURL:   tokenURL,
		tlsCfg:     tlsCfg,
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

	tok, expiresIn, err := fetchAccessToken(ctx, ts.signingKey, ts.tokenURL, ts.tlsCfg)
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
