package controlplane

import (
	"crypto/rsa"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// TokenIssuer wraps the RSA signing key and emits short-lived JWTs that
// follow the OIDC client_credentials shape. It lives behind the /token
// HTTP endpoint served on the OIDC UDS listener.
//
// This v1 implementation speaks OIDC wire protocol directly (form-encoded
// POST to /token, JSON response with access_token + expires_in) without
// embedding a full OIDC provider library. The wire contract is stable:
// when a future PR introduces browser-delivered callers (webui) that
// require authorization_code + PKCE, the /token handler can be swapped
// for an embedded `zitadel/oidc` provider without changing:
//   - the JWT claim shape (iss/sub/aud/exp/iat/scope)
//   - the signing algorithm (RS256)
//   - the gRPC interceptor's verifier
//   - client-side code (oauth2.TokenSource + grpc/credentials/oauth)
//
// The only thing that changes in that follow-up is the /token handler's
// internals — clients and verifiers don't notice.
type TokenIssuer struct {
	signingKey *rsa.PrivateKey
	verifier   *TokenVerifier
	issuer     string
	audience   string
	ttl        time.Duration
}

// NewTokenIssuer builds a TokenIssuer from the RSA private key loaded by
// ca.go. The issuer and audience strings are baked into every JWT; the
// TTL is the expiration window for issued tokens.
func NewTokenIssuer(signingKey *rsa.PrivateKey) *TokenIssuer {
	return &TokenIssuer{
		signingKey: signingKey,
		verifier:   newTokenVerifier(&signingKey.PublicKey),
		issuer:     cpIssuerURL,
		audience:   cpAudience,
		ttl:        accessTokenTTL,
	}
}

// Verifier returns the JWT verifier paired with this issuer. The gRPC
// authz interceptor uses it to validate incoming tokens.
func (i *TokenIssuer) Verifier() *TokenVerifier {
	return i.verifier
}

// Issue creates and signs a JWT for the given client with the requested
// scopes. Returns the compact-serialized JWT string, the expiration time,
// and any error. Caller is expected to have already validated that the
// client is authorized for every scope in `scopes` (TokenIssuer does not
// do authz — it just signs what it's given).
func (i *TokenIssuer) Issue(clientID string, scopes []string) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(i.ttl)

	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: i.signingKey},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build signer: %w", err)
	}

	standard := jwt.Claims{
		Issuer:    i.issuer,
		Subject:   clientID,
		Audience:  jwt.Audience{i.audience},
		Expiry:    jwt.NewNumericDate(exp),
		NotBefore: jwt.NewNumericDate(now.Add(-1 * time.Minute)),
		IssuedAt:  jwt.NewNumericDate(now),
	}
	private := map[string]any{
		// Scope is a space-separated list per RFC 8693. Using a string
		// instead of a JSON array matches OIDC convention and avoids the
		// ambiguity some clients have with array-valued scope claims.
		"scope": strings.Join(scopes, " "),
	}

	signed, err := jwt.Signed(sig).Claims(standard).Claims(private).Serialize()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return signed, exp, nil
}

// Wire-protocol constants. These are the strings clients see; changing
// any of them breaks wire compatibility with existing clients.
const (
	// cpIssuerURL is the `iss` claim on issued JWTs. It's a URL but is
	// not reachable — v1 is UDS-only. Keeping it URL-shaped makes the
	// tokens drop-in compatible with an OIDC discovery document when
	// the webui follow-up lands.
	cpIssuerURL = "https://clawker-cp/"

	// cpAudience is the `aud` claim value. The gRPC authz interceptor
	// requires every incoming JWT to include this audience.
	cpAudience = "clawker-cp"

	// accessTokenTTL is the lifetime of issued JWTs. Short enough to
	// contain damage from a leaked token, long enough that refresh
	// doesn't dominate traffic on the gRPC channel.
	accessTokenTTL = 5 * time.Minute
)

