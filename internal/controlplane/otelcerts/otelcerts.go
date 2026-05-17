// Package otelcerts mints and provisions short-lived mTLS client
// material for the trusted OTLP/infra lane.
//
// Trust model:
//
//	CLI root CA  (server cert for otel-collector — clients verify it)
//	  └── infra intermediate CA  (client_ca_file for otlp/infra receiver)
//	        ├── envoy-otel-client     (leaf, bind-mounted into envoy)
//	        ├── coredns-otel-client   (leaf, bind-mounted into coredns)
//	        └── cp-otel-client        (leaf, in-process via tls.Config)
//
// The otel-collector's otlp/infra receiver's `client_ca_file` is the
// infra intermediate CA. Agent containers hold leaves signed directly
// by the CLI root with no path through the intermediate, so their
// handshake fails the receiver's chain validation. The CLI root CA is
// still used on the *client* side (RootCAs / ca.pem on disk) to verify
// the otel-collector's server cert.
//
// Service is consumed in two shapes:
//
//   - EnsureClient writes <destDir>/<svc>/{client.pem,client.key,ca.pem}
//     for sibling containers (envoy, coredns) to bind-mount. CP is the
//     sole writer. Re-runs on firewall.Stack.Reload rotate the leaves
//     in place; container restarts pick up the new files on next
//     handshake.
//
//   - LoadTLSConfig returns a *tls.Config with a GetClientCertificate
//     hook that re-mints the leaf on every handshake. Consumed by the
//     CP's in-process OTLP exporter so the leaf material never lands on
//     disk and rotation matches the connection lifecycle. Matches the
//     CoreDNS plugin rotation pattern.
//
// Layering: this package lives outside internal/controlplane/firewall
// because the firewall is one of several consumers, not the owner. The
// historical home (firewall/stack.go::ensureInfraClientCerts) was a
// layering violation — see feedback_no_layering_violations.md.
package otelcerts

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

// leafTTL is the validity window applied to every minted leaf.
// Matches the previous firewall.Stack.ensureInfraClientCerts behavior
// and the MITM domain cert shape; rotation is driven by firewall
// Reload (disk path) or per-handshake re-mint (in-process path), not
// by approaching expiry.
const leafTTL = 365 * 24 * time.Hour

// Issuer is the minimal surface this package needs from
// infracerts.Issuer. Stated as an interface so tests can stub mint
// outcomes without depending on the real intermediate-CA load.
type Issuer interface {
	MintClient(serviceName string, ttl time.Duration) (chainPEM, keyPEM []byte, err error)
}

// Service mints and provisions trusted-lane OTel client material.
//
// Construction is restricted to call sites that have already loaded
// the infra intermediate; nil Issuer is rejected at New time so
// degraded-mode wiring is forced through the typed-nil
// (*Service)(nil) sentinel — callers pass nil into the firewall stack
// and CP exporter wiring drops the lane entirely.
type Service struct {
	issuer  Issuer
	destDir string // host-FS dir; per-svc subdirs land underneath
	rootCA  []byte // CLI root CA PEM (server-side trust anchor)
	log     *logger.Logger
}

// New constructs a Service.
//
//   - issuer must be non-nil; pass nil into downstream wiring instead
//     if no intermediate is available, which propagates the degraded
//     mode cleanly.
//   - destDir is the parent host-FS directory under which per-service
//     subdirs are written. Resolved via consts.OtelClientsDir() in
//     production.
//   - rootCABytes is the CLI root CA PEM used both for the on-disk
//     ca.pem copy (so sibling containers can bind-mount it) and the
//     in-process RootCAs pool (verifies otel-collector server cert).
//   - log may be nil; defaults to logger.Nop.
func New(issuer Issuer, destDir string, rootCABytes []byte, log *logger.Logger) (*Service, error) {
	if issuer == nil {
		return nil, fmt.Errorf("otelcerts: issuer must not be nil")
	}
	if destDir == "" {
		return nil, fmt.Errorf("otelcerts: destDir must not be empty")
	}
	if len(rootCABytes) == 0 {
		return nil, fmt.Errorf("otelcerts: rootCABytes must not be empty")
	}
	if log == nil {
		log = logger.Nop()
	}
	return &Service{
		issuer:  issuer,
		destDir: destDir,
		rootCA:  rootCABytes,
		log:     log,
	}, nil
}

