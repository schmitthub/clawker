package auth

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
	"github.com/schmitthub/clawker/internal/consts"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

// DialCPAdmin connects to the CP's gRPC AdminService with TLS + OAuth2.
//
//  1. Load signing key + server cert from dataDir
//  2. Build TLS config trusting the CLI CA certificate
//  3. Create a tokenSource that auto-refreshes via Hydra /oauth2/token
//  4. Dial gRPC with TLS + auto-refreshing bearer token in metadata
func DialCPAdmin(ctx context.Context, adminPort, hydraPort int) (adminv1.AdminServiceClient, *grpc.ClientConn, error) {
	signingKey, err := LoadSigningKey()
	if err != nil {
		return nil, nil, fmt.Errorf("load signing key: %w", err)
	}

	caCert, err := CACert()
	if err != nil {
		return nil, nil, fmt.Errorf("load CA cert: %w", err)
	}

	clientCert, err := LoadClientCert()
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

	// Eagerly fetch the first token so dial-time errors surface immediately.
	if _, err := ts.token(ctx); err != nil {
		return nil, nil, fmt.Errorf("fetch initial access token: %w", err)
	}

	target := fmt.Sprintf("127.0.0.1:%d", adminPort)
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(credentials.NewTLS(grpcTLSCfg)),
		grpc.WithUnaryInterceptor(ts.unaryInterceptor()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("dial cp grpc: %w", err)
	}

	return adminv1.NewAdminServiceClient(conn), conn, nil
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
	assertion, err := BuildSignedAssertion(AssertionClaims{
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
