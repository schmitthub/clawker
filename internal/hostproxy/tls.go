package hostproxy

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// ServerTLSConfig is the host proxy's mTLS server config: the host proxy
// server leaf, and a client cert chained to the CLI CA on every
// connection. requireControlPlane narrows the host actions to the CP.
func ServerTLSConfig() (*tls.Config, error) {
	certPath, err := consts.AuthHostProxyServerCertPath()
	if err != nil {
		return nil, fmt.Errorf("host proxy server cert path: %w", err)
	}
	keyPath, err := consts.AuthHostProxyServerKeyPath()
	if err != nil {
		return nil, fmt.Errorf("host proxy server key path: %w", err)
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load host proxy server cert: %w", err)
	}
	pool, err := caPool()
	if err != nil {
		return nil, err
	}
	var cfg tls.Config
	cfg.MinVersion = tls.VersionTLS13
	cfg.Certificates = []tls.Certificate{cert}
	cfg.ClientAuth = tls.RequireAndVerifyClientCert
	cfg.ClientCAs = pool
	return &cfg, nil
}

// ClientTLSConfig is the config the CLI uses to reach the host proxy: it
// trusts the CLI CA and presents the cert at certPath, if one is given.
func ClientTLSConfig(certPath, keyPath string) (*tls.Config, error) {
	pool, err := caPool()
	if err != nil {
		return nil, err
	}
	var cfg tls.Config
	cfg.MinVersion = tls.VersionTLS13
	cfg.RootCAs = pool
	if certPath == "" {
		return &cfg, nil
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load host proxy client cert: %w", err)
	}
	cfg.Certificates = []tls.Certificate{cert}
	return &cfg, nil
}

func caPool() (*x509.CertPool, error) {
	caPath, err := consts.AuthCACertPath()
	if err != nil {
		return nil, fmt.Errorf("CLI CA path: %w", err)
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CLI CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("CLI CA file holds no certificate")
	}
	return pool, nil
}

// requireControlPlane admits only the CP's client cert (CN pinned to
// consts.ContainerCP) to a host action. The TLS layer already verified the
// chain to the CLI CA.
func requireControlPlane(log *logger.Logger, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isControlPlanePeer(r.TLS) {
			log.Warn().
				Str("event", "host_proxy_caller_rejected").
				Str("path", r.URL.Path).
				Msg("host action from a caller that is not the control plane")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// isControlPlanePeer reports whether the verified peer cert names the CP.
func isControlPlanePeer(state *tls.ConnectionState) bool {
	if state == nil || len(state.PeerCertificates) == 0 {
		return false
	}
	cn := state.PeerCertificates[0].Subject.CommonName
	return subtle.ConstantTimeCompare([]byte(cn), []byte(consts.ContainerCP)) == 1
}
