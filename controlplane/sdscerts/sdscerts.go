// Package sdscerts provisions the dedicated mTLS client identity the
// firewall's Envoy sibling uses to dial the CP's on-demand certificate
// SDS server (controlplane/firewall/sds_server.go).
//
// It is intentionally separate from controlplane/otelcerts even though
// both mint client leaves off the same infra intermediate CA. The SDS
// lane hands out CA-signed MITM certificates, so it owns its own leaf
// identity (consts.EnvoySDSClientName — the server pins that exact
// SAN), its own on-disk material (consts.SDSClientsDirName), and its
// own readiness gate in the firewall stack. A telemetry provisioning
// change must not silently disable per-SNI certificate minting, and a
// telemetry client leaf must not authenticate to the SDS server.
package sdscerts

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/schmitthub/clawker/controlplane/infracerts"
	"github.com/schmitthub/clawker/internal/consts"
)

// leafTTL is the validity window of the minted SDS client leaf. The
// leaf is re-minted on every firewall stack reload (EnsureEnvoyClient
// overwrites in place), so the window only needs to outlive the gap
// between reloads with margin.
const leafTTL = 365 * 24 * time.Hour

// envoySubdir is the per-service directory under the SDS clients dir.
// The firewall stack composes the sibling bind-mount source as
// consts.HostFirewallSDSCertsDir + this same segment.
const envoySubdir = "envoy"

// Service mints and persists the Envoy→CP SDS client material.
type Service struct {
	issuer  *infracerts.Issuer
	destDir string
	rootCA  []byte
}

// New returns a Service writing under destDir. rootCABytes is the CLI
// root CA PEM Envoy uses to verify the CP's SDS server certificate; it
// is copied verbatim as ca.pem next to the client pair.
func New(issuer *infracerts.Issuer, destDir string, rootCABytes []byte) (*Service, error) {
	if issuer == nil {
		return nil, errors.New("sdscerts: issuer must not be nil")
	}
	if destDir == "" {
		return nil, errors.New("sdscerts: destDir must not be empty")
	}
	if len(rootCABytes) == 0 {
		return nil, errors.New("sdscerts: root CA bytes must not be empty")
	}
	return &Service{issuer: issuer, destDir: destDir, rootCA: rootCABytes}, nil
}

// EnsureEnvoyClient mints a fresh consts.EnvoySDSClientName leaf and
// writes
//
//	<destDir>/envoy/client.pem  (leaf + intermediate chain)
//	<destDir>/envoy/client.key  (leaf private key)
//	<destDir>/envoy/ca.pem      (CLI root CA, for server-cert verification)
//
// atomically (tmp + rename); re-runs overwrite in place. The pair is
// parse-checked before any write commits so a mint failure leaves the
// caller's ready-flag false instead of half-writing broken material.
//
// Permission shape matches the telemetry lane's for the same reason:
// 0o755 on the dir and 0o644 on the files, because the Envoy distroless
// image runs UID 101 and bind-mounts preserve host inode perms.
//
// Returned paths are CP-container-FS absolute paths — the firewall
// stack discards them and derives the sibling Mount.Source from
// consts.HostFirewallSDSCertsDir; they exist for tests.
func (s *Service) EnsureEnvoyClient() (string, string, string, error) {
	svcDir := filepath.Join(s.destDir, envoySubdir)
	// 0o755: Envoy distroless runs UID 101 and bind-mounts preserve host
	// inode perms — a tighter dir mode blocks traversal for the reader.
	if err := os.MkdirAll(svcDir, 0o755); err != nil { //nolint:gosec // G301: non-root Envoy must traverse
		return "", "", "", fmt.Errorf("sdscerts: create envoy dir: %w", err)
	}

	chainPEM, keyPEM, err := s.issuer.MintClient(consts.EnvoySDSClientName, leafTTL)
	if err != nil {
		return "", "", "", fmt.Errorf("sdscerts: mint %s leaf: %w", consts.EnvoySDSClientName, err)
	}
	if _, pairErr := tls.X509KeyPair(chainPEM, keyPEM); pairErr != nil {
		return "", "", "", fmt.Errorf("sdscerts: validate cert/key pair: %w", pairErr)
	}

	certPath := filepath.Join(svcDir, consts.ClientCertFile)
	keyPath := filepath.Join(svcDir, consts.ClientKeyFile)
	caPath := filepath.Join(svcDir, consts.CACertFile)
	for _, f := range []struct {
		path string
		data []byte
	}{
		{caPath, s.rootCA},
		{certPath, chainPEM},
		{keyPath, keyPEM},
	} {
		if writeErr := writeAtomic(f.path, f.data); writeErr != nil {
			return "", "", "", fmt.Errorf("sdscerts: write %s: %w", filepath.Base(f.path), writeErr)
		}
	}
	return certPath, keyPath, caPath, nil
}

// NewCPProvisioner builds the SDS client-cert provisioner from the CP's
// on-disk trust material: the infra intermediate CA (issuer) and the
// CLI root CA (server-verification anchor for Envoy).
//
// LANDMINE — degraded-mode signaling (same rule as
// otelcerts.NewCPProvisioner): the concrete *Service return is nil on
// ANY failure, and the caller must box it into the firewall package's
// SDSCertProvisioner interface ONLY on the success arm — a typed-nil
// boxed into the interface passes the stack's nil-guard and a later
// dispatch panics. All failures return errors; the caller logs
// event=sds_certs_unavailable and degrades (wildcard chains keep their
// static certs).
func NewCPProvisioner() (*Service, error) {
	issuer, err := infracerts.Load(consts.CPInfraCACertPath, consts.CPInfraCAKeyPath)
	if err != nil {
		return nil, fmt.Errorf("infracerts load: %w", err)
	}
	destDir, err := consts.SDSClientsDir()
	if err != nil {
		return nil, fmt.Errorf("resolving sds-clients dir: %w", err)
	}
	rootCABytes, err := os.ReadFile(consts.CPCACertPath)
	if err != nil {
		return nil, fmt.Errorf("reading CLI root CA at %s: %w", consts.CPCACertPath, err)
	}
	return New(issuer, destDir, rootCABytes)
}

// writeAtomic writes data via a same-dir temp file + rename so readers
// (the bind-mounted Envoy sibling) never observe a partial file.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName) // best-effort cleanup; the write error is the failure that matters
		return fmt.Errorf("write temp file: %w", writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		_ = os.Remove(tmpName) // best-effort cleanup; the close error is the failure that matters
		return fmt.Errorf("close temp file: %w", closeErr)
	}
	// 0o644: the non-root bind-mounted Envoy reader (UID 101) must read
	// these; the 0o755 dir + file world-read pair is the lane's contract.
	if chmodErr := os.Chmod(tmpName, 0o644); chmodErr != nil { //nolint:gosec // G302: non-root Envoy reads
		_ = os.Remove(tmpName) // best-effort cleanup; the chmod error is the failure that matters
		return fmt.Errorf("chmod temp file: %w", chmodErr)
	}
	if renameErr := os.Rename(tmpName, path); renameErr != nil {
		return fmt.Errorf("rename temp file: %w", renameErr)
	}
	return nil
}
