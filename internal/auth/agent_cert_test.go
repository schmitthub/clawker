package auth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// caPaths runs EnsureAuthMaterial in an isolated test env and returns
// the resolved CA cert + key paths. Reuses the existing CA generation
// path so MintAgentCert is exercised against real CLI material rather
// than a hand-rolled test CA.
func caPaths(t *testing.T) (caCertPath, caKeyPath string) {
	t.Helper()
	testenv.New(t)
	require.NoError(t, EnsureAuthMaterial())
	caCert, err := consts.AuthCACertPath()
	require.NoError(t, err)
	caKey, err := consts.AuthCAKeyPath()
	require.NoError(t, err)
	return caCert, caKey
}

func TestMintAgentCert_HappyPath(t *testing.T) {
	caCertPath, caKeyPath := caPaths(t)

	const project, agent = "alpha", "bravo"
	got, err := MintAgentCert(caCertPath, caKeyPath, MustProjectSlug(project), MustAgentName(agent), "abc1234567890def")
	require.NoError(t, err)
	require.NotEmpty(t, got.CertPEM)
	require.NotEmpty(t, got.KeyPEM)

	// Thumbprint must match SHA-256 of the parsed cert's DER bytes —
	// the same value the CP recomputes from the peer cert at Connect.
	leaf := mustParse(t, got.CertPEM)
	want := sha256.Sum256(leaf.Raw)
	assert.Equal(t, want, got.Thumbprint)

	// CN is the deterministic clawkerd binary identity. The per-agent
	// AgentFullName lives in the urn:clawker:agent: URI SAN — keeping
	// it out of the CN frees long random docker.GenerateRandomName
	// output from x509's 64-byte CN limit.
	assert.Equal(t, consts.ContainerClawkerd, leaf.Subject.CommonName)

	// AgentFullName must be in a URI SAN, read back via
	// AgentFullNameFromCert. CP-side IdentityInterceptor + Register
	// handler key off this string.
	gotAgentFullName, err := AgentFullNameFromCert(leaf)
	require.NoError(t, err, "MintAgentCert must populate the agent URI SAN")
	assert.Equal(t, "clawker.alpha.bravo", gotAgentFullName)

	// Cert must verify against the CA — same trust chain the CP server
	// will use for ClientCAs at the agent listener.
	pool := mustCAPool(t, caCertPath)
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	assert.NoError(t, err)

	// PEM material must round-trip through tls.X509KeyPair (what the
	// agent's gRPC dialer will call).
	_, err = tls.X509KeyPair(got.CertPEM, got.KeyPEM)
	assert.NoError(t, err)

	// 24h-ish lifetime — generous slack absorbs clock skew between test
	// runs without making the assertion useless.
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	assert.Greater(t, lifetime, 23*time.Hour)
	assert.LessOrEqual(t, lifetime, 25*time.Hour)
}

func TestMintAgentCert_DistinctSerials(t *testing.T) {
	caCertPath, caKeyPath := caPaths(t)

	first, err := MintAgentCert(caCertPath, caKeyPath, MustProjectSlug("x"), MustAgentName("y"), "abc111")
	require.NoError(t, err)
	second, err := MintAgentCert(caCertPath, caKeyPath, MustProjectSlug("x"), MustAgentName("y"), "abc222")
	require.NoError(t, err)

	leaf1 := mustParse(t, first.CertPEM)
	leaf2 := mustParse(t, second.CertPEM)
	assert.NotEqual(t, leaf1.SerialNumber.String(), leaf2.SerialNumber.String(),
		"two mints for the same agent name must produce distinct serials")
	assert.NotEqual(t, first.Thumbprint, second.Thumbprint,
		"distinct certs must produce distinct thumbprints")
}

func TestMintAgentCert_EmptyAgentName(t *testing.T) {
	caCertPath, caKeyPath := caPaths(t)
	_, err := MintAgentCert(caCertPath, caKeyPath, MustProjectSlug("proj"), AgentName{}, "abc1234567890def")
	require.Error(t, err)
}

