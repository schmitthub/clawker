package firewall

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/schmitthub/clawker/internal/controlplane"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// File names under <firewallDataDir>/cp-certs/ — must match the server-side
// issuer in internal/controlplane/ca.go. If these ever drift, clients will
// fail to load certs and refuse to talk to the CP.
const (
	cpCertsDirName       = "cp-certs"
	cpCAClientFileName   = "cp-ca.pem"
	cpClientCLICertFile  = "cp-client-cli.pem"
	cpClientCLIKeyFile   = "cp-client-cli.key"
	cpGRPCSocketFileName = "cp.sock"
	cpOIDCSocketFileName = "cp-oidc.sock"

	// Placeholder host for the /token URL passed to clientcredentials.Config.
	// The underlying HTTP transport dials a UDS, so the URL host is only used
	// by the TLS handshake as the ServerName — not for network routing.
	// It must match the server cert's SANs (see cp-server SANs in
	// internal/controlplane/ca.go: "clawker-cp", "localhost", "127.0.0.1").
	cpTLSServerName = "clawker-cp"
)

// CPClientPaths holds the on-disk locations for the CLI's copy of the CP
// CA and client cert. Populated by LoadCPClientPaths and used by both the
// /token HTTP client and the gRPC dialer.
type CPClientPaths struct {
	CACertPEM     string // cp-ca.pem (public half)
	ClientCertPEM string // cp-client-cli.pem
	ClientKeyPEM  string // cp-client-cli.key
	GRPCSocket    string // cp.sock
	OIDCSocket    string // cp-oidc.sock
}

// LoadCPClientPaths returns the canonical paths for the CP client certs
// under the firewall data directory. The CP server writes these files on
// every boot; the CLI reads them lazily on first use.
func LoadCPClientPaths(firewallDataDir string) CPClientPaths {
	certsDir := filepath.Join(firewallDataDir, cpCertsDirName)
	return CPClientPaths{
		CACertPEM:     filepath.Join(certsDir, cpCAClientFileName),
		ClientCertPEM: filepath.Join(certsDir, cpClientCLICertFile),
		ClientKeyPEM:  filepath.Join(certsDir, cpClientCLIKeyFile),
		GRPCSocket:    filepath.Join(firewallDataDir, cpGRPCSocketFileName),
		OIDCSocket:    filepath.Join(firewallDataDir, cpOIDCSocketFileName),
	}
}

// BuildCPTLSConfig constructs the *tls.Config the CLI uses to dial the
// CP's UDS listeners with mTLS. Reads the CA cert, client cert, and
// client key from the paths computed by LoadCPClientPaths.
//
// Returns an error if any file is missing or malformed — the caller
// (typically firewall.NewManager) should surface this to the user via
// the firewall manager's error path, since it indicates the CP has not
// been started yet or its data directory is corrupted.
func BuildCPTLSConfig(paths CPClientPaths) (*tls.Config, error) {
	caPEM, err := os.ReadFile(paths.CACertPEM)
	if err != nil {
		return nil, fmt.Errorf("read cp ca cert %s: %w", paths.CACertPEM, err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("parse cp ca cert %s: no PEM blocks found", paths.CACertPEM)
	}

	// Sanity-check the CA block decodes to a real certificate. Not
	// strictly required (AppendCertsFromPEM validates internally) but
	// gives a better error message on the "wrong file type" case.
	if block, _ := pem.Decode(caPEM); block == nil {
		return nil, fmt.Errorf("parse cp ca cert %s: not PEM-encoded", paths.CACertPEM)
	}

	clientCert, err := tls.LoadX509KeyPair(paths.ClientCertPEM, paths.ClientKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("load cp client cert %s / %s: %w",
			paths.ClientCertPEM, paths.ClientKeyPEM, err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		ServerName:   cpTLSServerName,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// NewCPTokenSource returns an oauth2.TokenSource that fetches JWTs from
// the CP's OIDC /token endpoint via HTTPS-over-UDS with mTLS client auth.
// The returned TokenSource caches tokens until expiry and automatically
// refreshes them by calling /token again when a cached token is about to
// expire. Pair this with grpc/credentials/oauth.TokenSource on a gRPC
// ClientConn to attach the current JWT as `authorization: bearer ...`
// metadata on every RPC.
//
// The TokenSource does not perform any network I/O until Token() is
// first called — construction is cheap and safe during NewManager.
func NewCPTokenSource(ctx context.Context, paths CPClientPaths, tlsCfg *tls.Config) oauth2.TokenSource {
	httpClient := &http.Client{
		Transport: controlplane.UnixHTTPTransport(paths.OIDCSocket, tlsCfg),
		Timeout:   10 * time.Second,
	}

	// Store the HTTP client on the context so clientcredentials.Config
	// picks it up — this is the x/oauth2 convention for injecting a
	// custom transport into the library's token fetch loop.
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	cfg := &clientcredentials.Config{
		ClientID: controlplane.ClientIDCLI,
		// TokenURL host is ignored (the UDS transport dials a fixed
		// socket path), but it must parse as a valid URL and match the
		// server cert's SAN for TLS verification.
		TokenURL:  "https://" + cpTLSServerName + "/token",
		AuthStyle: oauth2.AuthStyleInHeader,
		Scopes:    []string{controlplane.ScopeFirewallAdmin},
	}

	return cfg.TokenSource(ctx)
}

// NewCPUDSDialer returns a grpc.WithContextDialer-compatible dial function
// for the CP's gRPC UDS listener. grpc-go's NewClient normally dials TCP;
// this function makes it dial a Unix socket instead while keeping the
// mTLS transport credentials intact.
func NewCPUDSDialer(socketPath string) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socketPath)
	}
}

// ensureCPClientReady verifies the CP cert material exists on disk and
// is readable. Intended as a pre-flight check before NewManager tries
// to build the gRPC client. If the files are missing the error message
// guides the user toward the firewall manager's EnsureRunning flow (which
// starts the CP and triggers cert generation).
func ensureCPClientReady(paths CPClientPaths) error {
	required := []string{paths.CACertPEM, paths.ClientCertPEM, paths.ClientKeyPEM}
	for _, path := range required {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf(
					"cp certs not ready (%s does not exist); the clawker-cp container "+
						"must be started before its cert material is available",
					path,
				)
			}
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	return nil
}
