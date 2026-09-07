package firewall_test

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/controlplane/sdscerts"
	"github.com/schmitthub/clawker/internal/config"
)

// currentUserOwner is the domain-cert reader identity tests hand to the cert
// writers: an unprivileged runner can only chown to itself.
func currentUserOwner() sdscerts.FileOwner {
	return sdscerts.FileOwner{UID: os.Geteuid(), GID: os.Getegid()}
}

// ownerOf returns the numeric UID and GID of path from the inode.
func ownerOf(t *testing.T, path string) (int, int) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	st, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok, "inode owner needs a unix stat")
	return int(st.Uid), int(st.Gid)
}

// httpsRule is a minimal TLS-terminated rule: the only inputs the cert writer
// reads are dst and proto.
func httpsRule(dst string) config.EgressRule {
	return config.EgressRule{
		Dst:                   dst,
		Proto:                 "https",
		Port:                  "",
		Action:                "",
		PathRules:             nil,
		PathDefault:           "",
		InsecureSkipTLSVerify: false,
	}
}

// TestRegenerateDomainCerts_EnvoyReadableLayout pins the on-disk contract the
// Envoy sibling depends on. Envoy runs as consts.EnvoyUID, not as CP root, so
// each domain pair must be owned by the reader at 0600 and the certs directory
// must be traversable for the reader's group. Docker Desktop on macOS hides a
// wrong layout (its bind mounts flatten host ownership); Linux hosts do not —
// Envoy reads EACCES as an empty file and exits with "Failed to load
// incomplete private key".
func TestRegenerateDomainCerts_EnvoyReadableLayout(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)
	reader := currentUserOwner()

	rules := []config.EgressRule{httpsRule("registry.npmjs.org")}
	require.NoError(t, firewall.RegenerateDomainCerts(rules, certDir, caCert, caKey, reader))

	dirInfo, err := os.Stat(certDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), dirInfo.Mode().Perm(), "certs dir must be group-traversable")
	_, dirGID := ownerOf(t, certDir)
	assert.Equal(t, reader.GID, dirGID, "certs dir group must be the reader group")

	for _, name := range []string{"registry.npmjs.org-cert.pem", "registry.npmjs.org-key.pem"} {
		path := filepath.Join(certDir, name)
		fileInfo, statErr := os.Stat(path)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm(), name)
		uid, gid := ownerOf(t, path)
		assert.Equal(t, reader.UID, uid, name)
		assert.Equal(t, reader.GID, gid, name)
	}
}

// TestRegenerateDomainCerts_HealsPrivateCertsDir covers upgrade from a CP that
// created the directory 0700: MkdirAll leaves an existing mode alone, so the
// writer must chmod explicitly or the reader stays locked out.
func TestRegenerateDomainCerts_HealsPrivateCertsDir(t *testing.T) {
	certDir := filepath.Join(t.TempDir(), "certs")
	require.NoError(t, os.Mkdir(certDir, 0o700))
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	rules := []config.EgressRule{httpsRule("registry.npmjs.org")}
	require.NoError(t, firewall.RegenerateDomainCerts(rules, certDir, caCert, caKey, currentUserOwner()))

	dirInfo, err := os.Stat(certDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), dirInfo.Mode().Perm())
}

// TestRegenerateDomainCerts_OwnerFailureSurfaces pins two things: a chown the
// writer cannot perform is an error, not a silently root-owned file, and the
// failed pair is never published (no leaf, no temp file left behind).
func TestRegenerateDomainCerts_OwnerFailureSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can chown to any identity; the failure path needs an unprivileged runner")
	}
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	// Own group so the directory step passes; UID 0 so the file chown fails.
	reader := sdscerts.FileOwner{UID: 0, GID: os.Getegid()}
	rules := []config.EgressRule{httpsRule("registry.npmjs.org")}
	err = firewall.RegenerateDomainCerts(rules, certDir, caCert, caKey, reader)
	require.ErrorContains(t, err, "owner")

	assert.NoFileExists(t, filepath.Join(certDir, "registry.npmjs.org-key.pem"))
	assert.NoFileExists(t, filepath.Join(certDir, "registry.npmjs.org-cert.pem"))
	entries, err := os.ReadDir(certDir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp", "failed write must remove its temporary file")
	}
}

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