// EnsureClient mints a fresh leaf for svc and writes
//
//	<destDir>/<svc>/client.pem  (leaf + intermediate chain)
//	<destDir>/<svc>/client.key  (leaf private key)
//	<destDir>/<svc>/ca.pem      (CLI root CA, for server-cert verification)
//
// atomically (tmp + rename) and returns absolute paths suitable for
// Docker bind-mount sources. Re-runs overwrite in place.
//
// Permission shape is 0o755 on the per-svc dir and 0o644 on all three
// files. The directory mode is load-bearing for non-root in-container
// readers: Envoy distroless runs UID 101 and Docker bind-mounts
// preserve host inode perms, so a 0o700 dir blocks traversal even when
// the file itself would be readable. 0o644 on the key file is the
// relevant attack surface and is constrained by the same UID-traversal
// rule.
func (s *Service) EnsureClient(svc string) (certPath, keyPath, caPath string, err error) {
	if svc == "" {
		return "", "", "", fmt.Errorf("otelcerts: svc must not be empty")
	}

	svcDir := filepath.Join(s.destDir, svc)
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		return "", "", "", fmt.Errorf("otelcerts: create %s dir: %w", svc, err)
	}

	chainPEM, keyPEM, err := s.issuer.MintClient(svc+"-otel-client", leafTTL)
	if err != nil {
		return "", "", "", fmt.Errorf("otelcerts: mint %s leaf: %w", svc, err)
	}

	// Pair-check before any disk write commits. A corrupted output
	// (mismatched cert/key, malformed PEM) caught here fails the call
	// so callers leave their ready-flag false instead of half-writing
	// a stale-good pair into a broken state.
	if _, err := tls.X509KeyPair(chainPEM, keyPEM); err != nil {
		return "", "", "", fmt.Errorf("otelcerts: validate %s cert/key pair: %w", svc, err)
	}

	certPath = filepath.Join(svcDir, "client.pem")
	keyPath = filepath.Join(svcDir, "client.key")
	caPath = filepath.Join(svcDir, "ca.pem")

	if err := writeFileAtomic(caPath, s.rootCA, 0o644); err != nil {
		return "", "", "", fmt.Errorf("otelcerts: write %s ca.pem: %w", svc, err)
	}
	if err := writeFileAtomic(certPath, chainPEM, 0o644); err != nil {
		return "", "", "", fmt.Errorf("otelcerts: write %s client.pem: %w", svc, err)
	}
	if err := writeFileAtomic(keyPath, keyPEM, 0o644); err != nil {
		return "", "", "", fmt.Errorf("otelcerts: write %s client.key: %w", svc, err)
	}
	return certPath, keyPath, caPath, nil
}

// LoadTLSConfig returns a *tls.Config for an in-process OTLP exporter
// that authenticates as svc on the trusted lane. The leaf is re-minted
// on every TLS handshake via GetClientCertificate, so leaves never
// land on disk and rotation matches the connection lifecycle of the
// gRPC client.
//
// The returned config trusts the CLI root CA (RootCAs) for verifying
// the otel-collector server cert. MinVersion is TLS 1.2 to match the
// receiver's posture.
//
// Issuer key rotation (a `clawker auth rotate` that re-issues the
// intermediate) is NOT picked up at runtime — the GetClientCertificate
// closure holds the Service reference and the Service's Issuer is the
// one loaded at CP startup. Document this as a v1 limitation; restart
// CP after a rotation.
func (s *Service) LoadTLSConfig(svc string) (*tls.Config, error) {
	if svc == "" {
		return nil, fmt.Errorf("otelcerts: svc must not be empty")
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(s.rootCA) {
		return nil, fmt.Errorf("otelcerts: root CA bundle contains no PEM blocks")
	}

	var (
		mu     sync.Mutex
		cached *tls.Certificate
	)

	mint := func() (*tls.Certificate, error) {
		chainPEM, keyPEM, err := s.issuer.MintClient(svc+"-otel-client", leafTTL)
		if err != nil {
			return nil, fmt.Errorf("otelcerts: mint %s leaf: %w", svc, err)
		}
		cert, err := tls.X509KeyPair(chainPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("otelcerts: validate %s cert/key pair: %w", svc, err)
		}
		return &cert, nil
	}

	return &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
		GetClientCertificate: func(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			mu.Lock()
			defer mu.Unlock()
			cert, err := mint()
			if err != nil {
				return nil, err
			}
			cached = cert
			return cached, nil
		},
	}, nil
}

// writeFileAtomic writes data to path via tmp file + os.Rename so a
// partial write (ENOSPC, EINTR) leaves any pre-existing file intact.
// Same-filesystem rename is atomic on POSIX; the .tmp lives in the
// same directory as path.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
