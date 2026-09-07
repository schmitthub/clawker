package firewall

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/config"
)

const (
	caCertFile     = "ca-cert.pem"
	caKeyFile      = "ca-key.pem"
	pemTempPattern = ".pem-*.tmp"

	caCommonName = "Clawker Firewall CA"
	caValidYears = 10

	domainCertValidYears = 1

	// The two PEM block types this package emits (RFC 7468 labels).
	pemBlockCertificate  = "CERTIFICATE"
	pemBlockECPrivateKey = "EC PRIVATE KEY"
)

// ErrNoCA means that an existing CA certificate or key is absent.
var ErrNoCA = errors.New("firewall: CA certificate or key is absent")

var errCAMismatch = errors.New("firewall: CA certificate and private key do not match")

// LoadCA reads an existing CA pair without creating directories or files.
// Concurrent callers in the control plane must use their shared CAStore.
func LoadCA(certDir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cert, key, err := loadCA(filepath.Join(certDir, caCertFile), filepath.Join(certDir, caKeyFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("%w: %w", ErrNoCA, err)
	}
	return cert, key, err
}

// encodePEM renders a DER payload as a PEM block of the given type. Headers is
// explicitly nil: PEM headers are an RFC 1421 legacy that nothing in the firewall
// stack reads, and Go's own encoders emit none — so every block this package
// writes is header-free by intent, not by omission.
func encodePEM(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Headers: nil, Bytes: der})
}

// EnsureCA creates a self-signed CA keypair if none exists under certDir,
// or loads the existing one.
// Concurrent callers in the control plane must use their shared CAStore.
func EnsureCA(certDir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("creating certs directory: %w", err)
	}
	certPath := filepath.Join(certDir, caCertFile)
	keyPath := filepath.Join(certDir, caKeyFile)

	// If both files exist, load and return.
	if fileExists(certPath) && fileExists(keyPath) {
		cert, key, loadErr := LoadCA(certDir)
		if !errors.Is(loadErr, errCAMismatch) {
			return cert, key, loadErr
		}
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

	// Commit the key first. caCertFile marks a completed new CA pair.
	if writeErr := writeKeyPEM(keyPath, key); writeErr != nil {
		return nil, nil, fmt.Errorf("writing CA key: %w", writeErr)
	}
	if writeErr := writeCertPEM(certPath, certDER); writeErr != nil {
		return nil, nil, fmt.Errorf("writing CA cert: %w", writeErr)
	}

	return cert, key, nil
}

// GenerateDomainCert signs a per-domain certificate for TLS inspection.
// The certificate is signed by the given CA and has the domain as a SAN.
// For wildcard domains (leading-dot convention), the SAN includes both
// the apex (e.g., "datadoghq.com") and the wildcard ("*.datadoghq.com")
// so TLS inspection works for any subdomain.
// Returns PEM-encoded cert and key bytes.
func GenerateDomainCert(
	caCert *x509.Certificate,
	caKey *ecdsa.PrivateKey,
	domain string,
) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating domain key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, fmt.Errorf("generating serial: %w", err)
	}

	normalized := normalizeDomain(domain)

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: normalized},
		NotBefore:    now,
		NotAfter:     now.AddDate(domainCertValidYears, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	// IP-literal vs hostname is orthogonal to everything else about the cert: a
	// TLS connection to an IP carries no SNI (RFC 6066) and is validated against
	// the cert's iPAddress SAN, never a dNSName. A hostname (incl. wildcard) gets
	// dNSName SANs. Only one of the two applies per dst.
	switch ip := net.ParseIP(normalized); {
	case ip != nil:
		template.IPAddresses = []net.IP{ip}
	case isCIDR(normalized):
		// A CIDR dst mints ONE leaf whose iPAddress SAN is the network address. That
		// SAN matches only the network address itself — TLS name/IP verification
		// against any other in-range host (the .1 gateway, etc.) fails, since x509
		// has no CIDR-range SAN. It does not need to: agent-side verification is not
		// clawker's enforcement boundary. Authorization is enforced by Envoy's
		// prefix_range / original_dst gating (NOT SAN matching), and MITM inspection
		// still applies. The leaf only encrypts the hop and lets Envoy MITM-inspect;
		// a client connecting to a raw in-range IP must set its own no-verify, exactly
		// as it must for any self-signed endpoint.
		_, ipnet, _ := net.ParseCIDR(normalized)
		template.IPAddresses = []net.IP{ipnet.IP}
	default:
		dnsNames := []string{normalized}
		if isWildcardDomain(domain) {
			dnsNames = append(dnsNames, "*."+normalized)
		}
		template.DNSNames = dnsNames
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("creating domain certificate: %w", err)
	}

	certPEM := encodePEM(pemBlockCertificate, certDER)

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshalling domain key: %w", err)
	}
	keyPEM := encodePEM(pemBlockECPrivateKey, keyDER)

	return certPEM, keyPEM, nil
}

