package auth

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
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

	const agentName = "clawker.alpha.bravo"
	got, err := MintAgentCert(caCertPath, caKeyPath, agentName)
	require.NoError(t, err)
	require.NotEmpty(t, got.CertPEM)
	require.NotEmpty(t, got.KeyPEM)

	// Lowercase-hex SHA-256 = 64 chars; hex.DecodeString rejects upper
	// case + non-hex by erroring, so a successful decode + length check
	// is the cheap way to lock down the format.
	require.Len(t, got.ThumbprintHex, 64)
	raw, err := hex.DecodeString(got.ThumbprintHex)
	require.NoError(t, err)
	require.Len(t, raw, sha256.Size)

	// Thumbprint must match SHA-256 of the parsed cert's DER bytes —
	// the same value the CP recomputes from the peer cert at Register.
	leaf := mustParse(t, got.CertPEM)
	sum := sha256.Sum256(leaf.Raw)
	assert.Equal(t, hex.EncodeToString(sum[:]), got.ThumbprintHex)

	// CN preserves the canonical agent name verbatim.
	assert.Equal(t, agentName, leaf.Subject.CommonName)

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

	first, err := MintAgentCert(caCertPath, caKeyPath, "clawker.x.y")
	require.NoError(t, err)
	second, err := MintAgentCert(caCertPath, caKeyPath, "clawker.x.y")
	require.NoError(t, err)

	leaf1 := mustParse(t, first.CertPEM)
	leaf2 := mustParse(t, second.CertPEM)
	assert.NotEqual(t, leaf1.SerialNumber.String(), leaf2.SerialNumber.String(),
		"two mints for the same agent name must produce distinct serials")
}

func TestMintAgentCert_EmptyAgentName(t *testing.T) {
	caCertPath, caKeyPath := caPaths(t)
	_, err := MintAgentCert(caCertPath, caKeyPath, "")
	require.Error(t, err)
}

func TestMintAgentCert_MissingCAPaths(t *testing.T) {
	testenv.New(t)
	missing := filepath.Join(t.TempDir(), "nope.pem")
	_, err := MintAgentCert(missing, missing, "clawker.x.y")
	require.Error(t, err)
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
