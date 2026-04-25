package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"strings"
	"testing"

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

	// Expiry is roughly 24h ahead, with slack for build/test latency.
	require.NotNil(t, claims.Expiry)
	require.NotNil(t, claims.IssuedAt)
	exp := claims.Expiry.Time().Sub(claims.IssuedAt.Time())
	assert.InDelta(t, AgentAssertionTTL.Seconds(), exp.Seconds(), 5,
		"assertion TTL should be ~24h: got %s", exp)
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
