package controlplane

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadOrGenerateTLSMaterial_FreshDir verifies that calling the
// materializer against an empty directory produces a full working set
// of CA + server cert + client cert + OIDC signing key, and that the
// files land at the expected on-disk paths.
func TestLoadOrGenerateTLSMaterial_FreshDir(t *testing.T) {
	dir := t.TempDir()

	mat, err := LoadOrGenerateTLSMaterial(dir)
	require.NoError(t, err)
	require.NotNil(t, mat)

	// Every returned pointer should be populated.
	assert.NotNil(t, mat.CACert)
	assert.NotNil(t, mat.CAKey)
	assert.NotNil(t, mat.ServerCert)
	assert.NotEmpty(t, mat.ServerCertDER)
	assert.NotNil(t, mat.ServerKey)
	assert.NotNil(t, mat.ClientCLICert)
	assert.NotEmpty(t, mat.ClientCLICertDER)
	assert.NotNil(t, mat.ClientCLIKey)
	assert.NotNil(t, mat.OIDCSigningKey)

	// CA file + key land in the data dir.
	_, err = os.Stat(filepath.Join(dir, cpCACertFile))
	assert.NoError(t, err, "ca.pem should exist")
	_, err = os.Stat(filepath.Join(dir, cpCAKeyFile))
	assert.NoError(t, err, "ca.key should exist")

	// OIDC signing key exists.
	_, err = os.Stat(filepath.Join(dir, cpOIDCSigningKey))
	assert.NoError(t, err, "oidc signing key should exist")

	// Cert dir + client + server certs land in the certs subdir.
	certsDir := filepath.Join(dir, cpCertsDir)
	_, err = os.Stat(certsDir)
	assert.NoError(t, err, "certs dir should exist")
	_, err = os.Stat(filepath.Join(certsDir, cpServerCertFile))
	assert.NoError(t, err, "server cert should exist")
	_, err = os.Stat(filepath.Join(certsDir, cpClientCLICertFile))
	assert.NoError(t, err, "cli client cert should exist")
	_, err = os.Stat(filepath.Join(certsDir, cpClientCACertFile))
	assert.NoError(t, err, "client-facing ca cert should exist")
}

// TestLoadOrGenerateTLSMaterial_CAPersists checks that a second call
// against the same directory reuses the CA on disk (rather than
// generating a fresh one) while the leaf certs are re-issued.
func TestLoadOrGenerateTLSMaterial_CAPersists(t *testing.T) {
	dir := t.TempDir()

	first, err := LoadOrGenerateTLSMaterial(dir)
	require.NoError(t, err)

	second, err := LoadOrGenerateTLSMaterial(dir)
	require.NoError(t, err)

	// CA must match across calls (same SKI, same serial).
	assert.Equal(t, first.CACert.SerialNumber.String(),
		second.CACert.SerialNumber.String(),
		"CA should persist across calls")
	assert.Equal(t, first.CACert.Raw, second.CACert.Raw,
		"CA raw bytes should match across calls")

	// Leaf certs regenerate on every call, so serials differ.
	assert.NotEqual(t, first.ServerCert.SerialNumber.String(),
		second.ServerCert.SerialNumber.String(),
		"server cert should regenerate on each call")
	assert.NotEqual(t, first.ClientCLICert.SerialNumber.String(),
		second.ClientCLICert.SerialNumber.String(),
		"client cert should regenerate on each call")
}

// TestIssuedCertsAreSignedByCA verifies that x509.Verify accepts the
// server and client certs against the issuing CA. This is the load-
// bearing guarantee — if verify fails, the CP's mTLS listener won't
// accept any client and the whole auth stack breaks.
func TestIssuedCertsAreSignedByCA(t *testing.T) {
	dir := t.TempDir()

	mat, err := LoadOrGenerateTLSMaterial(dir)
	require.NoError(t, err)

	roots := x509.NewCertPool()
	roots.AddCert(mat.CACert)

	t.Run("server cert validates", func(t *testing.T) {
		_, err := mat.ServerCert.Verify(x509.VerifyOptions{
			Roots:     roots,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			DNSName:   "clawker-cp",
		})
		assert.NoError(t, err)
	})

	t.Run("client cert validates", func(t *testing.T) {
		_, err := mat.ClientCLICert.Verify(x509.VerifyOptions{
			Roots:     roots,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		})
		assert.NoError(t, err)
	})

	t.Run("client cert CN is clawker-cli", func(t *testing.T) {
		// The authz interceptor cross-checks the peer cert CN against
		// the JWT subject. If this CN drifts from ClientIDCLI, the
		// interceptor rejects every CLI call.
		assert.Equal(t, ClientIDCLI, mat.ClientCLICert.Subject.CommonName)
	})
}
