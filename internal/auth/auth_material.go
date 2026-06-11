// Package auth provides CLI-side authentication infrastructure for
// communicating with the clawker control plane. The CLI is the trust
// orchestrator — it generates all key material and bind-mounts the
// public halves into the CP container.
package auth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
)

// Directory layout under <DataDir>/auth/:
//
//	ca/
//	  ca.pem             ← CLI CA cert (bind-mounted into CP, read-only)
//	  ca.key             ← CLI CA key (0600, NEVER enters any container)
//	cli/
//	  signing.key        ← ES256 private key (0600, NEVER enters any container)
//	  signing-jwk.json   ← public JWK (bind-mounted into CP for Hydra)
//	  client.pem         ← CLI mTLS client cert signed by CLI CA (host-only)
//	  client.key         ← CLI mTLS client key (0600, NEVER enters any container)
//	tls/
//	  server.pem         ← server cert signed by CLI CA (bind-mounted into CP)
//	  server.key         ← server private key (bind-mounted into CP)
//	otel/
//	  server.pem         ← OTel collector server cert (bind-mounted into collector)
//	  server.key         ← OTel collector server key (0644, readable by collector uid)
//	cp/
//	  client.pem         ← CP outbound mTLS client cert (bind-mounted into CP)
//	  client.key         ← CP outbound mTLS client key (0600, bind-mounted into CP)
//	infra-ca/
//	  infra-ca.pem       ← infra intermediate CA cert (bind-mounted into CP)
//	  infra-ca.key       ← infra intermediate CA key (0600, bind-mounted into CP)

// EnsureAuthMaterial checks for existing auth material and creates any
// that is missing. Idempotent — safe to call on every CLI invocation.
// Directories are created by the consts accessors.
//
// The CLI is the root of trust. It generates:
//  1. CA keypair — signs server and client certs, never enters containers
//  2. ES256 signing keypair — for private_key_jwt auth with Hydra
//  3. Server cert — signed by the CLI CA, bind-mounted into CP
//  4. Client cert — signed by the CLI CA, used for mTLS to AdminService
//  5. OTel server cert — for the monitoring collector's CP-only OTLP receiver
//  6. CP client cert — CP's outbound mTLS identity for OTLP push and Session dials
//  7. Infra intermediate CA — signed by the CLI CA, CP mints short-lived infra leaves from it
func EnsureAuthMaterial() error {
	if err := consts.EnsureAuthDirs(); err != nil {
		return fmt.Errorf("ensure auth dirs: %w", err)
	}
	if err := ensureCA(); err != nil {
		return fmt.Errorf("CA: %w", err)
	}
	if err := ensureSigningKey(); err != nil {
		return fmt.Errorf("signing key: %w", err)
	}
	if err := ensureServerCert(); err != nil {
		return fmt.Errorf("server cert: %w", err)
	}
	if err := ensureClientCert(); err != nil {
		return fmt.Errorf("client cert: %w", err)
	}
	if err := ensureOtelServerCert(); err != nil {
		return fmt.Errorf("otel server cert: %w", err)
	}
	if err := ensureCPClientCert(); err != nil {
		return fmt.Errorf("cp client cert: %w", err)
	}
	if err := ensureInfraIntermediateCA(); err != nil {
		return fmt.Errorf("infra intermediate CA: %w", err)
	}
	return nil
}

