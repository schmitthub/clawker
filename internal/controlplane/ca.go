// Package controlplane — CA, TLS cert, and OIDC signing key management.
//
// The clawker control plane runs as its own Certificate Authority for the
// purpose of authenticating gRPC callers (host CLI in v1, clawkerd/webui in
// follow-ups). This CA is DISTINCT from the firewall MITM CA at
// internal/firewall/certs.go — that one is Envoy's upstream TLS-inspection
// CA, which signs per-destination-domain certs for TLS interception. This
// one exists only to sign gRPC server and client certs for the CP's own
// listeners. Keeping them separate means compromising one doesn't cascade.
//
// File layout under <firewallDataDir>:
//
//	cp-ca.pem                — self-signed CA cert (ECDSA P-256)
//	cp-ca.key                — CA private key (0600)
//	cp-oidc-signing.pem      — OIDC JWT signing key (RSA 2048) PKCS#8 PEM
//	cp-oidc-signing.key      — OIDC private key (0600)
//	cp-certs/cp-server.pem   — server cert, regenerated every boot
//	cp-certs/cp-server.key   — server private key (0600)
//	cp-certs/cp-client-cli.pem — CLI client cert
//	cp-certs/cp-client-cli.key — CLI client private key (0640, host-user-owned)
//	cp-certs/cp-ca.pem       — CA cert (public half), copied for clients
//
// The CA and OIDC signing key persist across CP restarts (v1 does not
// auto-rotate — rotation is manual via `rm cp-ca.* && docker restart
// clawker-cp`). Server and client certs are regenerated on every boot: they
// are signed by the stable CA, so clients' cached CA trust continues to
// validate them, but short-lived cert material limits the damage window of
// any single leaked private key to one process lifetime.
package controlplane

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// File names under the firewall data directory.
const (
	cpCACertFile        = "cp-ca.pem"
	cpCAKeyFile         = "cp-ca.key"
	cpOIDCSigningKey    = "cp-oidc-signing.key"
	cpCertsDir          = "cp-certs"
	cpServerCertFile    = "cp-server.pem"
	cpServerKeyFile     = "cp-server.key"
	cpClientCLICertFile = "cp-client-cli.pem"
	cpClientCLIKeyFile  = "cp-client-cli.key"
	// Client-visible copy of the CA cert so dialers can pin without needing
	// access to the CA private key's directory.
	cpClientCACertFile = "cp-ca.pem"
)

// Subject common names baked into the certs.
const (
	cpCACommonName        = "Clawker Control Plane CA"
	cpServerCommonName    = "clawker-cp"
	cpClientCLICommonName = "clawker-cli"
)

// Validity windows. CA is long-lived (manual rotation), server/client certs
// regenerate every CP boot so they can be short-lived without introducing
// uptime risk.
const (
	cpCAValidYears     = 10
	cpLeafCertValidity = 90 * 24 * time.Hour // 90 days; regenerated per boot
)

// Filesystem modes. Private keys must be 0600; the CLI client key is 0640
// so the host user can read it even if the CP ran as a different uid.
const (
	privateKeyMode     = 0o600
	clientKeyMode      = 0o640
	publicCertMode     = 0o644
	certsDirMode       = 0o755
	oidcSigningKeyBits = 2048
)

// TLSMaterial holds all the cert + key material the CP needs at runtime.
// Returned by LoadOrGenerateTLSMaterial for the CP main to feed into both
// the gRPC UDS listener and the OIDC HTTPS UDS listener.
type TLSMaterial struct {
	// CACert is the CP CA used to sign and verify all mTLS certs.
	CACert *x509.Certificate
	// CAKey is the CA private key.
	CAKey *ecdsa.PrivateKey
	// ServerCert is the leaf cert both listeners present.
	ServerCert *x509.Certificate
	// ServerCertDER is the raw DER bytes (needed to build tls.Certificate).
	ServerCertDER []byte
	// ServerKey is the leaf private key.
	ServerKey *ecdsa.PrivateKey
	// ClientCLICert is the leaf cert issued for the CLI caller.
	ClientCLICert *x509.Certificate
	// ClientCLICertDER is the raw DER bytes.
	ClientCLICertDER []byte
	// ClientCLIKey is the CLI client private key.
	ClientCLIKey *ecdsa.PrivateKey
	// OIDCSigningKey is the RSA private key used to sign JWTs.
	OIDCSigningKey *rsa.PrivateKey
}

