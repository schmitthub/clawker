package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

// AgentCert is the co-derived material produced by MintAgentCert: the
// PEM-encoded cert, its matching key, and the SHA-256 thumbprint over
// the cert DER. The three pieces are only meaningful as a unit — pairing
// a thumbprint with a different cert breaks the cert-swap defense at
// AgentService.Register.
//
// The String/GoString methods deliberately redact the contents so the
// struct (which carries the per-agent private key) can never leak via
// `%v`, `%+v`, `%#v`, or zerolog's interface logger. Callers that need
// the raw bytes must read the fields directly.
type AgentCert struct {
	CertPEM    []byte
	KeyPEM     []byte
	Thumbprint [sha256.Size]byte
}

// String redacts every field so AgentCert can never accidentally leak
// the per-agent private key via fmt.Sprintf("%v", cert) or zerolog.
func (AgentCert) String() string { return "AgentCert{<redacted>}" }

// GoString redacts so fmt.Sprintf("%#v", cert) (and any logger that
// uses Go-syntax representation) also does not leak KeyPEM.
func (AgentCert) GoString() string { return "AgentCert{<redacted>}" }

// MintAgentCert generates a per-agent mTLS leaf signed by the CLI CA at
// caCertPath/caKeyPath. The returned material is meant to be delivered
// to the agent container via tmpfs and never persisted on the host.
//
// CN is set to agentName verbatim (caller has already canonicalized it).
// The 24h lifetime is intentional — thumbprint pinning at Register makes
// longer-lived certs safe, but a tight ceiling caps the blast radius if
// a leaf leaks. Thumbprint is what the CLI announces to the CP via
// AnnounceAgent so the CP can reject any peer cert whose
// SHA-256(cert.Raw) doesn't match.
func MintAgentCert(caCertPath, caKeyPath, agentName string) (AgentCert, error) {
	if agentName == "" {
		return AgentCert{}, fmt.Errorf("agent name required")
	}

	caCert, caKey, err := loadCAFrom(caCertPath, caKeyPath)
	if err != nil {
		return AgentCert{}, fmt.Errorf("load CA: %w", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return AgentCert{}, fmt.Errorf("generate leaf key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return AgentCert{}, fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   agentName,
			Organization: []string{"clawker"},
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return AgentCert{}, fmt.Errorf("sign cert: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return AgentCert{}, fmt.Errorf("marshal leaf key: %w", err)
	}

	return AgentCert{
		CertPEM:    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		KeyPEM:     pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		Thumbprint: sha256.Sum256(certDER),
	}, nil
}

// loadCAFrom mirrors auth_material.go::loadCA but takes explicit paths
// so MintAgentCert is callable without going through the consts-driven
// resolution layer.
//
// Pair consistency is enforced: the loaded CA cert's public key must
// match the loaded CA private key. A silent mismatch would let
// MintAgentCert produce a leaf signed by key K whose issuer is a CA
// cert holding a different public key — the leaf chains to nothing,
// the CP rejects it at handshake, and the failure surfaces as an
// opaque mTLS error far from the actual misconfiguration.
func loadCAFrom(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA cert: %w", err)
	}
	block, _ := pem.Decode(certData)
	if block == nil {
		return nil, nil, fmt.Errorf("CA cert: no PEM block")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA cert: %w", err)
	}
	caKey, err := loadECDSAKey(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load CA key: %w", err)
	}
	caPub, ok := caCert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf("CA cert public key is not ECDSA")
	}
	if !caPub.Equal(&caKey.PublicKey) {
		return nil, nil, fmt.Errorf("CA cert and CA key do not form a matching pair")
	}
	return caCert, caKey, nil
}
