package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
)

func TestBuildAgentAssertion_ClaimsAndAlg(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	const audience = "https://hydra.example/oauth2/token"
	tok, err := BuildAgentAssertion(audience, key)
	require.NoError(t, err)

	parsed, err := josejwt.ParseSigned(tok, []jose.SignatureAlgorithm{jose.ES256})
	require.NoError(t, err)

	var claims josejwt.Claims
	require.NoError(t, parsed.Claims(&key.PublicKey, &claims))

	// iss + sub must both be the agent client id; aud must be the
	// passed token URL.
	assert.Equal(t, consts.ClientIDAgent, claims.Issuer)
	assert.Equal(t, consts.ClientIDAgent, claims.Subject)
	require.Len(t, claims.Audience, 1)
	assert.Equal(t, audience, claims.Audience[0])

	// jti must be non-empty and look like a UUID (hyphenated).
	assert.NotEmpty(t, claims.ID)
	assert.True(t, strings.Count(claims.ID, "-") >= 4, "jti should be UUID-like, got %q", claims.ID)

	// Expiry is roughly 24h ahead of real now, with slack for build/test
	// latency. iat is backdated by assertionClockSkewLeeway for clock-skew
	// tolerance, so exp-iat is TTL+leeway, not TTL — measure exp from now.
	require.NotNil(t, claims.Expiry)
	require.NotNil(t, claims.IssuedAt)
	ttlFromNow := time.Until(claims.Expiry.Time())
	assert.InDelta(t, AgentAssertionTTL.Seconds(), ttlFromNow.Seconds(), 5,
		"assertion expiry should be ~24h from now: got %s", ttlFromNow)
	assert.True(t, claims.IssuedAt.Time().Before(time.Now()),
		"iat must be backdated below now for clock-skew tolerance")
}

func TestBuildAgentAssertion_DistinctJTI(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	a, err := BuildAgentAssertion("https://hydra.example/oauth2/token", key)
	require.NoError(t, err)
	b, err := BuildAgentAssertion("https://hydra.example/oauth2/token", key)
	require.NoError(t, err)

	assert.NotEqual(t, a, b, "two assertions must have distinct JTI → distinct serialised tokens")
}

// TestBuildAgentAssertion_RejectsBadInput exercises the input
// validation surface. Each subtest asserts the function returns an
// error AND does not return a partially-constructed token (the empty
// string is the contract). Catches a future regression where a check
// is silently dropped or the function returns a token alongside the
// error.
func TestBuildAgentAssertion_RejectsBadInput(t *testing.T) {
	goodKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	t.Run("empty audience", func(t *testing.T) {
		tok, err := BuildAgentAssertion("", goodKey)
		require.Error(t, err)
		assert.Empty(t, tok, "no token must be produced when validation fails")
	})

	t.Run("nil signing key", func(t *testing.T) {
		tok, err := BuildAgentAssertion("https://hydra.example/oauth2/token", nil)
		require.Error(t, err)
		assert.Empty(t, tok)
	})

	// Note: the *ecdsa.PrivateKey type-level constraint already forbids
	// RSA keys at compile time. Non-P256 ECDSA curves (P-384, P-521)
	// are reachable at runtime — the jose ES256 signer rejects them
	// because the curve doesn't match the algorithm's required hash
	// size. Exercise that path so a future signer-config refactor that
	// silently widens the accepted curve set fails the test.
	t.Run("non-P256 ECDSA signing key (P-384)", func(t *testing.T) {
		p384Key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		require.NoError(t, err)
		tok, err := BuildAgentAssertion("https://hydra.example/oauth2/token", p384Key)
		require.Error(t, err)
		assert.Empty(t, tok)
	})

	t.Run("non-P256 ECDSA signing key (P-521)", func(t *testing.T) {
		p521Key, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
		require.NoError(t, err)
		tok, err := BuildAgentAssertion("https://hydra.example/oauth2/token", p521Key)
		require.Error(t, err)
		assert.Empty(t, tok)
	})
}
