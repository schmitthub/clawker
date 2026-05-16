package infracerts_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/controlplane/infracerts"
)

// testCA produces a self-signed root and an intermediate CA signed by
// the root, both with key files written under dir. Mirrors the
// production layout where the CLI mints these at bootstrap.
func testCA(t *testing.T, dir string) (rootCertPEM, intermediateCertPath, intermediateKeyPath string) {
	t.Helper()

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("root key: %v", err)
	}
	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		MaxPathLen:            1,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("sign root: %v", err)
	}
	rootCert, _ := x509.ParseCertificate(rootDER)
	rootCertPEMBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})

	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("intermediate key: %v", err)
	}
	interTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "test intermediate"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interTmpl, rootCert, &interKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("sign intermediate: %v", err)
	}

	interCertPath := filepath.Join(dir, "infra-ca.pem")
	if err := os.WriteFile(interCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: interDER}), 0o600); err != nil {
		t.Fatalf("write intermediate cert: %v", err)
	}
	interKeyDER, _ := x509.MarshalECPrivateKey(interKey)
	interKeyPath := filepath.Join(dir, "infra-ca.key")
	if err := os.WriteFile(interKeyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: interKeyDER}), 0o600); err != nil {
		t.Fatalf("write intermediate key: %v", err)
	}

	return string(rootCertPEMBytes), interCertPath, interKeyPath
}

func TestIssuer_MintClient_ChainVerifiesAgainstRoot(t *testing.T) {
	dir := t.TempDir()
	rootCertPEM, interCertPath, interKeyPath := testCA(t, dir)

	iss, err := infracerts.Load(interCertPath, interKeyPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	chainPEM, keyPEM, err := iss.MintClient("envoy-otel-client", time.Hour)
	if err != nil {
		t.Fatalf("MintClient: %v", err)
	}
	if len(chainPEM) == 0 || len(keyPEM) == 0 {
		t.Fatalf("empty PEM output")
	}

	// Parse the chain (leaf + intermediate).
	var certs []*x509.Certificate
	rest := chainPEM
	for {
		block, remainder := pem.Decode(rest)
		if block == nil {
			break
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("parse chain block: %v", err)
		}
		certs = append(certs, c)
		rest = remainder
	}
	if len(certs) != 2 {
		t.Fatalf("chain should have leaf + intermediate (2 certs), got %d", len(certs))
	}

	leaf := certs[0]
	if leaf.Subject.CommonName != "envoy-otel-client" {
		t.Errorf("leaf CN = %q, want envoy-otel-client", leaf.Subject.CommonName)
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("leaf EKU = %v, want [ClientAuth]", leaf.ExtKeyUsage)
	}

	// Verify leaf chains to root using only root + the bundled intermediate.
	rootPool := x509.NewCertPool()
	if !rootPool.AppendCertsFromPEM([]byte(rootCertPEM)) {
		t.Fatalf("append root to pool")
	}
	interPool := x509.NewCertPool()
	interPool.AddCert(certs[1])

	chains, err := leaf.Verify(x509.VerifyOptions{
		Roots:         rootPool,
		Intermediates: interPool,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		t.Fatalf("verify chain against root: %v", err)
	}
	if len(chains) == 0 {
		t.Fatalf("no valid chain found")
	}
}

func TestIssuer_Load_RejectsNonCACert(t *testing.T) {
	dir := t.TempDir()

	// Generate a leaf cert (not a CA) and try to Load it as intermediate.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(99),
		Subject:      pkix.Name{CommonName: "not-a-ca"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		// IsCA: false (default).
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	certPath := filepath.Join(dir, "leaf.pem")
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPath := filepath.Join(dir, "leaf.key")
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)

	if _, err := infracerts.Load(certPath, keyPath); err == nil {
		t.Fatal("Load accepted a non-CA cert; expected error")
	}
}
