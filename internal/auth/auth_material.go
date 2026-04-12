// Package auth provides CLI-side authentication infrastructure for
// communicating with the clawker control plane. The CLI is the trust
// orchestrator — it generates all key material and bind-mounts the
// public halves into the CP container.
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
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Directory layout under <DataDir>/auth/:
//
//	cli/
//	  signing.key        ← ES256 private key (NEVER enters container)
//	  signing-jwk.json   ← public JWK (bind-mounted into CP for Hydra)
//	tls/
//	  server.pem         ← self-signed server cert (bind-mounted into CP)
//	  server.key         ← server private key (bind-mounted into CP)

// EnsureAuthMaterial checks for existing auth material and creates any
// that is missing. Idempotent — safe to call on every CLI invocation.
func EnsureAuthMaterial(dataDir string) error {
	if err := ensureSigningKey(dataDir); err != nil {
		return fmt.Errorf("signing key: %w", err)
	}
	if err := ensureServerCert(dataDir); err != nil {
		return fmt.Errorf("server cert: %w", err)
	}
	return nil
}

// --- Paths ---

func AuthDir(dataDir string) string { return filepath.Join(dataDir, "auth") }
func CLIDir(dataDir string) string  { return filepath.Join(dataDir, "auth", "cli") }
func SigningKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "cli", "signing.key")
}
func SigningJWKPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "cli", "signing-jwk.json")
}
func TLSDir(dataDir string) string { return filepath.Join(dataDir, "auth", "tls") }
func ServerCertPath(dataDir string) string {
	return filepath.Join(dataDir, "auth", "tls", "server.pem")
}
func ServerKeyPath(dataDir string) string { return filepath.Join(dataDir, "auth", "tls", "server.key") }

// --- Signing key (ES256 for private_key_jwt) ---

func ensureSigningKey(dataDir string) error {
	keyPath := SigningKeyPath(dataDir)
	jwkPath := SigningJWKPath(dataDir)

	if err := os.MkdirAll(CLIDir(dataDir), 0o755); err != nil {
		return err
	}

	// Check if key already exists.
	if _, err := os.Stat(keyPath); err == nil {
		if _, err := os.Stat(jwkPath); err == nil {
			return nil // both exist
		}
		// Key exists but JWK missing — regenerate JWK from key.
		key, err := loadECDSAKey(keyPath)
		if err != nil {
			return fmt.Errorf("load existing key: %w", err)
		}
		return writeJWK(jwkPath, &key.PublicKey)
	}

	// Generate fresh key pair.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	if err := writeECDSAKey(keyPath, key, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	if err := writeJWK(jwkPath, &key.PublicKey); err != nil {
		return fmt.Errorf("write jwk: %w", err)
	}
	return nil
}

// LoadSigningKey reads the CLI's ES256 private key.
func LoadSigningKey(dataDir string) (*ecdsa.PrivateKey, error) {
	return loadECDSAKey(SigningKeyPath(dataDir))
}

// --- Server TLS cert (self-signed) ---

func ensureServerCert(dataDir string) error {
	certPath := ServerCertPath(dataDir)
	keyPath := ServerKeyPath(dataDir)

	if err := os.MkdirAll(TLSDir(dataDir), 0o755); err != nil {
		return err
	}

	// Check if both files exist.
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return nil // both exist
		}
	}

	// Generate self-signed server cert.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "clawker-cp",
			Organization: []string{"clawker"},
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost", "clawker-cp"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("sign cert: %w", err)
	}

	if err := writeCert(certPath, certDER); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	if err := writeECDSAKey(keyPath, key, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	return nil
}

// --- PEM helpers ---

func loadECDSAKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decode %s: no PEM block", path)
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

func writeECDSAKey(path string, key *ecdsa.PrivateKey, mode os.FileMode) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), mode)
}

func writeCert(path string, der []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// --- JWK ---

func writeJWK(path string, pub *ecdsa.PublicKey) error {
	jwk := map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(pub.X.Bytes()),
		"y":   base64.RawURLEncoding.EncodeToString(pub.Y.Bytes()),
	}
	data, err := json.MarshalIndent(jwk, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ReadJWK reads the CLI's public signing key as raw JSON bytes.
func ReadJWK(dataDir string) (json.RawMessage, error) {
	return os.ReadFile(SigningJWKPath(dataDir))
}

// ServerTLSCert reads the server cert for TLS trust. The CLI uses this
// to verify the CP's identity when dialing gRPC.
func ServerTLSCert(dataDir string) (*x509.Certificate, error) {
	data, err := os.ReadFile(ServerCertPath(dataDir))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("server cert: no PEM block")
	}
	return x509.ParseCertificate(block.Bytes)
}