func TestMintAgentCert_EmptyProjectStillMints(t *testing.T) {
	// 2-segment naming case (empty project) is legitimate — match
	// docker.ContainerName behavior. CN remains the binary literal;
	// the agent SAN encodes the 2-segment "clawker.<agent>"
	// AgentFullName.
	caCertPath, caKeyPath := caPaths(t)
	got, err := MintAgentCert(caCertPath, caKeyPath, ProjectSlug{}, MustAgentName("solo"), "abc1234567890def")
	require.NoError(t, err)
	leaf := mustParse(t, got.CertPEM)
	assert.Equal(t, consts.ContainerClawkerd, leaf.Subject.CommonName)
	gotAgentFullName, err := AgentFullNameFromCert(leaf)
	require.NoError(t, err)
	assert.Equal(t, "clawker.solo", gotAgentFullName)
}

func TestMintAgentCert_MissingCAPaths(t *testing.T) {
	testenv.New(t)
	missing := filepath.Join(t.TempDir(), "nope.pem")
	_, err := MintAgentCert(missing, missing, MustProjectSlug("x"), MustAgentName("y"), "abc1234567890def")
	require.Error(t, err)
}

// TestBuildContainerSAN_RejectsNonHex pins the charset gate. Docker
// container IDs are lowercase hex; any other string is a producer-side
// bug that must surface here, not propagate into a malformed cert SAN.
func TestBuildContainerSAN_RejectsNonHex(t *testing.T) {
	for _, bad := range []string{
		"",              // empty
		"contains-dash", // dash
		"WITHCAPS",      // upper case
		"has space",     // space
		"slash/in/it",   // slash
		"nul\x00bytes",  // control char
	} {
		t.Run(bad, func(t *testing.T) {
			_, err := BuildContainerSAN(bad)
			require.Error(t, err, "BuildContainerSAN must reject non-hex container IDs")
		})
	}
	// Valid hex must succeed.
	u, err := BuildContainerSAN("abc123def456")
	require.NoError(t, err)
	require.NotNil(t, u)
}

// TestMintAgentCert_AdversarialCAInputs exercises the malformed-input
// surface of the CA loader. Each subtest produces a CA file pair that
// real-world misconfiguration could write (mismatched pair, garbage
// PEM, RSA where ECDSA is required) and asserts MintAgentCert errors
// cleanly without panicking.
func TestMintAgentCert_AdversarialCAInputs(t *testing.T) {
	t.Run("mismatched CA pair (cert from one CA, key from another)", func(t *testing.T) {
		// Two independently generated CAs. Pairing CA-A's cert with
		// CA-B's key would otherwise produce a leaf signed by key K
		// whose issuer is a CA cert holding a different public key —
		// silent misconfiguration that surfaces only as opaque mTLS
		// failure later. loadCAFrom rejects the mismatch up front.
		dir := t.TempDir()
		certA, _ := writeCAPair(t, dir, "a")
		_, keyB := writeCAPair(t, dir, "b")

		_, err := MintAgentCert(certA, keyB, MustProjectSlug("x"), MustAgentName("y"), "abc1234567890def")
		require.Error(t, err, "mismatched CA pair must fail")
		assert.Contains(t, err.Error(), "matching pair")
	})

	t.Run("CA cert is not PEM", func(t *testing.T) {
		// pem.Decode returns nil block for garbage; loadCAFrom must
		// surface that as an error rather than dereferencing the nil
		// block.
		dir := t.TempDir()
		certPath := filepath.Join(dir, "garbage.pem")
		require.NoError(t, os.WriteFile(certPath, []byte("not a pem block at all"), 0o600))

		_, keyPath := writeCAPair(t, dir, "valid")
		_, err := MintAgentCert(certPath, keyPath, MustProjectSlug("x"), MustAgentName("y"), "abc1234567890def")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PEM")
	})

	t.Run("CA key is not P-256 ECDSA", func(t *testing.T) {
		// loadECDSAKey rejects non-EC keys. Use RSA to confirm the
		// type-narrowing path returns an error without panicking on
		// the failed type assertion.
		dir := t.TempDir()
		certPath, _ := writeCAPair(t, dir, "valid")

		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		rsaDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
		require.NoError(t, err)
		rsaKeyPath := filepath.Join(dir, "rsa.key.pem")
		require.NoError(t, os.WriteFile(rsaKeyPath,
			pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER}),
			0o600))

		_, err = MintAgentCert(certPath, rsaKeyPath, MustProjectSlug("x"), MustAgentName("y"), "abc1234567890def")
		require.Error(t, err)
	})
}