// RotateAuthMaterial regenerates all auth material unconditionally.
// Unlike EnsureAuthMaterial which is idempotent (no-op if files exist),
// this deletes existing material and creates fresh keypairs.
//
// The server cert is always regenerated because it depends on the CA.
// The signing key is regenerated only if forceSigningKey is true (it
// requires re-registering the CLI client with Hydra on next CP start).
func RotateAuthMaterial(forceSigningKey bool) error {
	if err := removeIfExists(consts.AuthCACertPath); err != nil {
		return fmt.Errorf("remove CA cert: %w", err)
	}
	if err := removeIfExists(consts.AuthCAKeyPath); err != nil {
		return fmt.Errorf("remove CA key: %w", err)
	}
	if err := removeIfExists(consts.AuthServerCertPath); err != nil {
		return fmt.Errorf("remove server cert: %w", err)
	}
	if err := removeIfExists(consts.AuthServerKeyPath); err != nil {
		return fmt.Errorf("remove server key: %w", err)
	}
	if err := removeIfExists(consts.AuthCLIClientCertPath); err != nil {
		return fmt.Errorf("remove client cert: %w", err)
	}
	if err := removeIfExists(consts.AuthCLIClientKeyPath); err != nil {
		return fmt.Errorf("remove client key: %w", err)
	}
	if err := removeIfExists(consts.AuthOtelServerCertPath); err != nil {
		return fmt.Errorf("remove otel server cert: %w", err)
	}
	if err := removeIfExists(consts.AuthOtelServerKeyPath); err != nil {
		return fmt.Errorf("remove otel server key: %w", err)
	}
	if err := removeIfExists(consts.AuthCPClientCertPath); err != nil {
		return fmt.Errorf("remove cp client cert: %w", err)
	}
	if err := removeIfExists(consts.AuthCPClientKeyPath); err != nil {
		return fmt.Errorf("remove cp client key: %w", err)
	}
	if err := removeIfExists(consts.AuthInfraCACertPath); err != nil {
		return fmt.Errorf("remove infra CA cert: %w", err)
	}
	if err := removeIfExists(consts.AuthInfraCAKeyPath); err != nil {
		return fmt.Errorf("remove infra CA key: %w", err)
	}

	if forceSigningKey {
		if err := removeIfExists(consts.AuthCLISigningKeyPath); err != nil {
			return fmt.Errorf("remove signing key: %w", err)
		}
		if err := removeIfExists(consts.AuthCLISigningJWKPath); err != nil {
			return fmt.Errorf("remove signing JWK: %w", err)
		}
	}

	return EnsureAuthMaterial()
}

// AuthFileStatus describes the state of a single auth material file.
type AuthFileStatus struct {
	Name       string // human-readable name (e.g., "CA certificate")
	Path       string // filesystem path
	Exists     bool
	Mode       os.FileMode // only valid if Exists
	ParseError error       // non-nil if stat/read/parse failed (not os.ErrNotExist)
	Expires    time.Time   // only valid for certificates
	Expired    bool        // only valid for certificates
}

// CheckAuthMaterial inspects all auth material files and returns their status.
func CheckAuthMaterial() ([]AuthFileStatus, error) {
	type fileSpec struct {
		name   string
		pathFn func() (string, error)
		isCert bool
	}

	specs := []fileSpec{
		{"CA certificate", consts.AuthCACertPath, true},
		{"CA private key", consts.AuthCAKeyPath, false},
		{"CLI signing key", consts.AuthCLISigningKeyPath, false},
		{"CLI signing JWK", consts.AuthCLISigningJWKPath, false},
		{"Server certificate", consts.AuthServerCertPath, true},
		{"Server private key", consts.AuthServerKeyPath, false},
		{"CLI client certificate", consts.AuthCLIClientCertPath, true},
		{"CLI client key", consts.AuthCLIClientKeyPath, false},
		{"OTEL server certificate", consts.AuthOtelServerCertPath, true},
		{"OTEL server key", consts.AuthOtelServerKeyPath, false},
		{"CP client certificate", consts.AuthCPClientCertPath, true},
		{"CP client key", consts.AuthCPClientKeyPath, false},
		{"Infra intermediate CA certificate", consts.AuthInfraCACertPath, true},
		{"Infra intermediate CA key", consts.AuthInfraCAKeyPath, false},
	}

	var results []AuthFileStatus
	for _, s := range specs {
		path, err := s.pathFn()
		if err != nil {
			return nil, fmt.Errorf("resolve %s path: %w", s.name, err)
		}

		st := AuthFileStatus{Name: s.name, Path: path}
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				results = append(results, st)
				continue
			}
			st.ParseError = fmt.Errorf("stat: %w", err)
			results = append(results, st)
			continue
		}

		st.Exists = true
		st.Mode = info.Mode().Perm()

		if s.isCert {
			data, err := os.ReadFile(path)
			if err != nil {
				st.ParseError = fmt.Errorf("read cert: %w", err)
			} else {
				block, _ := pem.Decode(data)
				if block == nil {
					st.ParseError = fmt.Errorf("no PEM block found")
				} else {
					cert, err := x509.ParseCertificate(block.Bytes)
					if err != nil {
						st.ParseError = fmt.Errorf("parse certificate: %w", err)
					} else {
						st.Expires = cert.NotAfter
						st.Expired = time.Now().After(cert.NotAfter)
					}
				}
			}
		}

		results = append(results, st)
	}

	return results, nil
}

