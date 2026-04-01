package firewall

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
	"path/filepath"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/config"
)

const (
	caCertFile = "ca-cert.pem"
	caKeyFile  = "ca-key.pem"

	caCommonName = "Clawker Firewall CA"
	caValidYears = 10

	domainCertValidYears = 1
)

// EnsureCA creates a self-signed CA keypair if none exists under certDir,
// or loads the existing one.
func EnsureCA(certDir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("creating certs directory: %w", err)
	}
	certPath := filepath.Join(certDir, caCertFile)
	keyPath := filepath.Join(certDir, caKeyFile)

	// If both files exist, load and return.
	if fileExists(certPath) && fileExists(keyPath) {
		return loadCA(certPath, keyPath)
	}

	// Generate new CA.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, fmt.Errorf("generating serial: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: caCommonName},
		NotBefore:             now,
		NotAfter:              now.AddDate(caValidYears, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("creating CA certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing CA certificate: %w", err)
	}

	if err := writeCertPEM(certPath, certDER); err != nil {
		return nil, nil, fmt.Errorf("writing CA cert: %w", err)
	}
	if err := writeKeyPEM(keyPath, key); err != nil {
		return nil, nil, fmt.Errorf("writing CA key: %w", err)
	}

	return cert, key, nil
}

// GenerateDomainCert signs a per-domain certificate for TLS inspection.
// The certificate is signed by the given CA and has the domain as a SAN.
// For wildcard domains (leading-dot convention), the SAN includes both
// the apex (e.g., "datadoghq.com") and the wildcard ("*.datadoghq.com")
// so TLS inspection works for any subdomain.
// Returns PEM-encoded cert and key bytes.
func GenerateDomainCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, domain string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating domain key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, fmt.Errorf("generating serial: %w", err)
	}

	wild := isWildcardDomain(domain)
	normalized := normalizeDomain(domain)

	dnsNames := []string{normalized}
	if wild {
		dnsNames = append(dnsNames, "*."+normalized)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: normalized},
		DNSNames:     dnsNames,
		NotBefore:    now,
		NotAfter:     now.AddDate(domainCertValidYears, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("creating domain certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshalling domain key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

// RegenerateDomainCerts generates certificates for all TLS egress rules,
// storing them in certDir/<domain>-cert.pem and <domain>-key.pem.
// Every TLS rule gets a certificate — Envoy terminates TLS for all domains
// to enable HTTP-level inspection (paths, methods, response codes).
//
// Rules are deduplicated by normalized domain. If any rule for a domain uses
// the wildcard convention (leading dot), the cert includes both apex and
// wildcard SANs. This prevents a later exact-domain rule from overwriting
// a cert that also needs wildcard SANs.
//
// Cert generation runs before stale cleanup so that a partial failure leaves
// previously-working certs intact rather than an empty directory.
func RegenerateDomainCerts(rules []config.EgressRule, certDir string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) error {
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return fmt.Errorf("creating certs directory: %w", err)
	}

	// Deduplicate by normalized domain, tracking whether any rule uses
	// the wildcard convention so the cert includes wildcard SANs if needed.
	type domainCertInfo struct {
		needsWild bool
	}
	seen := make(map[string]*domainCertInfo)
	var order []string // preserve deterministic iteration

	for _, rule := range rules {
		// Only TLS rules need certificates — skip TCP, SSH, HTTP.
		proto := strings.ToLower(rule.Proto)
		if proto == "tcp" || proto == "ssh" || proto == "http" {
			continue
		}
		if isIPOrCIDR(rule.Dst) {
			continue
		}

		normalized := normalizeDomain(rule.Dst)
		if info, exists := seen[normalized]; exists {
			if isWildcardDomain(rule.Dst) {
				info.needsWild = true
			}
			continue
		}
		seen[normalized] = &domainCertInfo{
			needsWild: isWildcardDomain(rule.Dst),
		}
		order = append(order, normalized)
	}

	// Generate certs first — overwrites existing files in-place.
	// If generation fails partway, domains before the failure have fresh certs
	// and domains after still have their old (valid) certs.
	for _, normalized := range order {
		info := seen[normalized]
		// Re-add leading dot so GenerateDomainCert produces wildcard SANs.
		domain := normalized
		if info.needsWild {
			domain = "." + normalized
		}

		certPEM, keyPEM, err := GenerateDomainCert(caCert, caKey, domain)
		if err != nil {
			return fmt.Errorf("generating cert for %s: %w", normalized, err)
		}

		certPath := filepath.Join(certDir, normalized+"-cert.pem")
		keyPath := filepath.Join(certDir, normalized+"-key.pem")

		if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
			return fmt.Errorf("writing cert for %s: %w", normalized, err)
		}
		if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
			return fmt.Errorf("writing key for %s: %w", normalized, err)
		}
	}

	// Clean stale domain cert files only after all new certs are written.
	// Only removes certs for domains no longer in the rule set.
	if err := cleanStaleDomainCerts(certDir, seen); err != nil {
		return fmt.Errorf("cleaning stale certs: %w", err)
	}

	return nil
}

// RotateCA regenerates the CA keypair and all domain certificates.
// The old CA files are overwritten. Any running containers will need
// the new CA injected to trust the regenerated domain certs.
func RotateCA(certDir string, rules []config.EgressRule) error {
	// Remove entire certs directory (CA + domain certs) so EnsureCA generates fresh ones.
	if err := os.RemoveAll(certDir); err != nil {
		return fmt.Errorf("removing old certs directory: %w", err)
	}

	caCert, caKey, err := EnsureCA(certDir)
	if err != nil {
		return fmt.Errorf("regenerating CA: %w", err)
	}

	if err := RegenerateDomainCerts(rules, certDir, caCert, caKey); err != nil {
		return fmt.Errorf("regenerating domain certs: %w", err)
	}

	return nil
}

// cleanStaleDomainCerts removes domain cert/key files from certDir that are
// not in the target domain set, preserving the CA files (ca-cert.pem, ca-key.pem).
// This is called after cert generation so that a partial generation failure
// does not leave the directory empty — only truly stale files are removed.
func cleanStaleDomainCerts[T any](certDir string, targetDomains map[string]T) error {
	entries, err := os.ReadDir(certDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if name == caCertFile || name == caKeyFile {
			continue
		}
		// Extract domain from filename: "<domain>-cert.pem" or "<domain>-key.pem".
		var domain string
		switch {
		case strings.HasSuffix(name, "-cert.pem"):
			domain = strings.TrimSuffix(name, "-cert.pem")
		case strings.HasSuffix(name, "-key.pem"):
			domain = strings.TrimSuffix(name, "-key.pem")
		default:
			continue
		}
		if _, inTarget := targetDomains[domain]; inTarget {
			continue // current domain — keep
		}
		if err := os.Remove(filepath.Join(certDir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing stale cert %s: %w", name, err)
		}
	}
	return nil
}

// --- helpers ---

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func loadCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading CA cert: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("decoding CA cert PEM: no PEM block found")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing CA cert: %w", err)
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading CA key: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("decoding CA key PEM: no PEM block found")
	}

	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing CA key: %w", err)
	}

	return cert, key, nil
}

func writeCertPEM(path string, certDER []byte) error {
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	return os.WriteFile(path, data, 0o600)
}

func writeKeyPEM(path string, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshalling key: %w", err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return os.WriteFile(path, data, 0o600)
}