// TestAgentFullNameFromCert_TriState pins the three return states the
// CP-side IdentityInterceptor relies on to emit
// agent_identity_no_agent_san vs agent_identity_malformed_agent_san
// as distinct structured-log events. Wire envelope is uniform
// PermissionDenied either way; structured-log differentiation is what
// lets operators triage missing-binding (cert was never minted with a
// SAN) vs producer-side bug (cert minted with an empty tail).
func TestAgentFullNameFromCert_TriState(t *testing.T) {
	t.Run("nil cert → missing", func(t *testing.T) {
		_, err := AgentFullNameFromCert(nil)
		require.ErrorIs(t, err, ErrAgentSANMissing)
	})

	t.Run("cert without any URI SANs → missing", func(t *testing.T) {
		cert := &x509.Certificate{}
		_, err := AgentFullNameFromCert(cert)
		require.ErrorIs(t, err, ErrAgentSANMissing)
	})

	t.Run("cert with unrelated URI SAN → missing", func(t *testing.T) {
		u, parseErr := url.Parse("https://example.com/other")
		require.NoError(t, parseErr)
		cert := &x509.Certificate{URIs: []*url.URL{u}}
		_, err := AgentFullNameFromCert(cert)
		require.ErrorIs(t, err, ErrAgentSANMissing)
	})

	t.Run("cert with agent SAN scheme but empty tail → malformed", func(t *testing.T) {
		u, parseErr := url.Parse(AgentSANScheme)
		require.NoError(t, parseErr)
		cert := &x509.Certificate{URIs: []*url.URL{u}}
		_, err := AgentFullNameFromCert(cert)
		require.ErrorIs(t, err, ErrAgentSANMalformed)
	})

	t.Run("cert with well-formed agent SAN → no error", func(t *testing.T) {
		u, parseErr := url.Parse(AgentSANScheme + "clawker.proj.dev")
		require.NoError(t, parseErr)
		cert := &x509.Certificate{URIs: []*url.URL{u}}
		name, err := AgentFullNameFromCert(cert)
		require.NoError(t, err)
		assert.Equal(t, "clawker.proj.dev", name)
	})
}

// TestAgentCert_RedactsViaFormatter pins the redaction contract: every
// fmt verb that could surface struct contents must emit the literal
// "<redacted>" sentinel, never any of the byte fields. This is the
// guard that keeps zerolog and ad-hoc fmt.Sprintf calls from leaking
// the per-agent private key.
func TestAgentCert_RedactsViaFormatter(t *testing.T) {
	cert := AgentCert{
		CertPEM:    []byte("---PRIVATE-CERT-MATERIAL---"),
		KeyPEM:     []byte("---PRIVATE-KEY-MATERIAL---"),
		Thumbprint: sha256.Sum256([]byte("dummy")),
	}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		t.Run(verb, func(t *testing.T) {
			out := fmt.Sprintf(verb, cert)
			assert.Contains(t, out, "<redacted>", "formatter %q must include redaction marker", verb)
			assert.NotContains(t, out, "PRIVATE-CERT", "formatter %q leaked CertPEM", verb)
			assert.NotContains(t, out, "PRIVATE-KEY", "formatter %q leaked KeyPEM", verb)
		})
	}

	// Pointer-form must redact too — fmt promotes through method sets.
	out := fmt.Sprintf("%v", &cert)
	assert.Contains(t, out, "<redacted>")
	assert.NotContains(t, out, "PRIVATE-KEY")
}

func mustParse(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return leaf
}

func mustCAPool(t *testing.T, caCertPath string) *x509.CertPool {
	t.Helper()
	data, err := os.ReadFile(caCertPath)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(data), "CA cert must parse")
	return pool
}

// writeCAPair generates a self-signed P-256 CA in dir under tag and
// returns the cert + key paths. Used by adversarial subtests that need
// independently-generated CAs to forge mismatched-pair scenarios.
func writeCAPair(t *testing.T, dir, tag string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca-" + tag},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	certPath = filepath.Join(dir, tag+".cert.pem")
	keyPath = filepath.Join(dir, tag+".key.pem")

	var certBuf bytes.Buffer
	require.NoError(t, pem.Encode(&certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
	require.NoError(t, os.WriteFile(certPath, certBuf.Bytes(), 0o600))

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	var keyBuf bytes.Buffer
	require.NoError(t, pem.Encode(&keyBuf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	require.NoError(t, os.WriteFile(keyPath, keyBuf.Bytes(), 0o600))

	return certPath, keyPath
}