// GenerateSNICert mints a P-256 leaf for one exact SNI hostname, signed by the
// firewall CA. The SAN set is the single dNSName — this is the on-demand path
// behind the Envoy SDS certificate selector, where the requested name can sit
// at any label depth under a wildcard rule zone (RFC 6125 wildcards match one
// label, so the static [apex, *.apex] pair cannot cover a.b.zone; the per-SNI
// leaf can). Wildcards, IP literals, and empty names are rejected — the SNI
// comes from the peer's ClientHello and must be an exact hostname.
func GenerateSNICert(
	caCert *x509.Certificate,
	caKey *ecdsa.PrivateKey,
	sni string,
) ([]byte, []byte, error) {
	if err := validateSNIHostname(sni); err != nil {
		return nil, nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating sni key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, fmt.Errorf("generating serial: %w", err)
	}

	template := sniCertificateTemplate(sni, serial, time.Now())

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("creating sni certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshalling sni key: %w", err)
	}

	return encodePEM(pemBlockCertificate, certDER), encodePEM(pemBlockECPrivateKey, keyDER), nil
}

// sniCertificateTemplate sets the certificate fields for one SNI hostname.
func sniCertificateTemplate(sni string, serial *big.Int, now time.Time) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     sniCertificateName(sni),
		NotBefore:                   now,
		NotAfter:                    now.AddDate(domainCertValidYears, 0, 0),
		KeyUsage:                    x509.KeyUsageDigitalSignature,
		ExtKeyUsage:                 []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:                    []string{sni},
		Raw:                         nil,
		RawTBSCertificate:           nil,
		RawSubjectPublicKeyInfo:     nil,
		RawSubject:                  nil,
		RawIssuer:                   nil,
		RawSignatureAlgorithm:       nil,
		Signature:                   nil,
		SignatureAlgorithm:          x509.UnknownSignatureAlgorithm,
		PublicKeyAlgorithm:          x509.UnknownPublicKeyAlgorithm,
		PublicKey:                   nil,
		Version:                     0,
		Issuer:                      sniCertificateName(""),
		Extensions:                  nil,
		ExtraExtensions:             nil,
		UnhandledCriticalExtensions: nil,
		UnknownExtKeyUsage:          nil,
		BasicConstraintsValid:       false,
		IsCA:                        false,
		MaxPathLen:                  0,
		MaxPathLenZero:              false,
		SubjectKeyId:                nil,
		AuthorityKeyId:              nil,
		OCSPServer:                  nil,
		IssuingCertificateURL:       nil,
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
}

func sniCertificateName(commonName string) pkix.Name {
	return pkix.Name{
		Country:            nil,
		Organization:       nil,
		OrganizationalUnit: nil,
		Locality:           nil,
		Province:           nil,
		StreetAddress:      nil,
		PostalCode:         nil,
		SerialNumber:       "",
		CommonName:         commonName,
		Names:              nil,
		ExtraNames:         nil,
	}
}

// validateSNIHostname accepts only an exact lowercase-normalizable FQDN: LDH
// labels, at least two labels, no wildcard marker, no IP literal. Fail closed
// on anything else — the value arrives from the peer's ClientHello.
// sniHostnameRe matches an exact FQDN of at least two LDH labels; no label
// starts or ends with a hyphen, no label exceeds 63 characters.
var sniHostnameRe = regexp.MustCompile(
	`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`,
)

// validateSNIHostname accepts only an exact FQDN: LDH labels (no leading or
// trailing hyphen), at least two labels, no wildcard marker, no IP literal.
// Fail closed on anything else — the value arrives from the peer's ClientHello.
func validateSNIHostname(sni string) error {
	if sni == "" || len(sni) > 253 {
		return fmt.Errorf("invalid sni hostname %q", sni)
	}
	if net.ParseIP(sni) != nil {
		return fmt.Errorf("invalid sni hostname %q: IP literal", sni)
	}
	if !sniHostnameRe.MatchString(sni) {
		return fmt.Errorf("invalid sni hostname %q", sni)
	}
	return nil
}

// certBasename is the flat on-disk filename stem for a dst's MITM cert/key. It
// keeps dots (valid in filenames and unique per FQDN/IP) but folds the CIDR "/"
// to "_" so a range dst (10.0.0.0/24) maps to a single flat file pair
// (10.0.0.0_24-{cert,key}.pem) instead of a bogus subdirectory. The downstream
// TLS context's cert reference must use this same basename so the listener finds
// the file — both call sites flow through certBasename.
func certBasename(dst string) string {
	return strings.ReplaceAll(normalizeDomain(dst), "/", "_")
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
func RegenerateDomainCerts(
	rules []config.EgressRule,
	certDir string,
	caCert *x509.Certificate,
	caKey *ecdsa.PrivateKey,
) error {
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return fmt.Errorf("creating certs directory: %w", err)
	}

	plans, order := planDomainCerts(rules)

	// Generate certificates first and replace each PEM file atomically.
	// If generation fails partway, domains before the failure have fresh certs
	// and domains after still have their old (valid) certs.
	for _, bn := range order {
		if err := writeDomainCert(certDir, bn, plans[bn], caCert, caKey); err != nil {
			return err
		}
	}

	// Clean stale domain cert files only after all new certs are written.
	// Only removes certs for domains no longer in the rule set.
	if err := cleanStaleDomainCerts(certDir, plans); err != nil {
		return fmt.Errorf("cleaning stale certs: %w", err)
	}

	return nil
}

