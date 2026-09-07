package sdscerts_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/infracerts"
	"github.com/schmitthub/clawker/controlplane/sdscerts"
	"github.com/schmitthub/clawker/internal/consts"
)

// testIssuer writes a self-signed CA cert+key pair to dir and loads it
// as an infracerts.Issuer — the minimal stand-in for the production
// infra intermediate.
func testIssuer(t *testing.T, dir string) *infracerts.Issuer {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test infra intermediate",

			Country:            nil,
			Organization:       nil,
			OrganizationalUnit: nil,
			Locality:           nil,
			Province:           nil,
			StreetAddress:      nil,
			PostalCode:         nil,
			SerialNumber:       "",
			Names:              nil,
			ExtraNames:         nil,
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,

		Raw:                     nil,
		RawTBSCertificate:       nil,
		RawSubjectPublicKeyInfo: nil,
		RawSubject:              nil,
		RawIssuer:               nil,
		RawSignatureAlgorithm:   nil,
		Signature:               nil,
		SignatureAlgorithm:      x509.UnknownSignatureAlgorithm,
		PublicKeyAlgorithm:      x509.UnknownPublicKeyAlgorithm,
		PublicKey:               nil,
		Version:                 0,
		Issuer: pkix.Name{
			Country:            nil,
			Organization:       nil,
			OrganizationalUnit: nil,
			Locality:           nil,
			Province:           nil,
			StreetAddress:      nil,
			PostalCode:         nil,
			SerialNumber:       "",
			CommonName:         "",
			Names:              nil,
			ExtraNames:         nil,
		},
		Extensions:                  nil,
		ExtraExtensions:             nil,
		UnhandledCriticalExtensions: nil,
		ExtKeyUsage:                 nil,
		UnknownExtKeyUsage:          nil,
		MaxPathLen:                  0,
		MaxPathLenZero:              false,
		SubjectKeyId:                nil,
		AuthorityKeyId:              nil,
		OCSPServer:                  nil,
		IssuingCertificateURL:       nil,
		DNSNames:                    nil,
		EmailAddresses:              nil,
		IPAddresses:                 nil,
		URIs:                        nil,
		PermittedDNSDomainsCritical: false,
		PermittedDNSDomains:         nil,
		ExcludedDNSDomains:          nil,
		PermittedIPRanges:           nil,
		ExcludedIPRanges:            nil,
		PermittedEmailAddresses:     nil,
		ExcludedEmailAddresses:      nil,
		PermittedURIDomains:         nil,
		ExcludedURIDomains:          nil,
		CRLDistributionPoints:       nil,
		PolicyIdentifiers:           nil,
		Policies:                    nil,
		InhibitAnyPolicy:            0,
		InhibitAnyPolicyZero:        false,
		InhibitPolicyMapping:        0,
		InhibitPolicyMappingZero:    false,
		RequireExplicitPolicy:       0,
		RequireExplicitPolicyZero:   false,
		PolicyMappings:              nil,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	certPath := filepath.Join(dir, "ca.pem")
	keyPath := filepath.Join(dir, "ca.key")
	certPEM := pem.EncodeToMemory(
		&pem.Block{
			Type: "CERTIFICATE", Bytes: der,
			Headers: nil,
		},
	)
	require.NoError(t, os.WriteFile(certPath, certPEM, 0o600))
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(
		&pem.Block{
			Type: "EC PRIVATE KEY", Bytes: keyDER,
			Headers: nil,
		},
	)
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	issuer, err := infracerts.Load(certPath, keyPath)
	require.NoError(t, err)
	return issuer
}

// TestService_EnsureEnvoyClient_MintsDedicatedSDSIdentity pins the lane
// contract: the written leaf carries the dedicated SDS identity as its
// SAN — NOT the telemetry lane's "<svc>-otel-client" naming — so the CP
// server's SAN pin admits it and refuses the otel leaf.
func TestService_EnsureEnvoyClient_MintsDedicatedSDSIdentity(t *testing.T) {
	dir := t.TempDir()
	issuer := testIssuer(t, dir)
	rootCA := []byte("-----BEGIN CERTIFICATE-----\nfake-root-for-copy-check\n-----END CERTIFICATE-----\n")

	svc, err := sdscerts.New(
		issuer,
		filepath.Join(dir, "sds-clients"),
		rootCA,
		sdscerts.FileOwner{UID: os.Geteuid(), GID: os.Getegid()},
	)
	require.NoError(t, err)

	certPath, keyPath, caPath, err := svc.EnsureEnvoyClient()
	require.NoError(t, err)

	chainPEM, err := os.ReadFile(certPath)
	require.NoError(t, err)
	keyPEM, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	_, err = tls.X509KeyPair(chainPEM, keyPEM)
	require.NoError(t, err, "written pair must parse together")

	block, _ := pem.Decode(chainPEM)
	require.NotNil(t, block)
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	assert.Equal(t, []string{consts.EnvoySDSClientName}, leaf.DNSNames, "leaf SAN must be the dedicated SDS identity")
	assert.Equal(t, consts.EnvoySDSClientName, leaf.Subject.CommonName)
	assert.Contains(t, leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth)

	gotCA, err := os.ReadFile(caPath)
	require.NoError(t, err)
	assert.Equal(t, rootCA, gotCA, "ca.pem must be the verbatim root CA handed to New")
}

// TestService_EnsureEnvoyClient_OverwritesInPlace pins re-run behavior:
// a second mint replaces the pair (fresh leaf) without erroring — the
// reload path re-provisions on every ensureConfigs.
func TestService_EnsureEnvoyClient_OverwritesInPlace(t *testing.T) {
	dir := t.TempDir()
	svc, err := sdscerts.New(
		testIssuer(t, dir),
		filepath.Join(dir, "sds-clients"),
		[]byte("root"),
		sdscerts.FileOwner{UID: os.Geteuid(), GID: os.Getegid()},
	)
	require.NoError(t, err)

	certPath, _, _, err := svc.EnsureEnvoyClient()
	require.NoError(t, err)
	first, err := os.ReadFile(certPath)
	require.NoError(t, err)

	certPath2, keyPath2, caPath2, err := svc.EnsureEnvoyClient()
	require.NoError(t, err)
	assert.Equal(t, certPath, certPath2, "re-mint must keep the same paths")
	assert.FileExists(t, keyPath2)
	assert.FileExists(t, caPath2)
	second, err := os.ReadFile(certPath2)
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "re-mint must write a fresh leaf")
}

func TestNew_RejectsMissingDeps(t *testing.T) {
	dir := t.TempDir()
	issuer := testIssuer(t, dir)

	_, err := sdscerts.New(nil, dir, []byte("root"), sdscerts.FileOwner{UID: os.Geteuid(), GID: os.Getegid()})
	require.Error(t, err)
	_, err = sdscerts.New(issuer, "", []byte("root"), sdscerts.FileOwner{UID: os.Geteuid(), GID: os.Getegid()})
	require.Error(t, err)
	_, err = sdscerts.New(issuer, dir, nil, sdscerts.FileOwner{UID: os.Geteuid(), GID: os.Getegid()})
	require.Error(t, err)
}