// removeIfExists removes a file if it exists. The path is resolved from
// a consts accessor function that returns (string, error).
func removeIfExists(pathFn func() (string, error)) error {
	path, err := pathFn()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// EnsureHydraSecret reads the persisted Hydra system secret from disk,
// or generates a new 32-byte random hex secret and writes it with 0600
// permissions. The secret is generated once and reused across restarts.
//
// Read errors are NOT collapsed into "regenerate" — a transient I/O
// fault that recovers between read and write would silently rotate the
// secret and invalidate every previously-issued Hydra token. Only
// os.ErrNotExist (first run) and an empty file (corruption fallback)
// trigger regeneration.
func EnsureHydraSecret() (string, error) {
	path, err := consts.HydraSystemSecretPath()
	if err != nil {
		return "", fmt.Errorf("hydra secret path: %w", err)
	}

	data, err := os.ReadFile(path)
	switch {
	case err == nil && len(data) > 0:
		return string(data), nil
	case err == nil:
		// empty file — fall through and regenerate
	case errors.Is(err, os.ErrNotExist):
		// first run — fall through and regenerate
	default:
		return "", fmt.Errorf("read hydra secret %q: %w", path, err)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate hydra secret: %w", err)
	}
	secret := hex.EncodeToString(buf)

	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		return "", fmt.Errorf("write hydra secret: %w", err)
	}
	return secret, nil
}

// --- CLI CA (root of trust) ---

func ensureCA() error {
	certPath, err := consts.AuthCACertPath()
	if err != nil {
		return err
	}
	keyPath, err := consts.AuthCAKeyPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return nil
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate CA serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   cliCACommonName,
			Organization: []string{certOrganization},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(5, 0, 0), // 5 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("sign CA cert: %w", err)
	}

	if err := writeCert(certPath, certDER); err != nil {
		return fmt.Errorf("write CA cert: %w", err)
	}
	if err := writeECDSAKey(keyPath, key, 0o600); err != nil {
		return fmt.Errorf("write CA key: %w", err)
	}
	return nil
}

// loadCA reads the CLI CA cert and key from disk.
func loadCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPath, err := consts.AuthCACertPath()
	if err != nil {
		return nil, nil, err
	}
	keyPath, err := consts.AuthCAKeyPath()
	if err != nil {
		return nil, nil, err
	}

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

	return caCert, caKey, nil
}

// CACert reads the CLI CA certificate. The CLI uses this to verify
// server certs it signed.
func CACert() (*x509.Certificate, error) {
	certPath, err := consts.AuthCACertPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("CA cert: no PEM block")
	}
	return x509.ParseCertificate(block.Bytes)
}

// --- Signing key (ES256 for private_key_jwt) ---

func ensureSigningKey() error {
	keyPath, err := consts.AuthCLISigningKeyPath()
	if err != nil {
		return err
	}
	jwkPath, err := consts.AuthCLISigningJWKPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(keyPath); err == nil {
		if _, err := os.Stat(jwkPath); err == nil {
			return nil
		}
		// Key exists but JWK missing — regenerate JWK from key.
		key, err := loadECDSAKey(keyPath)
		if err != nil {
			return fmt.Errorf("load existing key: %w", err)
		}
		return writeJWK(jwkPath, &key.PublicKey)
	}

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
func LoadSigningKey() (*ecdsa.PrivateKey, error) {
	keyPath, err := consts.AuthCLISigningKeyPath()
	if err != nil {
		return nil, err
	}
	return loadECDSAKey(keyPath)
}

// --- Server TLS cert (signed by CLI CA) ---

func ensureServerCert() error {
	certPath, err := consts.AuthServerCertPath()
	if err != nil {
		return err
	}
	keyPath, err := consts.AuthServerKeyPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return nil
		}
	}

	caCert, caKey, err := loadCA()
	if err != nil {
		return fmt.Errorf("load CA for signing: %w", err)
	}

	// Generate server key.
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
			CommonName:   consts.ContainerCP,
			Organization: []string{certOrganization},
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost", consts.ContainerCP},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	// CA-signed, not self-signed.
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
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

// --- CLI mTLS client cert (signed by CLI CA) ---

