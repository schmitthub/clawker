package auth

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

	"github.com/schmitthub/clawker/internal/consts"
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
		// On a non-200 the Hydra status code is the load-bearing signal
		// for the operator (502/503 = Hydra/upstream issue, 401/403 = auth
		// problem). Surface it in the message regardless of whether the
		// body is readable; if the read fails too, wrap the read error
		// while keeping the status code visible in the static message.
		hint, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if readErr != nil {
			return nil, fmt.Errorf("introspection returned %d (body read also failed): %w", resp.StatusCode, readErr)
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
// check active + scope → optional client_id pin → allow or deny.
// Fail-closed on any error.
//
// It is generic over the service's scope type S (a distinct named string
// type per service, e.g. adminv1.AdminScope, agentv1.AgentScope). Type
// inference binds S from the methodScopes map at construction, so an
// admin interceptor cannot be built from an agent scope map and vice
// versa — cross-service scope wiring fails to compile.
type AuthInterceptor[S ~string] struct {
	introspector     Introspector
	methodScopes     map[string]S // gRPC full method → required scope
	requiredClientID string       // optional: when non-empty, token's client_id must match
	log              *logger.Logger
}

// NewAuthInterceptor creates an interceptor that validates tokens via
// the given Introspector. methodScopes maps gRPC method names
// (e.g. "/clawker.admin.v1.AdminService/Install") to required OAuth2
// scopes (e.g. adminv1.ScopeAdmin). S is inferred from the map.
func NewAuthInterceptor[S ~string](introspector Introspector, methodScopes map[string]S, log *logger.Logger) *AuthInterceptor[S] {
	if log == nil {
		log = logger.Nop()
	}
	return &AuthInterceptor[S]{
		introspector: introspector,
		methodScopes: methodScopes,
		log:          log,
	}
}

// RequireClientID pins the interceptor to a specific OAuth2 client_id.
// Defense-in-depth on top of scope: when set, any token whose
// introspection result reports a different client_id is rejected with
// codes.PermissionDenied even if the scope check passed. Used on the
// agent listener (clientID = consts.ClientIDAgent) so a future Hydra
// misconfiguration that grants agent:self:register to a non-agent
// client doesn't silently let the wrong client through. The admin
// listener leaves this empty — the admin scope is required on every
// admin RPC except the public GetSystemTime, and the CLI client_id is
// the only one Hydra is currently configured to grant that scope to.
// Returns the receiver for fluent chaining at construction.
func (a *AuthInterceptor[S]) RequireClientID(clientID string) *AuthInterceptor[S] {
	a.requiredClientID = clientID
	return a
}

// UnaryInterceptor returns a gRPC unary server interceptor.
func (a *AuthInterceptor[S]) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := a.authorize(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor returns a gRPC stream server interceptor.
func (a *AuthInterceptor[S]) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := a.authorize(stream.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, stream)
	}
}

// GRPCServerOptions returns gRPC server options wired with both interceptors.
func (a *AuthInterceptor[S]) GRPCServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.UnaryInterceptor(a.UnaryInterceptor()),
		grpc.StreamInterceptor(a.StreamInterceptor()),
	}
}

func (a *AuthInterceptor[S]) authorize(ctx context.Context, fullMethod string) error {
	required, ok := a.methodScopes[fullMethod]
	requiredScope := string(required)
	if !ok || requiredScope == "" {
		// Both fail closed identically; distinguish the cause in the log so an
		// operator can tell a missing proto-method scope entry from a method
		// deliberately/accidentally mapped to the zero-value scope.
		reason := "method has no scope entry"
		if ok {
			reason = "method mapped to empty scope"
		}
		a.log.Warn().Str("method", fullMethod).Str("reason", reason).Msg("authz: method denied (fail-closed)")
		return status.Error(codes.Unauthenticated, "unauthorized")
	}

	// Public scope means the method is served on mTLS alone with
	// no authz bearer token (e.g. AdminService.GetSystemTime).
	if requiredScope == consts.ScopePublic {
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

	// Defense-in-depth: when this interceptor is pinned to a specific
	// client_id (agent listener — see RequireClientID), reject tokens
	// from any other Hydra client even though they passed scope. Today
	// only the clawker-agent client is registered with the agent scope,
	// so a mismatch here means either a Hydra misconfiguration or an
	// unexpected new client wiring — both deserve a generic
	// PermissionDenied (don't leak which check failed) plus a warn-level
	// log carrying the actual client_id seen.
	if a.requiredClientID != "" && result.ClientID != a.requiredClientID {
		a.log.Warn().
			Str("method", fullMethod).
			Str("required_client_id", a.requiredClientID).
			Str("token_client_id", result.ClientID).
			Msg("authz: token client_id does not match required client_id")
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