// ---------------------------------------------------------------------------
// HTTP handlers — OIDC wire protocol for client_credentials grant.
// ---------------------------------------------------------------------------

// NewOIDCMux returns the http.ServeMux that handles the OIDC endpoints
// the control plane exposes on its HTTPS UDS listener:
//
//	POST /token
//	  client_credentials grant, mTLS client auth (RFC 8705).
//	  Returns JSON {access_token, token_type, expires_in, scope}.
//
//	GET /.well-known/openid-configuration
//	  OIDC discovery document listing the available endpoints, grant
//	  types, and signing algorithms.
//
//	GET /keys
//	  JWKS containing the public half of the signing key. Clients that
//	  prefer external JWT verification can fetch this and cache it;
//	  the in-process gRPC interceptor uses TokenVerifier directly.
//
// The returned mux is meant to be served by an *http.Server with TLS
// RequireAndVerifyClientCert enabled — every request is expected to
// arrive with a valid mTLS peer certificate signed by the CP CA.
func NewOIDCMux(issuer *TokenIssuer) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("POST /token", &tokenHandler{issuer: issuer})
	mux.Handle("GET /.well-known/openid-configuration", &discoveryHandler{})
	mux.Handle("GET /keys", &jwksHandler{signingKey: &issuer.signingKey.PublicKey})
	return mux
}

// tokenHandler implements the /token endpoint for the client_credentials
// grant with tls_client_auth (RFC 8705).
type tokenHandler struct {
	issuer *TokenIssuer
}

func (h *tokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}

	if r.PostForm.Get("grant_type") != "client_credentials" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"only client_credentials is supported")
		return
	}

	// Identify the caller via the mTLS peer cert. RFC 8705 tls_client_auth:
	// the cert is the credential, no client_id/client_secret in the body.
	clientID, err := clientIDFromTLS(r.TLS)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}

	registration, ok := LookupClient(clientID)
	if !ok {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client",
			fmt.Sprintf("unknown client %q", clientID))
		return
	}

	// Requested scopes default to all the client is authorized for. If
	// the request includes an explicit `scope` form field, intersect it
	// with the client's registered scopes (dropping any scope the client
	// doesn't own).
	granted := registration.Scopes
	if req := r.PostForm.Get("scope"); req != "" {
		granted = intersectScopes(strings.Fields(req), registration.Scopes)
		if len(granted) == 0 {
			writeOAuthError(w, http.StatusForbidden, "invalid_scope",
				"no requested scopes are authorized for this client")
			return
		}
	}

	token, exp, err := h.issuer.Issue(clientID, granted)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			fmt.Sprintf("token issuance failed: %v", err))
		return
	}

	resp := tokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int64(time.Until(exp).Seconds()),
		Scope:       strings.Join(granted, " "),
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// tokenResponse is the JSON body returned by /token on success. Matches
// RFC 6749 §5.1 exactly so OIDC-compliant clients parse it unchanged.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope,omitempty"`
}

// oauthErrorBody matches RFC 6749 §5.2 for the token endpoint error
// response format. Fields are lowercased and use snake_case per spec.
type oauthErrorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(oauthErrorBody{Error: code, ErrorDescription: description})
}

// intersectScopes returns the elements of `requested` that are present in
// `allowed`, preserving requested-order. Used by the /token handler to
// narrow the granted scope set when the client asks for a subset.
func intersectScopes(requested, allowed []string) []string {
	out := make([]string, 0, len(requested))
	for _, s := range requested {
		if slices.Contains(allowed, s) {
			out = append(out, s)
		}
	}
	return out
}

