package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- EnsureAuthMaterial ---

func TestEnsureAuthMaterial_CreatesFiles(t *testing.T) {
	dataDir := t.TempDir()
	require.NoError(t, EnsureAuthMaterial(dataDir))

	for _, path := range []string{
		SigningKeyPath(dataDir),
		SigningJWKPath(dataDir),
		ServerCertPath(dataDir),
		ServerKeyPath(dataDir),
	} {
		_, err := os.Stat(path)
		assert.NoError(t, err, "expected file: %s", path)
	}
}

func TestEnsureAuthMaterial_Idempotent(t *testing.T) {
	dataDir := t.TempDir()
	require.NoError(t, EnsureAuthMaterial(dataDir))

	first, err := os.ReadFile(SigningKeyPath(dataDir))
	require.NoError(t, err)

	require.NoError(t, EnsureAuthMaterial(dataDir))

	second, err := os.ReadFile(SigningKeyPath(dataDir))
	require.NoError(t, err)

	assert.Equal(t, first, second, "signing key must not change on idempotent call")
}

func TestEnsureAuthMaterial_PrivateKeyPermissions(t *testing.T) {
	dataDir := t.TempDir()
	require.NoError(t, EnsureAuthMaterial(dataDir))

	for _, path := range []string{SigningKeyPath(dataDir), ServerKeyPath(dataDir)} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "%s must be 0600", path)
	}
}

func TestLoadSigningKey(t *testing.T) {
	dataDir := t.TempDir()
	require.NoError(t, EnsureAuthMaterial(dataDir))

	key, err := LoadSigningKey(dataDir)
	require.NoError(t, err)
	assert.Equal(t, "P-256", key.Curve.Params().Name)
}

func TestServerTLSCert(t *testing.T) {
	dataDir := t.TempDir()
	require.NoError(t, EnsureAuthMaterial(dataDir))

	cert, err := ServerTLSCert(dataDir)
	require.NoError(t, err)
	assert.Equal(t, "clawker-cp", cert.Subject.CommonName)
	assert.Contains(t, cert.DNSNames, "localhost")
}

func TestReadJWK(t *testing.T) {
	dataDir := t.TempDir()
	require.NoError(t, EnsureAuthMaterial(dataDir))

	jwk, err := ReadJWK(dataDir)
	require.NoError(t, err)
	assert.Contains(t, string(jwk), `"kty"`)
	assert.Contains(t, string(jwk), `"EC"`)
}

// --- Assertion claims ---

func TestValidateAssertionClaims(t *testing.T) {
	valid := AssertionClaims{
		Issuer:           "clawker-cli",
		Subject:          "clawker-cli",
		Audience:         "http://127.0.0.1:4444/oauth2/token",
		JWTID:            "unique-id",
		ExpiresInSeconds: 30,
	}

	t.Run("valid", func(t *testing.T) {
		assert.NoError(t, ValidateAssertionClaims(valid))
	})

	for _, tc := range []struct {
		name  string
		claim string
		tweak func(*AssertionClaims)
	}{
		{"missing iss", "iss", func(c *AssertionClaims) { c.Issuer = "" }},
		{"missing sub", "sub", func(c *AssertionClaims) { c.Subject = "" }},
		{"missing aud", "aud", func(c *AssertionClaims) { c.Audience = "" }},
		{"missing jti", "jti", func(c *AssertionClaims) { c.JWTID = "" }},
		{"zero exp", "exp", func(c *AssertionClaims) { c.ExpiresInSeconds = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.tweak(&c)
			err := ValidateAssertionClaims(c)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.claim)
		})
	}
}

func TestBuildSignedAssertion(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	claims := AssertionClaims{
		Issuer:           "clawker-cli",
		Subject:          "clawker-cli",
		Audience:         "http://127.0.0.1:4444/oauth2/token",
		JWTID:            "test-jti",
		ExpiresInSeconds: 30,
	}

	signed, err := BuildSignedAssertion(claims, key)
	require.NoError(t, err)

	parts := strings.Split(signed, ".")
	require.Len(t, parts, 3, "JWT must have 3 parts")

	// Verify header has alg: ES256.
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	var header map[string]any
	require.NoError(t, json.Unmarshal(headerJSON, &header))
	assert.Equal(t, "ES256", header["alg"])

	// Verify payload has the right claims.
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(payloadJSON, &payload))
	assert.Equal(t, "clawker-cli", payload["iss"])
	assert.Equal(t, "clawker-cli", payload["sub"])
	assert.Equal(t, "http://127.0.0.1:4444/oauth2/token", payload["aud"])
	assert.Equal(t, "test-jti", payload["jti"])

	// Verify signature using go-jose (handles JWS R||S format).
	tok, err := josejwt.ParseSigned(signed, []jose.SignatureAlgorithm{jose.ES256})
	require.NoError(t, err)
	var verified map[string]any
	require.NoError(t, tok.Claims(&key.PublicKey, &verified),
		"signature must verify against signing key")
}

func TestBuildSignedAssertion_DifferentJTIs(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	claims := AssertionClaims{
		Issuer:           "clawker-cli",
		Subject:          "clawker-cli",
		Audience:         "http://127.0.0.1:4444/oauth2/token",
		JWTID:            "jti-1",
		ExpiresInSeconds: 30,
	}

	first, err := BuildSignedAssertion(claims, key)
	require.NoError(t, err)

	claims.JWTID = "jti-2"
	second, err := BuildSignedAssertion(claims, key)
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
}
