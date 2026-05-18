// Package infracerts issues short-lived mTLS client certificates for
// clawker infrastructure services (Envoy, CoreDNS, clawker-cp's own
// trusted-lane OTLP exporter, future hostproxy observability sidecars,
// ...) using a CLI-provisioned intermediate CA.
//
// Trust chain:
//
//	CLI root CA (provisioned by `clawker auth` bootstrap — server-side
//	  │           anchor for clients verifying otel-collector's TLS cert)
//	  └── infra intermediate CA  (this package's signer)
//	        ├── envoy-otel-client    (minted via otelcerts.Service)
//	        ├── coredns-otel-client  (minted via otelcerts.Service)
//	        ├── cp-otel-client       (minted via otelcerts.Service)
//	        └── <future infra service>
//
// The intermediate is bind-mounted RO into the clawker-controlplane
// container; its private key never leaves the host CLI auth dir and
// the CP container. The otel-collector trusts the infra intermediate
// CA only on its `otlp/infra` receiver — agent leaves chained to the
// CLI root cannot complete the handshake. Leaves include the
// intermediate cert bundled in the PEM chain they present during
// handshake so the receiver can build a valid chain to the
// intermediate it trusts.
//
// Adding a new infra service is a CP-side change only — the CLI does
// not need to learn about it.
package infracerts

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

// Issuer signs short-lived mTLS client leaves for clawker infra
// services.
type Issuer struct {
	intermediate    *x509.Certificate
	key             *ecdsa.PrivateKey
	intermediatePEM []byte
}

// Load reads an intermediate CA cert + key from PEM files on disk.
// Returns an Issuer ready to mint client leaves. The intermediate
// must carry BasicConstraints CA=true; Load enforces this so a
// misprovisioned leaf cert cannot accidentally be used as a signer.
func Load(certPath, keyPath string) (*Issuer, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read intermediate cert: %w", err)
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("intermediate cert: no PEM block in %s", certPath)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse intermediate cert: %w", err)
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("intermediate cert %s is not a CA (BasicConstraints CA=false)", certPath)
	}
	if cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, fmt.Errorf("intermediate cert %s has KeyUsage extension without KeyCertSign", certPath)
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read intermediate key: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("intermediate key: no PEM block in %s", keyPath)
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse intermediate key: %w", err)
	}
	// Pair-check: x509.CreateCertificate happily signs leaves with a
	// mismatched key, but the produced chain won't verify at handshake
	// time. Catch the mismatch at Load so main.go's degrade path emits
	// event=infra_issuer_unavailable instead of stranding the bug as
	// opaque Envoy/CoreDNS TLS handshake failures at runtime.
	certPub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("intermediate cert %s public key is not ECDSA (got %T)", certPath, cert.PublicKey)
	}
	if !certPub.Equal(&key.PublicKey) {
		return nil, fmt.Errorf("intermediate key %s does not match cert %s public key", keyPath, certPath)
	}

	return &Issuer{
		intermediate:    cert,
		key:             key,
		intermediatePEM: certPEM,
	}, nil
}

// MintClient signs a leaf client cert (ClientAuth EKU) for the named
// service. Returns:
//
//   - chainPEM: leaf cert followed by the intermediate cert, both PEM-
//     encoded. The leaf holder presents this whole chain during TLS
//     handshake. Today's sole relying party — the otel-collector's
//     otlp/infra receiver — pins client_ca_file to the infra
//     intermediate, so the chain validates in one hop and any leaf
//     signed by something other than this intermediate (e.g. agent
//     leaves chained directly to the CLI root) fails validation by
//     design. Bundling the intermediate also lets future relying
//     parties anchor trust at the root and build the path without
//     holding the intermediate in their truststore.
//   - keyPEM: leaf private key, EC PRIVATE KEY block.
//
// The cert's CommonName is serviceName; serviceName is also added as a
// DNS SAN so peers that surface SANs (Envoy's `tls_certificate_sds_secret`
// among others) can audit the issuing identity.
func (i *Issuer) MintClient(serviceName string, ttl time.Duration) (chainPEM, keyPEM []byte, err error) {
	if serviceName == "" {
		return nil, nil, fmt.Errorf("serviceName must not be empty")
	}
	if ttl <= 0 {
		return nil, nil, fmt.Errorf("ttl must be > 0, got %s", ttl)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate leaf key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   serviceName,
			Organization: []string{"clawker"},
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.Add(ttl),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:    []string{serviceName},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, i.intermediate, &key.PublicKey, i.key)
	if err != nil {
		return nil, nil, fmt.Errorf("sign leaf: %w", err)
	}

	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	chain := append([]byte{}, leafPEM...)
	chain = append(chain, i.intermediatePEM...)

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal leaf key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return chain, keyPEM, nil
}

// IntermediatePEM returns a copy of the intermediate cert PEM the
// Issuer was loaded with.
func (i *Issuer) IntermediatePEM() []byte {
	out := make([]byte, len(i.intermediatePEM))
	copy(out, i.intermediatePEM)
	return out
}
