package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"strings"
	"testing"
	"time"

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

// TestRotateAuthMaterial_RegeneratesInfraCA pins the infra intermediate
// CA in the rotation set. Without this, a regression that drops the
// infra CA cert/key from the removeIfExists list in RotateAuthMaterial
// would leave the old intermediate alive after the user thought they
// rotated everything — runtime mTLS leaves minted post-rotation would
// continue to chain to the stale intermediate, and the otel-collector
// would keep trusting them despite the supposed rotation.
func TestRotateAuthMaterial_RegeneratesInfraCA(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	certPath, err := consts.AuthInfraCACertPath()
	require.NoError(t, err)
	keyPath, err := consts.AuthInfraCAKeyPath()
	require.NoError(t, err)

	originalCert, err := os.ReadFile(certPath)
	require.NoError(t, err)
	originalKey, err := os.ReadFile(keyPath)
	require.NoError(t, err)

	require.NoError(t, RotateAuthMaterial(true))

	rotatedCert, err := os.ReadFile(certPath)
	require.NoError(t, err)
	rotatedKey, err := os.ReadFile(keyPath)
	require.NoError(t, err)

	assert.NotEqual(t, originalCert, rotatedCert,
		"infra intermediate CA cert must change after forced rotation — stale signer would still mint trusted leaves")
	assert.NotEqual(t, originalKey, rotatedKey,
		"infra intermediate CA key must change after forced rotation")
}

// Tests INV-B1-014 [unit]: Private keys have 0600 permissions after rotation.
func TestRotateAuthMaterial_Permissions(t *testing.T) {
	testenv.New(t)
	require.NoError(t, RotateAuthMaterial(true))

	assertKeyPerms(t)
	assertAuthDirPerms(t)
}

// assertAuthDirPerms pins the auth directory tree to 0o700 — the looser
// 0o644 OTEL keys depend on the parent dir being unreachable to other
// local users for defense-in-depth on Linux hosts where $XDG_DATA_HOME
// (e.g., ~/.local/share) defaults to 0o755.
func assertAuthDirPerms(t *testing.T) {
	t.Helper()
	const dirMode = os.FileMode(0o700)
	for _, c := range []struct {
		name   string
		pathFn func() (string, error)
	}{
		{"auth/ca", consts.AuthCADir},
		{"auth/cli", consts.AuthCLIDir},
		{"auth/tls", consts.AuthTLSDir},
		{"auth/otel", consts.AuthOtelDir},
	} {
		p, err := c.pathFn()
		require.NoError(t, err)
		info, statErr := os.Stat(p)
		require.NoError(t, statErr, "stat %s", c.name)
		assert.Equal(t, dirMode, info.Mode().Perm(), "%s (%s) must be %o", c.name, p, dirMode)
	}
}

// assertKeyPerms pins the perm contract for every private key auth
// material file. Host-only keys must be 0o600; the OTEL server key is
// 0o644 because the otel-collector container runs under a uid that
// varies by image and needs to read it after bind-mount. The CP OTEL
// client key stays 0o600 — the CP container runs as root and reads it
// fine. Defense-in-depth: the auth/ tree is 0o700 (assertAuthDirPerms).
func assertKeyPerms(t *testing.T) {
	t.Helper()
	const tightMode = os.FileMode(0o600)
	const otelMode = os.FileMode(0o644)
	for _, c := range []struct {
		name   string
		pathFn func() (string, error)
		want   os.FileMode
	}{
		{"CA key", consts.AuthCAKeyPath, tightMode},
		{"signing key", consts.AuthCLISigningKeyPath, tightMode},
		{"server key", consts.AuthServerKeyPath, tightMode},
		{"client key", consts.AuthCLIClientKeyPath, tightMode},
		{"otel server key", consts.AuthOtelServerKeyPath, otelMode},
		{"cp client key", consts.AuthCPClientKeyPath, tightMode},
		{"infra CA key", consts.AuthInfraCAKeyPath, tightMode},
	} {
		p, err := c.pathFn()
		require.NoError(t, err)
		info, statErr := os.Stat(p)
		require.NoError(t, statErr, "stat %s", c.name)
		assert.Equal(t, c.want, info.Mode().Perm(), "%s (%s) must be %o", c.name, p, c.want)
	}
}