// LoadOrGenerateTLSMaterial materializes the full set of CP TLS artifacts
// under firewallDataDir, generating any that are missing. The CA and OIDC
// signing key persist; the server and CLI client certs are regenerated on
// every call so that each CP boot gets fresh leaf material.
//
// firewallDataDir is expected to already exist (the firewall manager
// creates it before starting the CP container); this function does not
// create the parent directory.
func LoadOrGenerateTLSMaterial(firewallDataDir string) (*TLSMaterial, error) {
	mat := &TLSMaterial{}

	caCert, caKey, err := loadOrGenerateCA(firewallDataDir)
	if err != nil {
		return nil, fmt.Errorf("ca: %w", err)
	}
	mat.CACert = caCert
	mat.CAKey = caKey

	signingKey, err := loadOrGenerateOIDCSigningKey(firewallDataDir)
	if err != nil {
		return nil, fmt.Errorf("oidc signing key: %w", err)
	}
	mat.OIDCSigningKey = signingKey

	certsDir := filepath.Join(firewallDataDir, cpCertsDir)
	if err := os.MkdirAll(certsDir, certsDirMode); err != nil {
		return nil, fmt.Errorf("create certs dir: %w", err)
	}

	serverCert, serverDER, serverKey, err := issueServerCert(caCert, caKey)
	if err != nil {
		return nil, fmt.Errorf("server cert: %w", err)
	}
	if err := writeCertAndKey(
		filepath.Join(certsDir, cpServerCertFile),
		filepath.Join(certsDir, cpServerKeyFile),
		serverDER, serverKey, privateKeyMode,
	); err != nil {
		return nil, fmt.Errorf("persist server cert: %w", err)
	}
	mat.ServerCert = serverCert
	mat.ServerCertDER = serverDER
	mat.ServerKey = serverKey

	clientCert, clientDER, clientKey, err := issueClientCert(caCert, caKey, cpClientCLICommonName)
	if err != nil {
		return nil, fmt.Errorf("cli client cert: %w", err)
	}
	if err := writeCertAndKey(
		filepath.Join(certsDir, cpClientCLICertFile),
		filepath.Join(certsDir, cpClientCLIKeyFile),
		clientDER, clientKey, clientKeyMode,
	); err != nil {
		return nil, fmt.Errorf("persist cli client cert: %w", err)
	}
	mat.ClientCLICert = clientCert
	mat.ClientCLICertDER = clientDER
	mat.ClientCLIKey = clientKey

	// Publish the CA cert alongside the client certs so the CLI has a
	// single directory to read from.
	if err := writeCert(filepath.Join(certsDir, cpClientCACertFile), caCert.Raw); err != nil {
		return nil, fmt.Errorf("publish ca cert: %w", err)
	}

	return mat, nil
}

// loadOrGenerateCA reads cp-ca.pem/cp-ca.key from dataDir, generating a
// fresh self-signed ECDSA P-256 CA if either file is missing.
func loadOrGenerateCA(dataDir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPath := filepath.Join(dataDir, cpCACertFile)
	keyPath := filepath.Join(dataDir, cpCAKeyFile)

	cert, key, err := loadECDSACertAndKey(certPath, keyPath)
	if err == nil {
		return cert, key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("read existing ca: %w", err)
	}

	// Missing — generate fresh.
	return generateCA(certPath, keyPath)
}

