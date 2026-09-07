package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"testing"
	"time"

	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	secretservice "github.com/envoyproxy/go-control-plane/envoy/service/secret/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/test/bufconn"

	"github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/logger"
)

func TestStopSDSServer(t *testing.T) {
	for _, tt := range []struct {
		name   string
		active bool
	}{
		{name: "idle", active: false},
		{name: "active delta stream", active: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server, clientTLS := newSDSStopTestServer(t)
			t.Cleanup(server.Stop)
			if tt.active {
				openSDSStream(t, server, clientTLS)
			}

			var output bytes.Buffer
			done := make(chan struct{})
			go func() {
				stopSDSServer(server, logger.NewWriter(&output))
				close(done)
			}()
			timer := time.NewTimer(2 * defaultShutdownWait)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				t.Fatal("SDS shutdown did not return while its stream was open")
			}
			if tt.active {
				assert.Contains(t, output.String(), "sds_graceful_stop_timeout")
				assert.Contains(t, output.String(), "firewall.sds")
			} else {
				assert.Empty(t, output.String())
			}
		})
	}
}

func newSDSStopTestServer(t *testing.T) (*grpc.Server, *tls.Config) {
	t.Helper()
	const hostname = "stop.sds.test"
	caCert, caKey, err := firewall.EnsureCA(t.TempDir())
	require.NoError(t, err)
	certPEM, keyPEM, err := firewall.GenerateDomainCert(caCert, caKey, hostname)
	require.NoError(t, err)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	//nolint:exhaustruct,exhaustruct_v5 // Other TLS fields use the standard defaults.
	clientTLS := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: hostname,
		RootCAs:    x509.NewCertPool(),
	}
	clientTLS.RootCAs.AddCert(caCert)
	return grpc.NewServer(grpc.Creds(credentials.NewServerTLSFromCert(&cert))), clientTLS
}

func openSDSStream(t *testing.T, server *grpc.Server, clientTLS *tls.Config) {
	t.Helper()
	store, err := firewall.NewRulesStoreFromString("rules: []")
	require.NoError(t, err)
	certDir := t.TempDir()
	sds, err := firewall.NewSDSServer(firewall.SDSServerDeps{
		Store:     store,
		CertDirFn: func() (string, error) { return certDir, nil },
		Log:       logger.Nop(),
	})
	require.NoError(t, err)
	secretservice.RegisterSecretDiscoveryServiceServer(server, sds)
	listener := bufconn.Listen(1 << 20)
	t.Cleanup(func() { assert.NoError(t, listener.Close()) })
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Errorf("serve SDS: %v", serveErr)
		}
	}()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })
	stream, err := secretservice.NewSecretDiscoveryServiceClient(conn).DeltaSecrets(t.Context())
	require.NoError(t, err)
	req := new(discovery.DeltaDiscoveryRequest)
	req.TypeUrl = firewall.SDSSecretTypeURL
	req.ResourceNamesSubscribe = []string{"probe.example.com"}
	require.NoError(t, stream.Send(req))
	_, err = stream.Recv()
	require.NoError(t, err)
}
