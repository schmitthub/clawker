package firewall_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"testing"

	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	secretservice "github.com/envoyproxy/go-control-plane/envoy/service/secret/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/test/bufconn"

	"github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/logger"
)

const sdsTestHostname = "sds.test"

const sdsTestRules = `
rules:
  - dst: .suno.com
    proto: https
  - dst: .stream.example.com
    proto: wss
  - dst: api.github.com
    proto: https
  - dst: .denied.io
    proto: https
    action: deny
`

// startSDSTestServer starts the SDS service and returns its client connection.
func startSDSTestServer(t *testing.T) *grpc.ClientConn {
	t.Helper()

	store, err := firewall.NewRulesStoreFromString(sdsTestRules)
	require.NoError(t, err)

	certDir := t.TempDir()
	ca, err := firewall.NewCAStore(func() (string, error) { return certDir, nil }, currentUserOwner())
	require.NoError(t, err)
	srv, err := firewall.NewSDSServer(firewall.SDSServerDeps{
		Store: store,
		CA:    ca,
		Log:   logger.Nop(),
	})
	require.NoError(t, err)

	caCert, caKey, err := ca.Ensure()
	require.NoError(t, err)
	certPEM, keyPEM, err := firewall.GenerateDomainCert(caCert, caKey, sdsTestHostname)
	require.NoError(t, err)
	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewServerTLSFromCert(&serverCert)))
	secretservice.RegisterSecretDiscoveryServiceServer(grpcSrv, srv)
	go func() {
		if serveErr := grpcSrv.Serve(lis); serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Errorf("serve SDS: %v", serveErr)
		}
	}()
	t.Cleanup(grpcSrv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(roots, sdsTestHostname)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })

	return conn
}

func requestSecret(
	t *testing.T,
	conn *grpc.ClientConn,
	name string,
) *corev3.DeltaDiscoveryResponse {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	stream, err := secretservice.NewSecretDiscoveryServiceClient(conn).DeltaSecrets(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&corev3.DeltaDiscoveryRequest{
		TypeUrl:                firewall.SDSSecretTypeURL,
		ResourceNamesSubscribe: []string{name},

		Node:                        nil,
		ResourceNamesUnsubscribe:    nil,
		ResourceLocatorsSubscribe:   nil,
		ResourceLocatorsUnsubscribe: nil,
		InitialResourceVersions:     nil,
		ResponseNonce:               "",
		ErrorDetail:                 nil,
	}))
	resp, err := stream.Recv()
	require.NoError(t, err)
	return resp
}

func TestSDSServer_MintsMultiLabelLeafUnderWildcardZone(t *testing.T) {
	client := startSDSTestServer(t)

	// SNI with multiple labels below the .suno.com wildcard zone — the case the static
	// [apex, *.apex] cert cannot cover (issue #500).
	resp := requestSecret(t, client, "studio-api.prod.suno.com")
	require.Len(t, resp.GetResources(), 1)
	assert.Empty(t, resp.GetRemovedResources())
	assert.Equal(t, "studio-api.prod.suno.com", resp.GetResources()[0].GetName())

	var secret tlsv3.Secret
	require.NoError(t, resp.GetResources()[0].GetResource().UnmarshalTo(&secret))
	tlsCert := secret.GetTlsCertificate()
	require.NotNil(t, tlsCert)

	block, _ := pem.Decode(tlsCert.GetCertificateChain().GetInlineBytes())
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	require.NoError(t, cert.VerifyHostname("studio-api.prod.suno.com"))
	assert.NotEmpty(t, tlsCert.GetPrivateKey().GetInlineBytes())
}

func TestSDSServer_ApexAndWSSZonesAdmitted(t *testing.T) {
	client := startSDSTestServer(t)

	for _, name := range []string{"suno.com", "auth.suno.com", "a.b.stream.example.com"} {
		resp := requestSecret(t, client, name)
		require.Len(t, resp.GetResources(), 1, "SNI %s must mint", name)
		assert.Empty(t, resp.GetRemovedResources())
	}
}

func TestSDSServer_FailsClosed(t *testing.T) {
	client := startSDSTestServer(t)

	cases := []struct {
		name string
		sni  string
	}{
		{"host outside every zone", "evil.example.org"},
		{"exact rule host is not SDS-served", "api.github.com"},
		{"deny wildcard zone", "a.b.denied.io"},
		{"zone-suffix spoof without dot", "evilsuno.com"},
		{"invalid sni", "*.suno.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := requestSecret(t, client, tc.sni)
			assert.Empty(t, resp.GetResources(), "SNI %s must not mint", tc.sni)
			assert.Equal(t, []string{tc.sni}, resp.GetRemovedResources(),
				"denied SNI must be reported as a removed resource so the handshake fails")
		})
	}
}

func TestSDSServer_CachesMintPerSNI(t *testing.T) {
	client := startSDSTestServer(t)

	first := requestSecret(t, client, "auth.suno.com")
	second := requestSecret(t, client, "auth.suno.com")
	require.Len(t, first.GetResources(), 1)
	require.Len(t, second.GetResources(), 1)
	// Same cached leaf: identical version and identical bytes.
	assert.Equal(t, first.GetResources()[0].GetVersion(), second.GetResources()[0].GetVersion())
	assert.Equal(t, first.GetResources()[0].GetResource().GetValue(), second.GetResources()[0].GetResource().GetValue())
}
