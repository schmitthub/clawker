package controlplane

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/schmitthub/clawker/internal/logger"
)

// IntrospectionResult represents the relevant fields from an OAuth2
// token introspection response (RFC 7662).
type IntrospectionResult struct {
	Active   bool   `json:"active"`
	Scope    string `json:"scope"`
	ClientID string `json:"client_id"`
	Sub      string `json:"sub"`
	Exp      int64  `json:"exp"`
}

// Introspector validates an OAuth2 bearer token and returns its
// introspection result. Implementations must be safe for concurrent use.
//
//go:generate moq -out mocks/introspector_mock.go -pkg mocks . Introspector
type Introspector interface {
	Introspect(ctx context.Context, token, requiredScope string) (*IntrospectionResult, error)
}

// HydraIntrospector implements Introspector by calling Hydra's admin
// introspection endpoint (POST /admin/oauth2/introspect, RFC 7662).
type HydraIntrospector struct {
	url    string
	client *http.Client
}

// NewHydraIntrospector creates an introspector that validates tokens
// against the given Hydra admin introspection URL. The optional TLS
// config is used when Hydra serves HTTPS (self-signed cert).
func NewHydraIntrospector(introspectURL string, tlsCfg *tls.Config) *HydraIntrospector {
	var transport *http.Transport
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = dt.Clone()
	} else {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		}
	}
	if tlsCfg != nil {
		transport.TLSClientConfig = tlsCfg
		transport.ForceAttemptHTTP2 = true
	}
	return &HydraIntrospector{
		url:    introspectURL,
		client: &http.Client{Timeout: 5 * time.Second, Transport: transport},
	}
}

func (h *HydraIntrospector) Introspect(ctx context.Context, token, requiredScope string) (*IntrospectionResult, error) {
	form := url.Values{
		"token": {token},
		"scope": {requiredScope},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build introspection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("introspection request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		hint, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if err != nil {
			return nil, fmt.Errorf("introspection returned %d (body read failed: %w)", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("introspection returned %d: %s", resp.StatusCode, hint)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read introspection response: %w", err)
	}

	var result IntrospectionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode introspection response: %w", err)
	}

	return &result, nil
}

// AuthInterceptor validates bearer tokens via an Introspector and
// enforces per-method scope requirements.
//
// Flow: extract bearer token from gRPC metadata → introspect →
// check active + scope → allow or deny. Fail-closed on any error.
type AuthInterceptor struct {
	introspector Introspector
	methodScopes map[string]string // gRPC full method → required scope
	log          *logger.Logger
}

// NewAuthInterceptor creates an interceptor that validates tokens via
// the given Introspector. methodScopes maps gRPC method names
// (e.g. "/clawker.admin.v1.AdminService/Install") to required OAuth2
// scopes (e.g. "admin").
func NewAuthInterceptor(introspector Introspector, methodScopes map[string]string, log *logger.Logger) *AuthInterceptor {
	return &AuthInterceptor{
		introspector: introspector,
		methodScopes: methodScopes,
		log:          log,
	}
}

// UnaryInterceptor returns a gRPC unary server interceptor.
func (a *AuthInterceptor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := a.authorize(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor returns a gRPC stream server interceptor.
func (a *AuthInterceptor) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := a.authorize(stream.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, stream)
	}
}

// GRPCServerOptions returns gRPC server options wired with both interceptors.
func (a *AuthInterceptor) GRPCServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.UnaryInterceptor(a.UnaryInterceptor()),
		grpc.StreamInterceptor(a.StreamInterceptor()),
	}
}

func (a *AuthInterceptor) authorize(ctx context.Context, fullMethod string) error {
	requiredScope, ok := a.methodScopes[fullMethod]
	if !ok {
		a.log.Warn().Str("method", fullMethod).Msg("authz: unmapped method denied")
		return status.Error(codes.Unauthenticated, "unauthorized")
	}

	// Empty scope means the method is public (e.g. Health).
	if requiredScope == "" {
		return nil
	}

	token, err := extractBearerToken(ctx)
	if err != nil {
		a.log.Debug().Str("method", fullMethod).Msg("authz: no bearer token")
		return status.Error(codes.Unauthenticated, "missing bearer token")
	}

	result, err := a.introspector.Introspect(ctx, token, requiredScope)
	if err != nil {
		a.log.Warn().Err(err).Str("method", fullMethod).Msg("authz: introspection failed")
		return status.Error(codes.Unauthenticated, "token validation failed")
	}

	if !result.Active {
		a.log.Debug().Str("method", fullMethod).Msg("authz: token inactive")
		return status.Error(codes.Unauthenticated, "token inactive or invalid")
	}

	if !hasScope(result.Scope, requiredScope) {
		a.log.Warn().
			Str("method", fullMethod).
			Str("required_scope", requiredScope).
			Str("token_scope", result.Scope).
			Msg("authz: token missing required scope")
		return status.Errorf(codes.PermissionDenied, "token missing required scope %q", requiredScope)
	}

	a.log.Debug().
		Str("method", fullMethod).
		Str("client_id", result.ClientID).
		Str("scope", result.Scope).
		Msg("authz: access granted")
	return nil
}

// extractBearerToken pulls the bearer token from gRPC metadata.
func extractBearerToken(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", fmt.Errorf("no metadata")
	}

	vals := md.Get("authorization")
	if len(vals) == 0 {
		return "", fmt.Errorf("no authorization header")
	}

	auth := vals[0]
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", fmt.Errorf("not a bearer token")
	}

	return strings.TrimPrefix(auth, prefix), nil
}

// hasScope checks whether the space-delimited scope string (RFC 7662)
// contains the exact required scope.
func hasScope(scopeStr, required string) bool {
	for _, s := range strings.Fields(scopeStr) {
		if s == required {
			return true
		}
	}
	return false
}