func ensureClientCert() error {
	certPath, err := consts.AuthCLIClientCertPath()
	if err != nil {
		return err
	}
	keyPath, err := consts.AuthCLIClientKeyPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return nil
		}
	}

	caCert, caKey, err := loadCA()
	if err != nil {
		return fmt.Errorf("load CA for signing: %w", err)
	}

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
			CommonName:   "clawker-cli",
			Organization: []string{certOrganization},
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	// CA-signed, not self-signed.
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
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

// LoadClientCert reads the CLI's mTLS client certificate and key
// as a tls.Certificate for use with grpc.WithTransportCredentials.
func LoadClientCert() (tls.Certificate, error) {
	certPath, err := consts.AuthCLIClientCertPath()
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPath, err := consts.AuthCLIClientKeyPath()
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(certPath, keyPath)
}

// --- PEM helpers ---

func loadECDSAKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decode %s: no PEM block", path)
	}
	// Reject trailing content after the first block: these files are
	// single-key PEMs and a non-empty rest signals corruption / wrong
	// file format. Whitespace is fine.
	if len(bytes.TrimSpace(rest)) > 0 {
		return nil, fmt.Errorf("decode %s: trailing bytes after PEM block", path)
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
		"use": "sig",
		"alg": "ES256",
		"x":   base64.RawURLEncoding.EncodeToString(pub.X.Bytes()),
		"y":   base64.RawURLEncoding.EncodeToString(pub.Y.Bytes()),
	}
	data, err := json.MarshalIndent(jwk, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// --- Infra intermediate CA ---
//
// The infra intermediate is signed by the CLI root CA and bind-mounted
// into the clawker-controlplane container. CP loads it via
// `internal/controlplane/infracerts` and signs short-lived mTLS client
// leaves for clawker infrastructure services (Envoy, CoreDNS, etc.)
// that push telemetry to the trusted-infra OTLP receiver. The
// otel-collector's `otlp/infra` receiver pins `client_ca_file` to this
// infra intermediate (NOT the CLI root) so only intermediate-chained
// leaves complete the handshake — agent leaves minted by
// `auth.MintAgentCert` chain to the CLI root directly and are rejected,
// which is what stops agents from forging `service.name=clawker-cp`
// records on the trusted forensic indices. `monitor init` resolves the
// bind-mount source via `consts.AuthInfraCACertPath()`. Adding a new
// infra service is a CP-side change — the CLI does not learn about
// it.

func ensureInfraIntermediateCA() error {
	certPath, err := consts.AuthInfraCACertPath()
	if err != nil {
		return err
	}
	keyPath, err := consts.AuthInfraCAKeyPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(certPath); err == nil {
		if info, err := os.Stat(keyPath); err == nil {
			// Migrate in-place: an upgrade from an older clawker that
			// wrote this key at 0o644 leaves a world-readable signing
			// key on disk forever (regen only fires on file absence).
			// Tighten unconditionally — same UID owns the file by
			// construction, so chmod is cheap and always succeeds.
			if info.Mode().Perm() != 0o600 {
				if err := os.Chmod(keyPath, 0o600); err != nil {
					return fmt.Errorf("tighten infra CA key perms: %w", err)
				}
			}
			return nil
		}
	}

	caCert, caKey, err := loadCA()
	if err != nil {
		return fmt.Errorf("load root CA for signing: %w", err)
	}

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
			CommonName:   infraCACommonName,
			Organization: []string{certOrganization},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("sign intermediate cert: %w", err)
	}

	if err := writeCert(certPath, certDER); err != nil {
		return fmt.Errorf("write intermediate cert: %w", err)
	}
	// 0o600: CP runs as root inside the container and reads 0o600 host
	// bind-mounts without any chown ceremony (same pattern as the
	// sibling CP OAuth2/mTLS client key written by ensureCPClientCert
	// below). The infra intermediate CA private key signs runtime
	// mTLS leaves trusted by the CP's trusted-infra OTLP receiver; it
	// must NOT be world-readable on the host.
	if err := writeECDSAKey(keyPath, key, 0o600); err != nil {
		return fmt.Errorf("write intermediate key: %w", err)
	}
	return nil
}