func TestGenerateDomainCert_CIDRNetworkSAN(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	// A CIDR dst mints ONE leaf whose only SAN is the network address (iPAddress),
	// never a dNSName. It cannot validate every in-range host — agent-side
	// verification is not the enforcement boundary; the cert exists to encrypt the
	// hop and enable MITM inspection.
	certPEM, _, err := firewall.GenerateDomainCert(caCert, caKey, "10.0.0.0/24")
	require.NoError(t, err)

	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	require.Len(t, cert.IPAddresses, 1)
	assert.True(t, cert.IPAddresses[0].Equal(net.ParseIP("10.0.0.0")), "SAN must be the network address")
	assert.Empty(t, cert.DNSNames, "a CIDR cert carries no dNSName SAN")
	assert.Equal(t, "10.0.0.0/24", cert.Subject.CommonName)

	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	_, err = cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	require.NoError(t, err, "CIDR leaf should verify against CA")
}

func TestRegenerateDomainCerts_CIDRFlatBasename(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	rules := []config.EgressRule{
		{Dst: "10.0.0.0/24", Proto: "https"},   // CIDR → flat basename (no "/")
		{Dst: "192.168.1.5", Proto: "https"},   // single IP → unfolded literal
		{Dst: "wss.example.com", Proto: "wss"}, // FQDN wss → unfolded, dots kept
	}
	require.NoError(t, firewall.RegenerateDomainCerts(rules, certDir, caCert, caKey, currentUserOwner()))

	// The CIDR's "/" folds to "_" so the cert is one flat file pair, never a
	// bogus subdirectory.
	assert.FileExists(t, filepath.Join(certDir, "10.0.0.0_24-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "10.0.0.0_24-key.pem"))
	assert.NoDirExists(t, filepath.Join(certDir, "10.0.0.0"))
	// IP and FQDN keep their literal basenames (no "/" to fold).
	assert.FileExists(t, filepath.Join(certDir, "192.168.1.5-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "wss.example.com-cert.pem"))
}

func TestRegenerateDomainCerts_AllTLSRules(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	rules := []config.EgressRule{
		{
			Dst:   "github.com",
			Proto: "https",
			// No PathRules — still gets a cert for TLS inspection.
		},
		{
			Dst:   "api.openai.com",
			Proto: "https",
			PathRules: []config.PathRule{
				{Path: "/v1/models", Action: "allow"},
			},
			PathDefault: "deny",
		},
		{
			Dst:   "storage.googleapis.com",
			Proto: "https",
			PathRules: []config.PathRule{
				{Path: "/download/*", Action: "allow"},
			},
			PathDefault: "deny",
		},
		{
			Dst:   "git.example.com",
			Proto: "ssh",
			Port:  "22",
			// SSH rules do NOT get certs.
		},
	}

	err = firewall.RegenerateDomainCerts(rules, certDir, caCert, caKey, currentUserOwner())
	require.NoError(t, err)

	// All TLS rules get certs — regardless of PathRules.
	assert.FileExists(t, filepath.Join(certDir, "github.com-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "github.com-key.pem"))
	assert.FileExists(t, filepath.Join(certDir, "api.openai.com-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "api.openai.com-key.pem"))
	assert.FileExists(t, filepath.Join(certDir, "storage.googleapis.com-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "storage.googleapis.com-key.pem"))

	// SSH rules do NOT get certs.
	assert.NoFileExists(t, filepath.Join(certDir, "git.example.com-cert.pem"))
	assert.NoFileExists(t, filepath.Join(certDir, "git.example.com-key.pem"))

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
			Proto: "https",
			PathRules: []config.PathRule{
				{Path: "/v1/models", Action: "allow"},
			},
			PathDefault: "deny",
		},
	}
	err = firewall.RegenerateDomainCerts(rules, certDir, oldCACert, oldCAKey, currentUserOwner())
	require.NoError(t, err)

	// Read old domain cert for comparison.
	oldDomainCertPEM, err := os.ReadFile(filepath.Join(certDir, "api.openai.com-cert.pem"))
	require.NoError(t, err)

	// Rotate.
	err = firewall.RotateCA(certDir, rules, currentUserOwner())
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

func TestGenerateDomainCert_WildcardSANs(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	certPEM, _, err := firewall.GenerateDomainCert(caCert, caKey, ".datadoghq.com")
	require.NoError(t, err)

	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	// CN should be the normalized domain (no leading dot).
	assert.Equal(t, "datadoghq.com", cert.Subject.CommonName)
	// SANs should include both apex and wildcard.
	assert.Contains(t, cert.DNSNames, "datadoghq.com")
	assert.Contains(t, cert.DNSNames, "*.datadoghq.com")
}

func TestGenerateSNICert_MultiLabelHostVerifies(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	// Host with multiple labels below the wildcard zone: the static [apex, *.apex] SAN pair
	// cannot cover it (RFC 6125 wildcards match one label), so the per-SNI
	// leaf must carry the exact name. One label below the zone would pass against the
	// wildcard pair and hide the regression (issue #500).
	certPEM, keyPEM, err := firewall.GenerateSNICert(caCert, caKey, "studio-api.prod.suno.com")
	require.NoError(t, err)
	require.NotEmpty(t, keyPEM)

	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.Equal(t, "studio-api.prod.suno.com", cert.Subject.CommonName)
	require.NoError(t, cert.VerifyHostname("studio-api.prod.suno.com"))
	require.Error(t, cert.VerifyHostname("other.prod.suno.com"), "per-SNI leaf must cover only the requested name")

	// Chain must verify against the firewall CA.
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	_, err = cert.Verify(x509.VerifyOptions{
		Roots:                     roots,
		DNSName:                   "",
		Intermediates:             nil,
		CurrentTime:               time.Time{},
		KeyUsages:                 nil,
		MaxConstraintComparisions: 0,
		CertificatePolicies:       nil,
	})
	require.NoError(t, err)
}

func TestGenerateSNICert_RejectsInvalidName(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	for _, bad := range []string{"", "*.suno.com", ".suno.com", "10.0.0.5", "bad host.com"} {
		_, _, err = firewall.GenerateSNICert(caCert, caKey, bad)
		require.Error(t, err, "SNI %q must be rejected", bad)
	}
}

func TestGenerateDomainCert_ExactDomainNoWildcardSAN(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	certPEM, _, err := firewall.GenerateDomainCert(caCert, caKey, "api.openai.com")
	require.NoError(t, err)

	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.Equal(t, "api.openai.com", cert.Subject.CommonName)
	assert.Equal(t, []string{"api.openai.com"}, cert.DNSNames, "exact domain should not get wildcard SAN")
}

func TestRegenerateDomainCerts_WildcardAndExactDedup(t *testing.T) {
	// Both ".example.com" (wildcard) and "example.com" (exact) should produce
	// exactly one cert file with wildcard SANs, regardless of input order.
	tests := []struct {
		name  string
		rules []config.EgressRule
	}{
		{
			name: "wildcard first",
			rules: []config.EgressRule{
				{Dst: ".claude.ai", Proto: "https"},
				{Dst: "claude.ai", Proto: "https"},
			},
		},
		{
			name: "exact first",
			rules: []config.EgressRule{
				{Dst: "claude.ai", Proto: "https"},
				{Dst: ".claude.ai", Proto: "https"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			certDir := t.TempDir()
			caCert, caKey, err := firewall.EnsureCA(certDir)
			require.NoError(t, err)

			err = firewall.RegenerateDomainCerts(tc.rules, certDir, caCert, caKey, currentUserOwner())
			require.NoError(t, err)

			// Only one cert file pair — normalized to "claude.ai".
			certPath := filepath.Join(certDir, "claude.ai-cert.pem")
			assert.FileExists(t, certPath)
			assert.FileExists(t, filepath.Join(certDir, "claude.ai-key.pem"))
			assert.NoFileExists(t, filepath.Join(certDir, ".claude.ai-cert.pem"))

			// Cert must have both apex and wildcard SANs.
			certPEM, err := os.ReadFile(certPath)
			require.NoError(t, err)
			block, _ := pem.Decode(certPEM)
			require.NotNil(t, block)
			cert, err := x509.ParseCertificate(block.Bytes)
			require.NoError(t, err)

			assert.Equal(t, "claude.ai", cert.Subject.CommonName)
			assert.Contains(t, cert.DNSNames, "claude.ai", "apex SAN required")
			assert.Contains(t, cert.DNSNames, "*.claude.ai", "wildcard SAN required")

			// Count cert files — should be exactly 1 pair (+ CA pair).
			entries, err := os.ReadDir(certDir)
			require.NoError(t, err)
			certFiles := 0
			for _, e := range entries {
				if !e.IsDir() && filepath.Ext(e.Name()) == ".pem" {
					certFiles++
				}
			}
			// 2 CA files + 2 domain files = 4.
			assert.Equal(t, 4, certFiles, "should have exactly 1 domain cert pair + CA pair")
		})
	}
}

func TestRegenerateDomainCerts_WildcardFilenames(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	rules := []config.EgressRule{
		{
			Dst:   ".datadoghq.com",
			Proto: "https",
			PathRules: []config.PathRule{
				{Path: "/api/v2/logs", Action: "allow"},
			},
			PathDefault: "deny",
		},
	}

	err = firewall.RegenerateDomainCerts(rules, certDir, caCert, caKey, currentUserOwner())
	require.NoError(t, err)

	// Files should use the normalized domain (no leading dot).
	assert.FileExists(t, filepath.Join(certDir, "datadoghq.com-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "datadoghq.com-key.pem"))
	// No hidden files with leading dot.
	assert.NoFileExists(t, filepath.Join(certDir, ".datadoghq.com-cert.pem"))
	assert.NoFileExists(t, filepath.Join(certDir, ".datadoghq.com-key.pem"))

	// Verify the cert has wildcard SANs.
	certPEM, err := os.ReadFile(filepath.Join(certDir, "datadoghq.com-cert.pem"))
	require.NoError(t, err)
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	assert.Contains(t, cert.DNSNames, "datadoghq.com")
	assert.Contains(t, cert.DNSNames, "*.datadoghq.com")
}

func TestRegenerateDomainCerts_CleansStaleCerts(t *testing.T) {
	certDir := t.TempDir()
	caCert, caKey, err := firewall.EnsureCA(certDir)
	require.NoError(t, err)

	// Generate certs for two domains.
	rules := []config.EgressRule{
		{Dst: "old-domain.com", Proto: "https"},
		{Dst: "kept-domain.com", Proto: "https"},
	}
	err = firewall.RegenerateDomainCerts(rules, certDir, caCert, caKey, currentUserOwner())
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(certDir, "old-domain.com-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "kept-domain.com-cert.pem"))

	// Regenerate with only the kept domain — stale cert should be cleaned.
	rules = []config.EgressRule{
		{Dst: "kept-domain.com", Proto: "https"},
	}
	err = firewall.RegenerateDomainCerts(rules, certDir, caCert, caKey, currentUserOwner())
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(certDir, "old-domain.com-cert.pem"))
	assert.NoFileExists(t, filepath.Join(certDir, "old-domain.com-key.pem"))
	assert.FileExists(t, filepath.Join(certDir, "kept-domain.com-cert.pem"))
	// CA files preserved.
	assert.FileExists(t, filepath.Join(certDir, "ca-cert.pem"))
	assert.FileExists(t, filepath.Join(certDir, "ca-key.pem"))
}
