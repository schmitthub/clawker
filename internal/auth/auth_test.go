package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"strings"
	"testing"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// --- EnsureAuthMaterial ---

func TestEnsureAuthMaterial_CreatesFiles(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	for _, pathFn := range []func() (string, error){
		consts.AuthCACertPath, consts.AuthCAKeyPath,
		consts.AuthCLISigningKeyPath, consts.AuthCLISigningJWKPath,
		consts.AuthServerCertPath, consts.AuthServerKeyPath,
		consts.AuthCLIClientCertPath, consts.AuthCLIClientKeyPath,
	} {
		p, err := pathFn()
		require.NoError(t, err)
		_, statErr := os.Stat(p)
		assert.NoError(t, statErr, "expected file: %s", p)
	}
}

func TestEnsureAuthMaterial_Idempotent(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	keyPath, err := consts.AuthCLISigningKeyPath()
	require.NoError(t, err)
	first, err := os.ReadFile(keyPath)
	require.NoError(t, err)

	require.NoError(t, EnsureAuthMaterial())

	second, err := os.ReadFile(keyPath)
	require.NoError(t, err)

	assert.Equal(t, first, second, "signing key must not change on idempotent call")
}

// Tests INV-B1-014 [unit]: Running rotate with --force produces new files.
func TestRotateAuthMaterial_ForceRegeneratesFiles(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	// Capture original CA cert content.
	caPath, err := consts.AuthCACertPath()
	require.NoError(t, err)
	original, err := os.ReadFile(caPath)
	require.NoError(t, err)

	require.NoError(t, RotateAuthMaterial(true))

	rotated, err := os.ReadFile(caPath)
	require.NoError(t, err)

	assert.NotEqual(t, original, rotated, "CA cert must change after forced rotation")
}

// Tests INV-B1-014 [unit]: Running rotate without force preserves signing key.
func TestRotateAuthMaterial_PreservesSigningKeyWithoutForce(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	keyPath, err := consts.AuthCLISigningKeyPath()
	require.NoError(t, err)
	before, err := os.ReadFile(keyPath)
	require.NoError(t, err)

	require.NoError(t, RotateAuthMaterial(false))

	after, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "signing key must be preserved when forceSigningKey=false")
}

// Tests INV-B1-014 [unit]: Private keys have 0600 permissions after rotation.
func TestRotateAuthMaterial_Permissions(t *testing.T) {
	testenv.New(t)
	require.NoError(t, RotateAuthMaterial(true))

	for _, pathFn := range []func() (string, error){
		consts.AuthCAKeyPath, consts.AuthCLISigningKeyPath, consts.AuthServerKeyPath,
		consts.AuthCLIClientKeyPath,
	} {
		p, err := pathFn()
		require.NoError(t, err)
		info, statErr := os.Stat(p)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "%s must be 0600", p)
	}
}

func TestCheckAuthMaterial_ReportsStatus(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	status, err := CheckAuthMaterial()
	require.NoError(t, err)
	require.Len(t, status, 12)

	for _, s := range status {
		assert.True(t, s.Exists, "%s should exist", s.Name)
	}

	// Certificates should have expiry info.
	caCert := status[0]
	assert.Equal(t, "CA certificate", caCert.Name)
	assert.False(t, caCert.Expires.IsZero(), "CA cert should have expiry")
	assert.False(t, caCert.Expired, "CA cert should not be expired")

	serverCert := status[4]
	assert.Equal(t, "Server certificate", serverCert.Name)
	assert.False(t, serverCert.Expires.IsZero(), "server cert should have expiry")
	assert.False(t, serverCert.Expired, "server cert should not be expired")

	clientCert := status[6]
	assert.Equal(t, "CLI client certificate", clientCert.Name)
	assert.False(t, clientCert.Expires.IsZero(), "client cert should have expiry")
	assert.False(t, clientCert.Expired, "client cert should not be expired")
}

func TestCheckAuthMaterial_MissingFiles(t *testing.T) {
	testenv.New(t)
	// Don't create any material — everything should be missing.

	status, err := CheckAuthMaterial()
	require.NoError(t, err)
	require.Len(t, status, 12)

	for _, s := range status {
		assert.False(t, s.Exists, "%s should not exist", s.Name)
	}
}

func TestEnsureAuthMaterial_PrivateKeyPermissions(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	for _, pathFn := range []func() (string, error){
		consts.AuthCAKeyPath, consts.AuthCLISigningKeyPath, consts.AuthServerKeyPath,
		consts.AuthCLIClientKeyPath,
	} {
		p, err := pathFn()
		require.NoError(t, err)
		info, statErr := os.Stat(p)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "%s must be 0600", p)
	}
}

func TestLoadSigningKey(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	key, err := LoadSigningKey()
	require.NoError(t, err)
	assert.Equal(t, "P-256", key.Curve.Params().Name)
}

func TestCACert(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	cert, err := CACert()
	require.NoError(t, err)
	assert.Equal(t, "clawker CLI CA", cert.Subject.CommonName)
	assert.True(t, cert.IsCA)
}

func TestServerCertSignedByCA(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	caCert, err := CACert()
	require.NoError(t, err)

	certPath, err := consts.AuthServerCertPath()
	require.NoError(t, err)
	certPEM, err := os.ReadFile(certPath)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	serverCert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	_, err = serverCert.Verify(x509.VerifyOptions{Roots: pool})
	require.NoError(t, err, "server cert must be signed by CLI CA")
	assert.Equal(t, consts.ContainerCP, serverCert.Subject.CommonName)
	assert.Contains(t, serverCert.DNSNames, "localhost")
}

func TestClientCertSignedByCA(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	caCert, err := CACert()
	require.NoError(t, err)

	certPath, err := consts.AuthCLIClientCertPath()
	require.NoError(t, err)
	certPEM, err := os.ReadFile(certPath)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	clientCert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	_, err = clientCert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	require.NoError(t, err, "client cert must be signed by CLI CA")
	assert.Equal(t, "clawker-cli", clientCert.Subject.CommonName)
	assert.Contains(t, clientCert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
}

func TestLoadClientCert(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	cert, err := LoadClientCert()
	require.NoError(t, err)
	assert.NotEmpty(t, cert.Certificate)
}

func TestReadJWK(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	jwk, err := ReadJWK()
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
