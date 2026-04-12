package controlplane

import (
	"context"
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

// AuthInterceptor validates bearer tokens against Hydra's introspection
// endpoint (RFC 7662) and enforces per-method scope requirements.
//
// Flow: extract bearer token from gRPC metadata → POST to Hydra introspection
// → check active + scope → allow or deny. Fail-closed on any error.
type AuthInterceptor struct {
	introspectURL string            // e.g. "http://127.0.0.1:4445/admin/oauth2/introspect"
	methodScopes  map[string]string // gRPC full method → required scope
	client        *http.Client
	log           *logger.Logger
}

// NewAuthInterceptor creates an interceptor that validates tokens via Hydra.
// introspectURL is Hydra's admin introspection endpoint.
// methodScopes maps gRPC method names (e.g. "/clawker.admin.v1.AdminService/Install")
// to required OAuth2 scopes (e.g. "admin").
func NewAuthInterceptor(introspectURL string, methodScopes map[string]string, log *logger.Logger) *AuthInterceptor {
	return &AuthInterceptor{
		introspectURL: introspectURL,
		methodScopes:  methodScopes,
		client:        &http.Client{Timeout: 5 * time.Second},
		log:           log,
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
		// Fail-closed: unmapped methods are denied.
		a.log.Warn().Str("method", fullMethod).Msg("authz: unmapped method denied")
		return status.Error(codes.Unauthenticated, "unauthorized")
	}

	token, err := extractBearerToken(ctx)
	if err != nil {
		a.log.Debug().Str("method", fullMethod).Msg("authz: no bearer token")
		return status.Error(codes.Unauthenticated, "missing bearer token")
	}

	result, err := a.introspect(ctx, token, requiredScope)
	if err != nil {
		a.log.Warn().Err(err).Str("method", fullMethod).Msg("authz: introspection failed")
		return status.Error(codes.Unauthenticated, "token validation failed")
	}

	if !result.Active {
		a.log.Debug().Str("method", fullMethod).Msg("authz: token inactive")
		return status.Error(codes.Unauthenticated, "token inactive or invalid")
	}

	a.log.Debug().
		Str("method", fullMethod).
		Str("client_id", result.ClientID).
		Str("scope", result.Scope).
		Msg("authz: access granted")
	return nil
}

// introspectionResult represents the relevant fields from Hydra's
// OAuth2 token introspection response (RFC 7662).
type introspectionResult struct {
	Active   bool   `json:"active"`
	Scope    string `json:"scope"`
	ClientID string `json:"client_id"`
	Sub      string `json:"sub"`
	Exp      int64  `json:"exp"`
}

func (a *AuthInterceptor) introspect(ctx context.Context, token, requiredScope string) (*introspectionResult, error) {
	form := url.Values{
		"token": {token},
		"scope": {requiredScope},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.introspectURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build introspection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("introspection request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("introspection returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read introspection response: %w", err)
	}

	var result introspectionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode introspection response: %w", err)
	}

	return &result, nil
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
