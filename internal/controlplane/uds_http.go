package controlplane

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// ListenUnix creates a Unix domain socket listener at socketPath, removing
// any stale socket file from a previous run. The file permissions are set
// so only the calling process's user/group can connect; callers relying on
// wider access must chmod after the listener is created.
//
// Returns the net.Listener; callers are responsible for Close().
func ListenUnix(socketPath string) (net.Listener, error) {
	// A leftover socket from a crashed previous run prevents Listen from
	// binding. Removing it is safe because ListenUnix will fail below if
	// another live process owns the address.
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket %s: %w", socketPath, err)
	}

	addr, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("resolve unix addr %s: %w", socketPath, err)
	}
	lis, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", socketPath, err)
	}

	// File permissions on the socket. 0660 lets the host user + group dial;
	// the firewall data directory's own permissions bound who can even see
	// the file. Callers with stricter needs can chmod after Listen.
	if err := os.Chmod(socketPath, 0o660); err != nil {
		_ = lis.Close()
		return nil, fmt.Errorf("chmod socket %s: %w", socketPath, err)
	}

	return lis, nil
}

// NewTLSHTTPServer returns a ready-to-Serve *http.Server configured with the
// given TLS config and handler. Timeouts are set conservatively to limit
// slow-client resource holding against the OIDC token endpoint.
func NewTLSHTTPServer(handler http.Handler, tlsConfig *tls.Config) *http.Server {
	return &http.Server{
		Handler:           handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}

// ServeTLSOnListener runs srv.ServeTLS on lis using the cert and key from
// srv.TLSConfig. ServeTLS normally requires cert/key file paths; wrapping
// it with a tls.Listener lets us pass pre-loaded in-memory certs instead.
func ServeTLSOnListener(srv *http.Server, lis net.Listener) error {
	if srv.TLSConfig == nil {
		return fmt.Errorf("controlplane: ServeTLSOnListener requires srv.TLSConfig")
	}
	tlsListener := tls.NewListener(lis, srv.TLSConfig)
	return srv.Serve(tlsListener)
}

// UnixHTTPTransport builds an *http.Transport whose DialContext always
// routes to socketPath regardless of the URL host. Callers use this with
// an *http.Client to make HTTPS requests over a Unix domain socket:
//
//	client := &http.Client{Transport: UnixHTTPTransport(socketPath, tlsConfig)}
//	resp, _ := client.Get("https://placeholder/endpoint")
//
// The URL host is ignored at the transport layer — only the tlsConfig's
// ServerName and certificate validation see it.
func UnixHTTPTransport(socketPath string, tlsConfig *tls.Config) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
}
