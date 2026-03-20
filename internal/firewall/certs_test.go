package firewall_test

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureCA_CreatesNew(t *testing.T) {
	certDir := t.TempDir()

	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	assert.True(t, caCert.IsCA)
	assert.Equal(t, "Clawker Firewall CA", caCert.Subject.CommonName)
	assert.Equal(t, x509.KeyUsageCertSign|x509.KeyUsageCRLSign, caCert.KeyUsage)
	assert.NotNil(t, caKey)

	assert.FileExists(t, filepath.Join(certDir, "ca-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "ca-key.pem"))
}

func TestEnsureCA_LoadsExisting(t *testing.T) {
	certDir := t.TempDir()

	cert1, key1, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	cert2, key2, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	// Same CA loaded — serial numbers and keys must match.
	assert.Equal(t, cert1.SerialNumber, cert2.SerialNumber)
	assert.True(t, key1.Equal(key2))
}

func TestGenerateDomainCert_Valid(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	certPEM, keyPEM, err := firewall.GenerateDomainCert(caCert, caKey, "github.com")
	require.NoError(t, err)
	require.NotEmpty(t, certPEM)
	require.NotEmpty(t, keyPEM)

	// Parse the domain cert.
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	domainCert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.Equal(t, "github.com", domainCert.Subject.CommonName)
	assert.Contains(t, domainCert.DNSNames, "github.com")
	assert.False(t, domainCert.IsCA)
	assert.Contains(t, domainCert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)

	// Verify the cert chain: domain cert signed by our CA.
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	_, err = domainCert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	require.NoError(t, err, "domain cert should verify against CA")

	// Verify key PEM is a valid EC private key.
	keyBlock, _ := pem.Decode(keyPEM)
	require.NotNil(t, keyBlock)
	_, err = x509.ParseECPrivateKey(keyBlock.Bytes)
	require.NoError(t, err, "key PEM should be a valid EC private key")
}

func TestRegenerateDomainCerts_OnlyForPathRules(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	rules := []config.EgressRule{
		{
			Dst:   "github.com",
			Proto: "tls",
			// No PathRules — SNI passthrough, no cert needed.
		},
		{
			Dst:   "api.openai.com",
			Proto: "tls",
			PathRules: []config.PathRule{
				{Path: "/v1/models", Action: "allow"},
			},
			PathDefault: "deny",
		},
		{
			Dst:   "storage.googleapis.com",
			Proto: "tls",
			PathRules: []config.PathRule{
				{Path: "/download/*", Action: "allow"},
			},
			PathDefault: "deny",
		},
	}

	err = firewall.RegenerateDomainCerts(rules, certDir, caCert, caKey)
	require.NoError(t, err)

	// github.com should NOT have certs (no PathRules).
	assert.NoFileExists(t, filepath.Join(certDir, "github.com-cert.pem"))
	assert.NoFileExists(t, filepath.Join(certDir, "github.com-key.pem"))

	// api.openai.com SHOULD have certs (has PathRules).
	assert.FileExists(t, filepath.Join(certDir, "api.openai.com-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "api.openai.com-key.pem"))

	// storage.googleapis.com SHOULD have certs (has PathRules).
	assert.FileExists(t, filepath.Join(certDir, "storage.googleapis.com-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "storage.googleapis.com-key.pem"))

	// Verify one of the generated certs is valid and CA-signed.
	certPEM, err := os.ReadFile(filepath.Join(certDir, "api.openai.com-cert.pem"))
	require.NoError(t, err)
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	assert.Equal(t, "api.openai.com", cert.Subject.CommonName)

	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	_, err = cert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	require.NoError(t, err)
}

func TestRotateCA_RegeneratesAll(t *testing.T) {
	certDir := t.TempDir()

	// Create initial CA and domain certs.
	oldCACert, oldCAKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	rules := []config.EgressRule{
		{
			Dst:   "api.openai.com",
			Proto: "tls",
			PathRules: []config.PathRule{
				{Path: "/v1/models", Action: "allow"},
			},
			PathDefault: "deny",
		},
	}
	err = firewall.RegenerateDomainCerts(rules, certDir, oldCACert, oldCAKey)
	require.NoError(t, err)

	// Read old domain cert for comparison.
	oldDomainCertPEM, err := os.ReadFile(filepath.Join(certDir, "api.openai.com-cert.pem"))
	require.NoError(t, err)

	// Rotate.
	err = firewall.RotateCA(certDir, rules)
	require.NoError(t, err)

	// New CA should exist and be different.
	newCACert, _, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)
	assert.NotEqual(t, oldCACert.SerialNumber, newCACert.SerialNumber, "CA serial should change after rotation")

	// New domain cert should exist and be different.
	newDomainCertPEM, err := os.ReadFile(filepath.Join(certDir, "api.openai.com-cert.pem"))
	require.NoError(t, err)
	assert.NotEqual(t, oldDomainCertPEM, newDomainCertPEM, "domain cert should be regenerated")

	// New domain cert should verify against the NEW CA.
	block, _ := pem.Decode(newDomainCertPEM)
	require.NotNil(t, block)
	newDomainCert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	roots := x509.NewCertPool()
	roots.AddCert(newCACert)
	_, err = newDomainCert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	require.NoError(t, err, "new domain cert should verify against new CA")

	// Old CA should NOT verify new domain cert.
	oldRoots := x509.NewCertPool()
	oldRoots.AddCert(oldCACert)
	_, err = newDomainCert.Verify(x509.VerifyOptions{
		Roots:     oldRoots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	assert.Error(t, err, "new domain cert should NOT verify against old CA")
}