// generateCA creates a self-signed ECDSA P-256 CA cert and persists it.
func generateCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ca key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate ca serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   cpCACommonName,
			Organization: []string{"clawker"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(cpCAValidYears, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("sign ca cert: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca cert: %w", err)
	}

	if err := writeCert(certPath, certDER); err != nil {
		return nil, nil, fmt.Errorf("write ca cert: %w", err)
	}
	if err := writeECDSAKey(keyPath, key, privateKeyMode); err != nil {
		return nil, nil, fmt.Errorf("write ca key: %w", err)
	}
	return cert, key, nil
}

// issueServerCert signs a short-lived server cert valid for both the gRPC
// UDS listener and the OIDC HTTPS UDS listener. SANs include the container
// name (clawker-cp), localhost, and 127.0.0.1.
func issueServerCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (*x509.Certificate, []byte, *ecdsa.PrivateKey, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate server key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate server serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   cpServerCommonName,
			Organization: []string{"clawker"},
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.Add(cpLeafCertValidity),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{cpServerCommonName, "localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sign server cert: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse server cert: %w", err)
	}
	return cert, certDER, leafKey, nil
}

// issueClientCert signs a short-lived client cert for a named caller. The
// caller's name becomes the cert's Common Name and is used by the authz
// interceptor to cross-check against the JWT subject claim.
func issueClientCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, commonName string) (*x509.Certificate, []byte, *ecdsa.PrivateKey, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate client key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate client serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"clawker"},
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.Add(cpLeafCertValidity),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sign client cert: %w", err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse client cert: %w", err)
	}
	return cert, certDER, leafKey, nil
}

// loadOrGenerateOIDCSigningKey reads (or generates) the RSA key used to
// sign JWTs issued by the embedded OIDC provider.
func loadOrGenerateOIDCSigningKey(dataDir string) (*rsa.PrivateKey, error) {
	keyPath := filepath.Join(dataDir, cpOIDCSigningKey)

	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("decode %s: no PEM block", keyPath)
		}
		// Try PKCS#8 first (modern), fall back to PKCS#1.
		if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			if rsaKey, ok := parsed.(*rsa.PrivateKey); ok {
				return rsaKey, nil
			}
			return nil, fmt.Errorf("%s is not an RSA key", keyPath)
		}
		if rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("parse %s: unrecognized RSA private key format", keyPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", keyPath, err)
	}

	// Missing — generate fresh.
	key, err := rsa.GenerateKey(rand.Reader, oidcSigningKeyBits)
	if err != nil {
		return nil, fmt.Errorf("generate oidc signing key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal oidc signing key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(keyPath, pemBytes, privateKeyMode); err != nil {
		return nil, fmt.Errorf("write oidc signing key: %w", err)
	}
	return key, nil
}

// ---------------------------------------------------------------------------
// File helpers
// ---------------------------------------------------------------------------

func writeCert(path string, der []byte) error {
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return os.WriteFile(path, pemBytes, publicCertMode)
}

func writeECDSAKey(path string, key *ecdsa.PrivateKey, mode os.FileMode) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal ec key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	return os.WriteFile(path, pemBytes, mode)
}

func writeCertAndKey(certPath, keyPath string, certDER []byte, key *ecdsa.PrivateKey, keyMode os.FileMode) error {
	if err := writeCert(certPath, certDER); err != nil {
		return err
	}
	return writeECDSAKey(keyPath, key, keyMode)
}

// loadECDSACertAndKey reads an ECDSA leaf-or-CA cert + key pair from PEM
// files. Returns (nil, nil, os.ErrNotExist) wrapped if either file is
// missing so callers can short-circuit to "generate fresh".
func loadECDSACertAndKey(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}

	certBlock, _ := pem.Decode(certData)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("decode %s: no PEM block", certPath)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", certPath, err)
	}

	keyBlock, _ := pem.Decode(keyData)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("decode %s: no PEM block", keyPath)
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", keyPath, err)
	}
	return cert, key, nil
}