// ReadJWK reads the CLI's public signing key as raw JSON bytes.
func ReadJWK() (json.RawMessage, error) {
	p, err := consts.AuthCLISigningJWKPath()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// --- OTEL collector mTLS pair (signed by CLI CA) ---
//
// Two extra certs gate the monitoring stack's CP-only OTLP receiver:
//
//   - otel-server.{pem,key}: presented by the otel-collector container
//     on its CP-only receiver. SANs cover the names CP uses to dial
//     (host.docker.internal, localhost, 127.0.0.1) so a single cert
//     works across Linux native (where host.docker.internal resolves
//     to the bridge gateway) and Docker Desktop.
//
//   - cp-client.{pem,key}: presented by clawker-cp when pushing OTLP.
//     Subject "clawker-cp" so the receiver can audit which client is
//     pushing in case future scoping is added.
//
// Agents on clawker-net cannot reach the receiver because they lack
// any cert signed by the CLI CA — the TLS handshake fails before any
// data is accepted. No BPF rule needed; auth is the boundary.

// otelServerCertSANs lists every name a trusted peer might dial. Hoisted
// out of the mint path so the on-disk cert can be checked against the
// current SAN list at every CP start and re-minted when the list grows.
//
//   - host.docker.internal: CP dials this from the host-side 127.0.0.1
//     publish.
//   - localhost / 127.0.0.1: host-side debug, future tools.
//   - otel-collector (consts.MonitoringServiceOtelCollector): clawker-net
//     DNS name used by Envoy ALS and the CoreDNS OTel plugin dialing
//     siblings over the docker network. Without this SAN the gRPC SNI
//     check fails: "certificate is valid for ... not otel-collector".
var (
	otelServerDNSNames = []string{consts.HostDockerInternal, "localhost", consts.MonitoringServiceOtelCollector}
	otelServerIPs      = []net.IP{net.IPv4(127, 0, 0, 1)}
)

// otelServerCertSANsCurrent reports whether the cert at certPath already
// carries every entry in otelServerDNSNames/otelServerIPs. Returns false
// for any read/parse failure so the caller re-mints — a cert we can't
// parse is by definition not current.
func otelServerCertSANsCurrent(certPath string) bool {
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	have := make(map[string]struct{}, len(cert.DNSNames))
	for _, n := range cert.DNSNames {
		have[n] = struct{}{}
	}
	for _, want := range otelServerDNSNames {
		if _, ok := have[want]; !ok {
			return false
		}
	}
	haveIPs := make(map[string]struct{}, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		haveIPs[ip.String()] = struct{}{}
	}
	for _, want := range otelServerIPs {
		if _, ok := haveIPs[want.String()]; !ok {
			return false
		}
	}
	return true
}

func ensureOtelServerCert() error {
	certPath, err := consts.AuthOtelServerCertPath()
	if err != nil {
		return err
	}
	keyPath, err := consts.AuthOtelServerKeyPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			if otelServerCertSANsCurrent(certPath) {
				return nil
			}
		}
	}

	caCert, caKey, err := loadCA()
	if err != nil {
		return fmt.Errorf("load CA for signing: %w", err)
	}

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
			CommonName:   otelCollectorCommonName,
			Organization: []string{certOrganization},
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    otelServerDNSNames,
		IPAddresses: otelServerIPs,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("sign cert: %w", err)
	}

	if err := writeCert(certPath, certDER); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	// Loosen file perms so the otel-collector container's uid (varies
	// by image) can read after bind-mount. Defense-in-depth against
	// other local users comes from the auth/ tree being 0o700 — see
	// consts.EnsureAuthDirs and TestRotateAuthMaterial_Permissions.
	if err := writeECDSAKey(keyPath, key, 0o644); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	return nil
}

func ensureCPClientCert() error {
	certPath, err := consts.AuthCPClientCertPath()
	if err != nil {
		return err
	}
	keyPath, err := consts.AuthCPClientKeyPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return nil
		}
	}

	caCert, caKey, err := loadCA()
	if err != nil {
		return fmt.Errorf("load CA for signing: %w", err)
	}

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
			CommonName:   consts.ContainerCP,
			Organization: []string{certOrganization},
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("sign cert: %w", err)
	}

	if err := writeCert(certPath, certDER); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	// Tight perms: the CP container runs as root (no USER directive on
	// distroless/static, no Config.User in BuildCPContainerConfig), so
	// a 0o600 host file is readable in-container without loosening.
	if err := writeECDSAKey(keyPath, key, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	return nil
}
