package controlplane

import (
	"context"
	"slices"
	"strings"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// methodScopes is the static authz policy for ControlPlaneService. Every
// gRPC method the CP exposes must have an entry here. A missing entry is
// treated as PermissionDenied by the interceptor — fail closed — so that
// adding a new RPC without updating this map is a visible failure, not a
// silent open hole.
//
// The empty-string scope ("") marks a method as public: no authentication
// is required. Currently only Health qualifies — it's used by the firewall
// manager's readiness probe before the CLI has set up its OIDC client,
// and revealing "the CP is alive" to anyone who can reach the socket is
// not a privacy concern on a UDS with file-permission trust.
//
// Future entries live here too: when `AgentReportingService` or a
// `WebUIService` is registered, each of their full-method names gets one
// line. Scopes can be anything (multi-caller work may introduce
// scopes like `agent:register`, `webui:read`, etc.).
var methodScopes = map[string]string{
	fullMethod(v1.ControlPlaneService_ServiceDesc.ServiceName, "Health"):                   "",
	fullMethod(v1.ControlPlaneService_ServiceDesc.ServiceName, "EnableContainerFirewall"):  ScopeFirewallAdmin,
	fullMethod(v1.ControlPlaneService_ServiceDesc.ServiceName, "DisableContainerFirewall"): ScopeFirewallAdmin,
	fullMethod(v1.ControlPlaneService_ServiceDesc.ServiceName, "BypassContainer"):          ScopeFirewallAdmin,
	fullMethod(v1.ControlPlaneService_ServiceDesc.ServiceName, "UnbypassContainer"):        ScopeFirewallAdmin,
	fullMethod(v1.ControlPlaneService_ServiceDesc.ServiceName, "SyncFirewallRoutes"):       ScopeFirewallAdmin,
	fullMethod(v1.ControlPlaneService_ServiceDesc.ServiceName, "UpdateDnsCache"):           ScopeFirewallAdmin,
	fullMethod(v1.ControlPlaneService_ServiceDesc.ServiceName, "GarbageCollectDns"):        ScopeFirewallAdmin,
	fullMethod(v1.ControlPlaneService_ServiceDesc.ServiceName, "LookupContainer"):          ScopeFirewallAdmin,
	fullMethod(v1.ControlPlaneService_ServiceDesc.ServiceName, "ResolveHostname"):          ScopeFirewallAdmin,
}

// fullMethod constructs a gRPC full-method name ("/package.Service/Method")
// used as the key in methodScopes and as the value of
// grpc.UnaryServerInfo.FullMethod at request time.
func fullMethod(service, method string) string {
	return "/" + service + "/" + method
}

// AuthUnaryInterceptor returns a grpc.UnaryServerInterceptor that enforces
// the CP's auth policy on every incoming RPC:
//
//  1. Look up the method's required scope. A missing entry → PermissionDenied
//     (fail closed). An empty-string entry → the method is public.
//  2. Extract `authorization: Bearer <jwt>` from the request metadata.
//  3. Verify the JWT via the provided verifier (signature + iss + aud + exp).
//  4. Cross-check the JWT `sub` claim against the mTLS peer cert CN. A
//     mismatch means someone paired a stolen cert with someone else's
//     token — reject.
//  5. Check that the JWT's granted scopes include the required scope.
//
// The interceptor is intentionally strict: every failure path returns a
// typed gRPC error (Unauthenticated or PermissionDenied) and logs nothing
// sensitive to the caller.
func AuthUnaryInterceptor(verifier *TokenVerifier) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if err := authorize(ctx, info.FullMethod, verifier); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// AuthStreamInterceptor returns the stream-side equivalent of
// AuthUnaryInterceptor. It runs the same authorize() check before
// invoking the stream handler. v1 has no streaming RPCs on
// ControlPlaneService but the interceptor is wired up for parity so
// future streaming methods inherit the same authz enforcement.
func AuthStreamInterceptor(verifier *TokenVerifier) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if err := authorize(ss.Context(), info.FullMethod, verifier); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// authorize is the shared authz core used by both the unary and stream
// interceptors. Split out so behavior changes land in one place.
func authorize(ctx context.Context, method string, verifier *TokenVerifier) error {
	required, known := methodScopes[method]
	if !known {
		// Fail closed: any method not explicitly mapped is rejected.
		return status.Errorf(codes.PermissionDenied, "no scope policy for %q", method)
	}

	// Public methods (empty required scope) skip JWT verification and
	// mTLS CN checks. Useful for Health, which the CLI may call during
	// startup before it has a cached token.
	if required == "" {
		return nil
	}

	// Extract the bearer token from the request metadata.
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing request metadata")
	}
	authVals := md.Get("authorization")
	if len(authVals) == 0 {
		return status.Error(codes.Unauthenticated, "missing authorization header")
	}
	rawToken, ok := bearerToken(authVals[0])
	if !ok {
		return status.Error(codes.Unauthenticated, "authorization header is not a bearer token")
	}

	claims, err := verifier.Verify(rawToken)
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "token verification failed: %v", err)
	}

	// Cross-check mTLS peer CN against JWT subject. This is the
	// "stolen cert + stolen token" defense: even if an attacker obtains
	// both a valid client cert and a valid JWT, mismatched ownership is
	// rejected before the handler runs.
	peerCN, err := peerCommonName(ctx)
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "peer auth: %v", err)
	}
	if peerCN != claims.Subject {
		return status.Errorf(codes.Unauthenticated,
			"mTLS peer CN %q does not match JWT sub %q", peerCN, claims.Subject)
	}

	if !slices.Contains(claims.Scopes, required) {
		return status.Errorf(codes.PermissionDenied,
			"token does not include required scope %q", required)
	}
	return nil
}

// bearerToken extracts the token portion of an "Authorization: Bearer XXX"
// header value. The check is case-insensitive on the scheme.
func bearerToken(headerValue string) (string, bool) {
	const prefix = "bearer "
	if len(headerValue) <= len(prefix) {
		return "", false
	}
	if !strings.EqualFold(headerValue[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(headerValue[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// peerCommonName pulls the Common Name from the mTLS client cert on the
// current connection. Returns an error if the call did not arrive via
// TLS or if no peer cert was presented.
func peerCommonName(ctx context.Context) (string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", errNoPeer
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", errNotTLS
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return "", errNoClientCert
	}
	cn := strings.TrimSpace(tlsInfo.State.PeerCertificates[0].Subject.CommonName)
	if cn == "" {
		return "", errEmptyCN
	}
	return cn, nil
}

// Sentinel errors kept as vars so tests can assert against them without
// string matching.
var (
	errNoPeer       = authErr("no peer in context")
	errNotTLS       = authErr("peer is not mTLS")
	errNoClientCert = authErr("peer did not present a client certificate")
	errEmptyCN      = authErr("peer client certificate has empty Common Name")
)

// authErr is a lightweight typed error for authz diagnostics.
type authErr string

func (e authErr) Error() string { return string(e) }