// statusByName looks up an auth file status by its display name.
// Indexed access is fragile: any new entry shifts indices and
// silently breaks expiry assertions on unrelated files.
func statusByName(t *testing.T, status []AuthFileStatus, name string) AuthFileStatus {
	t.Helper()
	for _, s := range status {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("auth file status %q not found", name)
	return AuthFileStatus{}
}

func TestCheckAuthMaterial_ReportsStatus(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	status, err := CheckAuthMaterial()
	require.NoError(t, err)
	require.Len(t, status, 14)

	for _, s := range status {
		assert.True(t, s.Exists, "%s should exist", s.Name)
	}

	for _, name := range []string{
		"CA certificate",
		"Server certificate",
		"CLI client certificate",
		"OTEL server certificate",
		"CP client certificate",
		"Infra intermediate CA certificate",
	} {
		s := statusByName(t, status, name)
		assert.False(t, s.Expires.IsZero(), "%s should have expiry", name)
		assert.False(t, s.Expired, "%s should not be expired", name)
	}
}

func TestCheckAuthMaterial_MissingFiles(t *testing.T) {
	testenv.New(t)
	// Don't create any material — everything should be missing.

	status, err := CheckAuthMaterial()
	require.NoError(t, err)
	require.Len(t, status, 14)

	for _, s := range status {
		assert.False(t, s.Exists, "%s should not exist", s.Name)
	}
}

func TestEnsureAuthMaterial_PrivateKeyPermissions(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	assertKeyPerms(t)
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

func TestOtelServerCertSignedByCA(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	caCert, err := CACert()
	require.NoError(t, err)

	certPath, err := consts.AuthOtelServerCertPath()
	require.NoError(t, err)
	certPEM, err := os.ReadFile(certPath)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	_, err = cert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	require.NoError(t, err, "OTEL server cert must be signed by CLI CA")
	assert.Equal(t, "clawker-otel-collector", cert.Subject.CommonName)
	// SANs cover both Linux native (host.docker.internal → bridge) and
	// Docker Desktop. Both must verify or CP→collector dial breaks on
	// one of the platforms.
	assert.Contains(t, cert.DNSNames, "host.docker.internal")
	assert.Contains(t, cert.DNSNames, "localhost")
	// clawker-net dial path (Envoy ALS, CoreDNS otel plugin) verifies SNI
	// against this SAN — drop it and gRPC handshakes fail with
	// "certificate is valid for ..., not otel-collector".
	assert.Contains(t, cert.DNSNames, consts.MonitoringServiceOtelCollector)
}

// Tests that ensureOtelServerCert re-mints when the on-disk cert is
// missing a SAN that the current source declares. Without this drift
// detection a cert minted before a SAN was added would persist forever
// and silently break trusted peers that dial the newly-added name.
func TestEnsureOtelServerCert_RemintsOnSANDrift(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	certPath, err := consts.AuthOtelServerCertPath()
	require.NoError(t, err)

	// Overwrite the cert with one that has a strict subset of the
	// current SAN list (drops "otel-collector"). Reuse the CA so chain
	// verification still works; only the SANs differ.
	caCert, caKey, err := loadCA()
	require.NoError(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "clawker-otel-collector"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"host.docker.internal", "localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	require.NoError(t, err)
	require.NoError(t, writeCert(certPath, der))

	// Sanity: stale cert lacks the SAN the production list now requires.
	stalePEM, err := os.ReadFile(certPath)
	require.NoError(t, err)
	staleBlock, _ := pem.Decode(stalePEM)
	staleCert, err := x509.ParseCertificate(staleBlock.Bytes)
	require.NoError(t, err)
	require.NotContains(t, staleCert.DNSNames, consts.MonitoringServiceOtelCollector)

	require.NoError(t, ensureOtelServerCert())

	freshPEM, err := os.ReadFile(certPath)
	require.NoError(t, err)
	freshBlock, _ := pem.Decode(freshPEM)
	freshCert, err := x509.ParseCertificate(freshBlock.Bytes)
	require.NoError(t, err)
	assert.Contains(t, freshCert.DNSNames, consts.MonitoringServiceOtelCollector,
		"stale cert with missing SAN must be re-minted")
}

// TestEnsureInfraIntermediateCA_MigratesStaleKeyPerms pins the upgrade
// path in ensureInfraIntermediateCA: when an existing key on disk has
// permissive perms (e.g. 0o644 from an older clawker that minted the
// key with the wrong mode), the next EnsureAuthMaterial call must
// tighten it to 0o600 in place. Without the migration block the
// permissive perms would survive forever because the regen branch
// only fires on file absence — the signing key for runtime mTLS
// leaves would remain world-readable on the host indefinitely.
func TestEnsureInfraIntermediateCA_MigratesStaleKeyPerms(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	keyPath, err := consts.AuthInfraCAKeyPath()
	require.NoError(t, err)

	// Simulate the legacy state: same key material on disk but at
	// 0o644 (the permissions an older clawker would have left).
	require.NoError(t, os.Chmod(keyPath, 0o644))
	info, err := os.Stat(keyPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "precondition: stale perms applied")

	require.NoError(t, EnsureAuthMaterial(), "second EnsureAuthMaterial after stale perms must succeed")

	info, err = os.Stat(keyPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"stale 0o644 perms must be tightened to 0o600 on the next EnsureAuthMaterial — a world-readable infra CA signing key is a privilege boundary violation")
}

func TestCPClientCertSignedByCA(t *testing.T) {
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())

	caCert, err := CACert()
	require.NoError(t, err)

	certPath, err := consts.AuthCPClientCertPath()
	require.NoError(t, err)
	certPEM, err := os.ReadFile(certPath)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	_, err = cert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	require.NoError(t, err, "CP client cert must be signed by CLI CA")
	assert.Equal(t, consts.ContainerCP, cert.Subject.CommonName)
	assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
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
		Audience:         "http://" + consts.LoopbackIPv4 + ":4444/oauth2/token",
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
		Audience:         "http://" + consts.LoopbackIPv4 + ":4444/oauth2/token",
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
	assert.Equal(t, "http://"+consts.LoopbackIPv4+":4444/oauth2/token", payload["aud"])
	assert.Equal(t, "test-jti", payload["jti"])

	// Verify signature using go-jose (handles JWS R||S format).
	tok, err := josejwt.ParseSigned(signed, []jose.SignatureAlgorithm{jose.ES256})
	require.NoError(t, err)
	var verified map[string]any
	require.NoError(t, tok.Claims(&key.PublicKey, &verified),
		"signature must verify against signing key")
}

// TestBuildSignedAssertion_IATIsNow pins that iat is the mint clock with no
// backdate: callers wait until the CP clock has caught up to the host before
// exchanging, so a host-clock iat is already in the CP's past and fosite's
// zero-leeway (now >= iat) check passes without any minting-side fudge. exp
// stays a forward window from now; nbf is intentionally absent (fosite
// rejects a future nbf with the same zero leeway).
func TestBuildSignedAssertion_IATIsNow(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	const expiresIn = 30
	before := time.Now()
	signed, err := BuildSignedAssertion(AssertionClaims{
		Issuer:           "clawker-cli",
		Subject:          "clawker-cli",
		Audience:         "http://" + consts.LoopbackIPv4 + ":4444/oauth2/token",
		JWTID:            "test-jti",
		ExpiresInSeconds: expiresIn,
	}, key)
	require.NoError(t, err)
	after := time.Now()

	tok, err := josejwt.ParseSigned(signed, []jose.SignatureAlgorithm{jose.ES256})
	require.NoError(t, err)
	var claims josejwt.Claims
	require.NoError(t, tok.Claims(&key.PublicKey, &claims))

	require.NotNil(t, claims.IssuedAt)
	require.NotNil(t, claims.Expiry)
	require.Nil(t, claims.NotBefore, "nbf must be absent — a future nbf would itself trip fosite's zero-leeway check")

	iat := claims.IssuedAt.Time()
	// iat == now (no backdate), modulo NumericDate second-truncation: iat
	// floors to a whole Unix second, so allow a 1s slack below `before`.
	assert.False(t, iat.Before(before.Add(-time.Second)),
		"iat (%s) must be ~now, not backdated (before=%s)", iat, before)
	assert.False(t, iat.After(after),
		"iat (%s) must not be in the future (after=%s)", iat, after)

	// exp stays a forward window from now.
	exp := claims.Expiry.Time()
	assert.InDelta(t, before.Add(expiresIn*time.Second).Unix(), exp.Unix(), 5,
		"exp must be ~now+ExpiresInSeconds, got %s", exp)
}

// TestBuildSignedAssertion_HonorsInjectedNow pins the clock-injection seam:
// when AssertionClaims.Now is set, iat/exp anchor to it rather than the local
// wall clock. This seam exists only for deterministic test pinning — production
// never sets Now (it leaves it zero → time.Now() and instead waits for the CP
// clock to converge with the host before minting; see adminclient.Dial). The
// large injected offset below proves a regression that fell back to time.Now()
// would fail.
func TestBuildSignedAssertion_HonorsInjectedNow(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// A reference clock deliberately far from real now (simulating a large
	// host↔CP offset) so a regression that falls back to time.Now() fails.
	ref := time.Now().Add(73 * time.Hour).Truncate(time.Second)
	const expiresIn = 30

	signed, err := BuildSignedAssertion(AssertionClaims{
		Issuer:           "clawker-cli",
		Subject:          "clawker-cli",
		Audience:         "http://" + consts.LoopbackIPv4 + ":4444/oauth2/token",
		JWTID:            "test-jti",
		ExpiresInSeconds: expiresIn,
		Now:              ref,
	}, key)
	require.NoError(t, err)

	tok, err := josejwt.ParseSigned(signed, []jose.SignatureAlgorithm{jose.ES256})
	require.NoError(t, err)
	var claims josejwt.Claims
	require.NoError(t, tok.Claims(&key.PublicKey, &claims))

	require.NotNil(t, claims.IssuedAt)
	require.NotNil(t, claims.Expiry)
	// iat = ref; exp = ref + ExpiresInSeconds (both off ref, not now).
	assert.Equal(t, ref.Unix(), claims.IssuedAt.Time().Unix(),
		"iat must anchor to injected Now")
	assert.Equal(t, ref.Add(expiresIn*time.Second).Unix(), claims.Expiry.Time().Unix(),
		"exp must anchor to injected Now plus ExpiresInSeconds")
}

func TestBuildSignedAssertion_DifferentJTIs(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	claims := AssertionClaims{
		Issuer:           "clawker-cli",
		Subject:          "clawker-cli",
		Audience:         "http://" + consts.LoopbackIPv4 + ":4444/oauth2/token",
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
