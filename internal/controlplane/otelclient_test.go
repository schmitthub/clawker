package controlplane_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane"
	"github.com/schmitthub/clawker/internal/logger"
)

func TestNewOtelLoggerProvider_RequiredFields(t *testing.T) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	log := logger.Nop()

	cases := []struct {
		name string
		opts controlplane.OtelClientOptions
		want string
	}{
		{
			name: "missing endpoint",
			opts: controlplane.OtelClientOptions{TLSConfig: tlsCfg, ServiceName: "svc", Log: log, PreflightTimeout: -1},
			want: "Endpoint required",
		},
		{
			name: "missing tls config",
			opts: controlplane.OtelClientOptions{Endpoint: "host:1234", ServiceName: "svc", Log: log, PreflightTimeout: -1},
			want: "TLSConfig required",
		},
		{
			name: "missing service name",
			opts: controlplane.OtelClientOptions{Endpoint: "host:1234", TLSConfig: tlsCfg, Log: log, PreflightTimeout: -1},
			want: "ServiceName required",
		},
		{
			name: "missing log",
			opts: controlplane.OtelClientOptions{Endpoint: "host:1234", TLSConfig: tlsCfg, ServiceName: "svc", PreflightTimeout: -1},
			want: "Log required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := controlplane.NewOtelLoggerProvider(tc.opts)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.want)
			}
			if provider != nil {
				t.Fatalf("want nil provider on error, got %v", provider)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// TestNewOtelLoggerProvider_PreflightFailure verifies the degraded-
// path contract: an unreachable collector at startup yields an error
// rather than a provider that buffers forever. The CP main wiring
// relies on this to emit event=netlogger_unavailable instead of
// pinning a goroutine.
func TestNewOtelLoggerProvider_PreflightFailure(t *testing.T) {
	// Bind, then close — we want a port that's known-closed but
	// without depending on a fixed number being free on the host.
	lis, err := net.Listen("tcp", consts.LoopbackIPv4+":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()

	provider, err := controlplane.NewOtelLoggerProvider(controlplane.OtelClientOptions{
		Endpoint:         addr,
		TLSConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		ServiceName:      "test",
		Log:              logger.Nop(),
		PreflightTimeout: 250 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("want preflight error, got provider=%v", provider)
	}
	if provider != nil {
		t.Fatalf("want nil provider on preflight failure, got %v", provider)
	}
	if !strings.Contains(err.Error(), "preflight dial") {
		t.Fatalf("want preflight dial error, got %v", err)
	}
}

// TestNewOtelLoggerProvider_Constructs verifies the happy path: a
// reachable TLS endpoint produces a usable provider. The listener
// completes the TLS handshake then drops the connection; we don't
// implement OTLP, so this only smoke-tests construction and the
// Logger() / Shutdown() lifecycle.
func TestNewOtelLoggerProvider_Constructs(t *testing.T) {
	serverCert, clientTLS := selfSignedTLSPair(t)
	lis, err := tls.Listen("tcp", consts.LoopbackIPv4+":0", &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	go acceptAndClose(lis)

	provider, err := controlplane.NewOtelLoggerProvider(controlplane.OtelClientOptions{
		Endpoint:            lis.Addr().String(),
		TLSConfig:           clientTLS,
		ServiceName:         "test-service",
		Log:                 logger.Nop(),
		PreflightTimeout:    2 * time.Second,
		RetryMaxElapsedTime: -1, // disable retry; not under test here
		ExportInterval:      time.Hour,
		ExportTimeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("NewOtelLoggerProvider: %v", err)
	}
	if provider == nil {
		t.Fatalf("want non-nil provider")
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = provider.Shutdown(shutdownCtx)
	})

	if got := provider.Logger("scope-a"); got == nil {
		t.Fatalf("Logger(scope-a) returned nil")
	}
}

// selfSignedTLSPair returns a TLS cert for the server side of a test
// listener plus a *tls.Config trusting that cert as the root. The
// client path exercises real root-pool validation — InsecureSkipVerify
// is deliberately not used.
func selfSignedTLSPair(t *testing.T) (tls.Certificate, *tls.Config) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "otelclient-test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:              []string{"localhost"},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}

	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	clientTLS := &tls.Config{
		RootCAs:    pool,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS12,
	}
	return cert, clientTLS
}

func acceptAndClose(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		if tlsConn, ok := conn.(*tls.Conn); ok {
			_ = tlsConn.Handshake()
		}
		_ = conn.Close()
	}
}
