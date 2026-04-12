package controlplane

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSigningKey mints an RSA key for isolated test use. Kept short
// 2048 bits — the minimum that satisfies semgrep's key-size check.
func newTestSigningKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

// TestTokenIssuer_IssueVerifyRoundTrip issues a JWT, feeds it back into
// the paired verifier, and asserts the claims come through intact.
func TestTokenIssuer_IssueVerifyRoundTrip(t *testing.T) {
	issuer := NewTokenIssuer(newTestSigningKey(t))

	raw, exp, err := issuer.Issue(ClientIDCLI, []string{ScopeFirewallAdmin})
	require.NoError(t, err)
	assert.NotEmpty(t, raw)
	assert.True(t, exp.After(time.Now()),
		"expiration should be in the future")

	claims, err := issuer.Verifier().Verify(raw)
	require.NoError(t, err)
	assert.Equal(t, ClientIDCLI, claims.Subject)
	assert.ElementsMatch(t, []string{ScopeFirewallAdmin}, claims.Scopes)
}

// TestTokenVerifier_RejectsBadSignature asserts that a JWT signed by a
// different key is rejected by the verifier even when the claims are
// otherwise well-formed. This is the core cryptographic guarantee.
func TestTokenVerifier_RejectsBadSignature(t *testing.T) {
	realIssuer := NewTokenIssuer(newTestSigningKey(t))
	attacker := NewTokenIssuer(newTestSigningKey(t))

	// Attacker signs a token for clawker-cli with full admin scope.
	// Issued by the wrong key — the real verifier should reject it.
	rawAttackerToken, _, err := attacker.Issue(ClientIDCLI, []string{ScopeFirewallAdmin})
	require.NoError(t, err)

	_, err = realIssuer.Verifier().Verify(rawAttackerToken)
	require.Error(t, err, "real verifier must reject attacker-signed token")
	assert.Contains(t, err.Error(), "verify",
		"error should mention signature verification")
}

// TestTokenVerifier_RejectsExpired confirms expiration validation — a
// token with past expiry should fail Verify, regardless of signature.
func TestTokenVerifier_RejectsExpired(t *testing.T) {
	issuer := NewTokenIssuer(newTestSigningKey(t))

	// Swap out the TTL to issue a token that's already expired (past
	// the verifier's 30s leeway).
	issuer.ttl = -5 * time.Minute

	raw, _, err := issuer.Issue(ClientIDCLI, []string{ScopeFirewallAdmin})
	require.NoError(t, err)

	_, err = issuer.Verifier().Verify(raw)
	require.Error(t, err)
	// go-jose wraps expiration errors with "expired" somewhere in the chain.
	assert.True(t,
		strings.Contains(err.Error(), "expired") ||
			strings.Contains(err.Error(), "exp"),
		"error should mention expiration: %v", err)
}

// TestTokenVerifier_RejectsEmptySubject guards against tokens whose
// subject claim is missing — the authz interceptor cross-checks sub
// against the mTLS peer CN, so an empty sub is a hard-fail case.
func TestTokenVerifier_RejectsEmptySubject(t *testing.T) {
	issuer := NewTokenIssuer(newTestSigningKey(t))
	raw, _, err := issuer.Issue("", []string{ScopeFirewallAdmin})
	require.NoError(t, err)

	_, err = issuer.Verifier().Verify(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subject")
}

// TestIntersectScopes covers the /token handler's scope narrowing: when
// a client asks for a subset of its authorized scopes, only the allowed
// subset comes back; requests for unauthorized scopes get dropped.
func TestIntersectScopes(t *testing.T) {
	tests := []struct {
		name       string
		requested  []string
		allowed    []string
		wantResult []string
	}{
		{
			name:       "exact match",
			requested:  []string{"firewall:admin"},
			allowed:    []string{"firewall:admin"},
			wantResult: []string{"firewall:admin"},
		},
		{
			name:       "requested is subset",
			requested:  []string{"firewall:admin"},
			allowed:    []string{"firewall:admin", "dns:report"},
			wantResult: []string{"firewall:admin"},
		},
		{
			name:       "requested includes disallowed",
			requested:  []string{"firewall:admin", "root:god"},
			allowed:    []string{"firewall:admin"},
			wantResult: []string{"firewall:admin"},
		},
		{
			name:       "all requested are disallowed",
			requested:  []string{"root:god"},
			allowed:    []string{"firewall:admin"},
			wantResult: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := intersectScopes(tt.requested, tt.allowed)
			if len(tt.wantResult) == 0 {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tt.wantResult, got)
		})
	}
}

// TestBearerToken_Parse covers the interceptor's bearer-header parsing.
func TestBearerToken_Parse(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantTok string
		wantOk  bool
	}{
		{name: "plain bearer", header: "Bearer abc.def.ghi", wantTok: "abc.def.ghi", wantOk: true},
		{name: "lowercase bearer", header: "bearer abc.def.ghi", wantTok: "abc.def.ghi", wantOk: true},
		{name: "trailing whitespace", header: "Bearer abc.def.ghi   ", wantTok: "abc.def.ghi", wantOk: true},
		{name: "empty after prefix", header: "Bearer ", wantTok: "", wantOk: false},
		{name: "just bearer", header: "Bearer", wantTok: "", wantOk: false},
		{name: "wrong scheme", header: "Basic abc123", wantTok: "", wantOk: false},
		{name: "empty string", header: "", wantTok: "", wantOk: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := bearerToken(tt.header)
			assert.Equal(t, tt.wantTok, got)
			assert.Equal(t, tt.wantOk, ok)
		})
	}
}

// TestMethodScopes_NoUnmappedPublicMethods asserts every declared method
// on ControlPlaneService is in methodScopes. A missing entry is valid
// (fail-closed behavior in authz), but the test ensures we haven't
// forgotten a method by accident — adding a new RPC to the proto
// without thinking about authz should fail this test.
func TestMethodScopes_CoversAllControlPlaneMethods(t *testing.T) {
	// Known methods on ControlPlaneService. If a new one is added to
	// the proto, append it here, which will force a test update that
	// reminds the author to add a methodScopes entry too.
	expected := []string{
		"Health",
		"EnableContainerFirewall",
		"DisableContainerFirewall",
		"BypassContainer",
		"UnbypassContainer",
		"SyncFirewallRoutes",
		"UpdateDnsCache",
		"GarbageCollectDns",
		"LookupContainer",
		"ResolveHostname",
	}
	for _, method := range expected {
		full := fullMethod("clawker.agent.v1.ControlPlaneService", method)
		_, ok := methodScopes[full]
		assert.Truef(t, ok, "methodScopes missing entry for %s", full)
	}
}