// clientIDFromTLS extracts the CN from the mTLS peer cert on a request.
// Returns an error if the request didn't arrive over TLS, if no peer cert
// was provided, or if the CN is empty.
func clientIDFromTLS(state *tls.ConnectionState) (string, error) {
	if state == nil {
		return "", errors.New("mTLS required: request did not arrive over TLS")
	}
	if len(state.PeerCertificates) == 0 {
		return "", errors.New("mTLS required: no client certificate presented")
	}
	cn := strings.TrimSpace(state.PeerCertificates[0].Subject.CommonName)
	if cn == "" {
		return "", errors.New("mTLS peer certificate has empty Common Name")
	}
	return cn, nil
}

// ---------------------------------------------------------------------------
// Discovery + JWKS handlers.
// ---------------------------------------------------------------------------

type discoveryHandler struct{}

func (h *discoveryHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	// OIDC discovery document. Advertises only the endpoints and flows
	// we actually support; webui follow-up adds /authorize, /userinfo.
	doc := map[string]any{
		"issuer":                                cpIssuerURL,
		"token_endpoint":                        cpIssuerURL + "token",
		"jwks_uri":                              cpIssuerURL + "keys",
		"grant_types_supported":                 []string{"client_credentials"},
		"token_endpoint_auth_methods_supported": []string{"tls_client_auth"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{ScopeFirewallAdmin},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

type jwksHandler struct {
	signingKey *rsa.PublicKey
}

func (h *jwksHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	jwks := jose.JSONWebKeySet{
		Keys: []jose.JSONWebKey{
			{
				Key:       h.signingKey,
				Algorithm: string(jose.RS256),
				Use:       "sig",
				KeyID:     "cp-oidc-signing",
			},
		},
	}
	w.Header().Set("Content-Type", "application/jwk-set+json")
	_ = json.NewEncoder(w).Encode(jwks)
}

// ---------------------------------------------------------------------------
// Token verification (used by the gRPC authz interceptor).
// ---------------------------------------------------------------------------

// TokenVerifier verifies JWTs issued by TokenIssuer. Pairs with the authz
// interceptor in authz.go — the interceptor calls Verify() on each
// incoming bearer token and uses the resulting claims for authorization.
type TokenVerifier struct {
	publicKey *rsa.PublicKey
	issuer    string
	audience  string
}

func newTokenVerifier(publicKey *rsa.PublicKey) *TokenVerifier {
	return &TokenVerifier{
		publicKey: publicKey,
		issuer:    cpIssuerURL,
		audience:  cpAudience,
	}
}

// VerifiedClaims is the trimmed subset of JWT claims the authz interceptor
// needs after validation. Separating it from jwt.Claims decouples the
// interceptor from the underlying JWT library.
type VerifiedClaims struct {
	Subject string
	Scopes  []string
}

// Verify parses, checks signature + audience + issuer + expiration + nbf
// on a JWT. Returns parsed claims on success; returns an error on any
// validation failure. The interceptor converts non-nil errors to
// codes.Unauthenticated.
func (v *TokenVerifier) Verify(raw string) (*VerifiedClaims, error) {
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		return nil, fmt.Errorf("parse jwt: %w", err)
	}

	var standard jwt.Claims
	var private struct {
		Scope string `json:"scope"`
	}
	if err := tok.Claims(v.publicKey, &standard, &private); err != nil {
		return nil, fmt.Errorf("verify signature: %w", err)
	}

	// Validate standard claims (iss, aud, exp, nbf) with a small leeway
	// for clock skew between the issuer process and the verifying process
	// — in v1 these are the same process, but a future PR running the CP
	// as a separate issuer would benefit from the leeway.
	if err := standard.ValidateWithLeeway(jwt.Expected{
		Issuer:      v.issuer,
		AnyAudience: jwt.Audience{v.audience},
		Time:        time.Now().UTC(),
	}, 30*time.Second); err != nil {
		return nil, fmt.Errorf("claim validation: %w", err)
	}

	if standard.Subject == "" {
		return nil, errors.New("jwt subject claim is empty")
	}

	return &VerifiedClaims{
		Subject: standard.Subject,
		Scopes:  strings.Fields(private.Scope),
	}, nil
}
