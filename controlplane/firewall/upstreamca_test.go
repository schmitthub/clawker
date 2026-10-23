package firewall_test

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/firewall"
)

func TestWriteUpstreamCABundle(t *testing.T) {
	// Roots removed from the Mozilla root store after the eMudhra intermediate
	// CA compromise (October 2026). Every public root program and OS trust
	// store dropped them; the Envoy image bundle did not. The generated
	// upstream bundle must never carry them again.
	distrustedRootFingerprints := map[string]string{
		"D7A7A0FB5D7E2731D771E9484EBCDEF71D5F0C3E0A2948782BC83EE0EA699EF4": "AAA Certificate Services",
		"86A1ECBA089C4A8D3BBE2734C612BA341D813E043CF9E8A862CD5C57A36BBE6B": "emSign ECC Root CA - G3",
		"7DD78D5F4F13459A83DFF9ABBBA62EDBAF6F2D102BF257FD712F4D9F2746ED8D": "emSign Root TLS CA - G3",
		"F2EDAAFF3D9B701FB4D14C9F285F8C0BAD90FC3BCC1FA07D47F4718EDABA34A5": "emSign Root CS CA - G3",
	}

	certDir := t.TempDir()
	require.NoError(t, firewall.WriteUpstreamCABundle(certDir, currentUserOwner()))

	path := filepath.Join(certDir, firewall.UpstreamCABundleFile)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "bundle keeps the cert-file mode")

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var count int
	for rest := data; len(rest) > 0; {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		require.Equal(t, "CERTIFICATE", block.Type)
		cert, parseErr := x509.ParseCertificate(block.Bytes)
		require.NoError(t, parseErr)
		assert.True(t, cert.IsCA, "%s is not a CA certificate", cert.Subject.CommonName)
		sum := sha256.Sum256(cert.Raw)
		fp := strings.ToUpper(hex.EncodeToString(sum[:]))
		assert.NotContains(t, distrustedRootFingerprints, fp,
			"distrusted root %q is in the bundle", cert.Subject.CommonName)
		count++
	}
	assert.Greater(t, count, 100, "a Mozilla root store has well over 100 roots")
}

func TestWriteUpstreamCABundle_MissingDir(t *testing.T) {
	err := firewall.WriteUpstreamCABundle(filepath.Join(t.TempDir(), "absent"), currentUserOwner())
	require.Error(t, err)
}