// domainCertPlan is the decision for ONE flat on-disk cert basename: which
// domain form to sign and whether the leaf needs wildcard SANs. Several rules
// can fold into one plan (same dst on two ports, exact + wildcard on one apex),
// which is why the plan — not the rule — is the unit of generation.
type domainCertPlan struct {
	domain    string // normalizeDomain(dst): FQDN, IP literal, or CIDR (keeps "/")
	needsWild bool
}

// certDomain is the domain string handed to GenerateDomainCert: the leading dot
// is re-added for a wildcard plan so the leaf carries both apex and wildcard SANs.
func (p *domainCertPlan) certDomain() string {
	if p.needsWild {
		return "." + p.domain
	}
	return p.domain
}

// planDomainCerts reduces the rule set to one cert plan per cert basename (the
// flat on-disk filename stem), plus the deterministic basename order to generate
// them in. If ANY rule for a basename uses the wildcard convention the plan is
// marked wildcard, so a later exact-domain rule cannot demote a cert that also
// needs wildcard SANs.
func planDomainCerts(rules []config.EgressRule) (map[string]*domainCertPlan, []string) {
	plans := make(map[string]*domainCertPlan)
	var order []string

	for _, rule := range rules {
		// Normalize first so legacy `proto: tls` translates to `https` before
		// the proto check. A skip here means no cert minted → TLS-MITM handshake
		// fails at runtime with no operator-visible signal.
		rule = NormalizeRule(rule)
		// Only TLS-terminated protos need a MITM cert: https and wss (websocket
		// over TLS — same downstream TLS termination as https, just with an
		// upgrade enrichment). Plaintext http/ws, opaque TCP/SSH/UDP, and any
		// other proto pass through without TLS termination.
		if p := strings.ToLower(rule.Proto); p != protoHTTPS && p != protoWSS {
			continue
		}
		// Every TLS-terminated dst gets a MITM cert — FQDN (dNSName SANs), IP
		// literal (iPAddress SAN), AND CIDR range (one leaf, iPAddress SAN = the
		// network address; see GenerateDomainCert). A range cert cannot validate
		// every in-range host, but agent-side verification is not the enforcement
		// boundary — the cert exists to encrypt the hop and enable MITM inspection.
		bn := certBasename(rule.Dst)
		if plan, exists := plans[bn]; exists {
			plan.needsWild = plan.needsWild || isWildcardDomain(rule.Dst)
			continue
		}
		plans[bn] = &domainCertPlan{
			domain:    normalizeDomain(rule.Dst),
			needsWild: isWildcardDomain(rule.Dst),
		}
		order = append(order, bn)
	}

	return plans, order
}

// writeDomainCert signs one plan against the CA and writes the cert/key pair to
// certDir under the plan's basename. Overwrites in place, so a failure here
// leaves every not-yet-regenerated dst on its previous (still valid) cert.
func writeDomainCert(
	certDir, basename string,
	plan *domainCertPlan,
	caCert *x509.Certificate,
	caKey *ecdsa.PrivateKey,
) error {
	certPEM, keyPEM, err := GenerateDomainCert(caCert, caKey, plan.certDomain())
	if err != nil {
		return fmt.Errorf("generating cert for %s: %w", plan.domain, err)
	}
	if err = writePEMAtomic(filepath.Join(certDir, basename+"-key.pem"), keyPEM); err != nil {
		return fmt.Errorf("writing key for %s: %w", plan.domain, err)
	}
	if err = writePEMAtomic(filepath.Join(certDir, basename+"-cert.pem"), certPEM); err != nil {
		return fmt.Errorf("writing cert for %s: %w", plan.domain, err)
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
	if !key.PublicKey.Equal(cert.PublicKey) {
		return nil, nil, errCAMismatch
	}

	return cert, key, nil
}

func writeCertPEM(path string, certDER []byte) error {
	data := encodePEM(pemBlockCertificate, certDER)
	return writePEMAtomic(path, data)
}

func writeKeyPEM(path string, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshalling key: %w", err)
	}
	data := encodePEM(pemBlockECPrivateKey, keyDER)
	return writePEMAtomic(path, data)
}

// writePEMAtomic replaces a PEM file with a complete file from the same
// directory. [os.CreateTemp] restricts access to the file owner.
func writePEMAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), pemTempPattern)
	if err != nil {
		return fmt.Errorf("creating PEM temporary file: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Remove temporary private key material after any failed write.
			_ = os.Remove(tmp.Name()) // Cleanup cannot replace the original file error.
		}
	}()
	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close() // Cleanup cannot replace the write error.
		return fmt.Errorf("writing PEM temporary file: %w", writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("closing PEM temporary file: %w", closeErr)
	}
	if renameErr := os.Rename(tmp.Name(), path); renameErr != nil {
		return fmt.Errorf("committing PEM file: %w", renameErr)
	}
	committed = true
	return nil
}
