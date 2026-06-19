package auth

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

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// writeTestCACert writes a self-signed CA cert PEM to a temp file and returns
// its path.
func writeTestCACert(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return path
}

// TestNewOryStack_BuildsSingleCAPool asserts the constructor builds the single
// CP CA pool / TLS config from the CA cert up front and exposes them via the
// CATLS / CACertPool accessors with the pinned ServerName and TLS 1.3 floor.
// This is the load-bearing cert invariant the gRPC stack reuses — a regression
// here means a second/empty pool or wrong ServerName, breaking mTLS.
func TestNewOryStack_BuildsSingleCAPool(t *testing.T) {
	caPath := writeTestCACert(t)

	s, err := NewOryStack(configmocks.NewBlankConfig(), nil, caPath, "ignored.jwk", logger.Nop())
	if err != nil {
		t.Fatalf("NewOryStack: %v", err)
	}

	if s.CACertPool() == nil {
		t.Fatal("CACertPool() returned nil")
	}
	tlsCfg := s.CATLS()
	if tlsCfg == nil {
		t.Fatal("CATLS() returned nil")
	}
	if tlsCfg.RootCAs != s.CACertPool() {
		t.Error("CATLS().RootCAs is not the same pool as CACertPool() — expected a single shared pool")
	}
	if tlsCfg.ServerName != consts.ContainerCP {
		t.Errorf("ServerName = %q, want %q", tlsCfg.ServerName, consts.ContainerCP)
	}
	if tlsCfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = %d, want TLS 1.3 (%d)", tlsCfg.MinVersion, tls.VersionTLS13)
	}
}

// TestNewOryStack_MissingCACert asserts a missing CA cert fails closed with an
// error (never a panic) so the orchestrator treats it as a startup gate.
func TestNewOryStack_MissingCACert(t *testing.T) {
	s, err := NewOryStack(configmocks.NewBlankConfig(), nil, filepath.Join(t.TempDir(), "absent.pem"), "ignored.jwk", logger.Nop())
	if err == nil {
		t.Fatal("expected error for missing CA cert, got nil")
	}
	if s != nil {
		t.Error("expected nil OryStack on error")
	}
}

// TestNewOryStack_UnparseableCACert asserts a CA file with no valid PEM cert
// block fails closed with an error rather than a silently empty pool.
func TestNewOryStack_UnparseableCACert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.pem")
	if err := os.WriteFile(path, []byte("not a pem cert"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	s, err := NewOryStack(configmocks.NewBlankConfig(), nil, path, "ignored.jwk", logger.Nop())
	if err == nil {
		t.Fatal("expected error for unparseable CA cert, got nil")
	}
	if s != nil {
		t.Error("expected nil OryStack on error")
	}
}
