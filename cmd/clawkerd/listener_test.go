package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/test/bufconn"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// --- pinPeerCNToCP unit tests --------------------------------------
//
// pinPeerCNToCP runs after Go's TLS chain validation, so testing it
// in isolation against synthesized cert chains is the right level of
// granularity. Driving these branches via a real TLS handshake is
// impossible for the EKU case — the TLS layer already enforces
// ClientAuth on client certs and would reject before pinPeerCNToCP
// ever ran, defeating the point of the defense-in-depth assertion.

func certWithCNAndEKUs(t *testing.T, cn string, ekus []x509.ExtKeyUsage) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  ekus,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	c, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return c
}

func TestPinPeerCNToCP_AcceptsValidCNWithClientAuthEKU(t *testing.T) {
	cert := certWithCNAndEKUs(t, consts.ContainerCP, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	require.NoError(t, pinPeerCNToCP(nil, [][]*x509.Certificate{{cert}}))
}

func TestPinPeerCNToCP_RejectsBadCN(t *testing.T) {
	cert := certWithCNAndEKUs(t, "evil-impostor", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	err := pinPeerCNToCP(nil, [][]*x509.Certificate{{cert}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "peer CN not authorized")
}

func TestPinPeerCNToCP_RejectsMissingClientAuthEKU(t *testing.T) {
	// Correct CN but only ServerAuth EKU — TLS layer would normally
	// reject this before reaching pinPeerCNToCP, but the app-layer
	// assertion is the load-bearing defense if TLS verify is ever
	// loosened. Test the assertion directly.
	cert := certWithCNAndEKUs(t, consts.ContainerCP, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	err := pinPeerCNToCP(nil, [][]*x509.Certificate{{cert}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing ClientAuth EKU")
}

func TestPinPeerCNToCP_RejectsEmptyChain(t *testing.T) {
	err := pinPeerCNToCP(nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no verified peer chain")

	err = pinPeerCNToCP(nil, [][]*x509.Certificate{{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no verified peer chain")
}

// --- runSession audit log integration test -------------------------
//
// Exercising runSession's audit log path requires real peer cert
// extraction, which means a real (bufconn) mTLS handshake. This test
// stands up a self-contained CA + agent leaf + CP client cert, runs
// the production listener TLS config, opens a Session stream, then
// closes it and asserts both audit events were emitted with the
// expected fields.

type bufconnTLSStack struct {
	caPool       *x509.CertPool
	caCert       *x509.Certificate
	caKey        *ecdsa.PrivateKey
	serverCert   tls.Certificate // agent leaf — dual-EKU
	cpClientCert tls.Certificate // CN=ContainerCP, ClientAuth EKU
	caPEM        []byte
	serverPEMs   pemPair
}

type pemPair struct {
	cert []byte
	key  []byte
}

// buildBufconnTLSStack mints a fresh CA + a server cert (agent leaf
// with dual ClientAuth+ServerAuth EKU, mirroring auth.MintAgentCert)
// + a client cert with CN=ContainerCP and ClientAuth EKU. Uses a
// self-contained CA so the test does not depend on testenv state or
// auth.EnsureAuthMaterial side effects.
func buildBufconnTLSStack(t *testing.T) bufconnTLSStack {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "test-CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	serverCert, serverPEMs := signLeaf(t, caCert, caKey, "clawker.test.agent",
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth})
	cpClientCert, _ := signLeaf(t, caCert, caKey, consts.ContainerCP,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(caPEM))

	return bufconnTLSStack{
		caPool:       pool,
		caCert:       caCert,
		caKey:        caKey,
		serverCert:   serverCert,
		cpClientCert: cpClientCert,
		caPEM:        caPEM,
		serverPEMs:   serverPEMs,
	}
}

func signLeaf(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, ekus []x509.ExtKeyUsage) (tls.Certificate, pemPair) {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  ekus,
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	return pair, pemPair{cert: certPEM, key: keyPEM}
}

// startListenerOnBufconn spins up the production listener TLS config
// (via buildListenerTLSConfig) on a bufconn listener and registers
// clawkerdServer with a logger that drains to logBuf. Returns a
// connect helper the caller uses to dial the same bufconn.
func startListenerOnBufconn(t *testing.T, stack bufconnTLSStack, logBuf *bytes.Buffer) (*grpc.Server, *bufconn.Listener) {
	t.Helper()

	tlsCfg, err := buildListenerTLSConfig(stack.serverPEMs.cert, stack.serverPEMs.key, stack.caPEM)
	require.NoError(t, err)

	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	clawkerdv1.RegisterClawkerdServiceServer(srv, &clawkerdServer{log: logger.NewWriter(logBuf)})

	lis := bufconn.Listen(1 << 20)
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.Stop)
	return srv, lis
}

func dialBufconn(t *testing.T, lis *bufconn.Listener, clientCert tls.Certificate, caPool *x509.CertPool, serverName string) *grpc.ClientConn {
	t.Helper()
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}
	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestRunSession_LogsAuditOnEntryAndExit(t *testing.T) {
	stack := buildBufconnTLSStack(t)
	var logBuf bytes.Buffer
	_, lis := startListenerOnBufconn(t, stack, &logBuf)

	conn := dialBufconn(t, lis, stack.cpClientCert, stack.caPool, "clawker.test.agent")
	client := clawkerdv1.NewClawkerdServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Session(ctx)
	require.NoError(t, err)

	// CloseSend → server's stream.Recv() returns io.EOF → runReceiver
	// returns nil → runSession defer fires session_ended.
	require.NoError(t, stream.CloseSend())

	// Drain server-side responses until EOF so the server-side stream
	// fully tears down before we assert on log output.
	for {
		if _, recvErr := stream.Recv(); recvErr != nil {
			break
		}
	}

	// Give the server goroutine a tick to flush the deferred log.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logBuf.String(), "session_ended") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	out := logBuf.String()
	assert.Contains(t, out, `"event":"session_started"`)
	assert.Contains(t, out, `"event":"session_ended"`)
	assert.Contains(t, out, `"peer_cn":"`+consts.ContainerCP+`"`)
	assert.Contains(t, out, `"peer_thumbprint":"`)
	assert.Contains(t, out, `"duration":`)
}

// TestListener_RejectsBadCN drives a full TLS handshake with a client
// cert whose CN is wrong but whose chain validates. The handshake
// succeeds at the TLS layer, then VerifyPeerCertificate runs
// pinPeerCNToCP which rejects. Surfaces as a connection-level error
// on the client's first stream operation.
func TestListener_RejectsBadCN(t *testing.T) {
	stack := buildBufconnTLSStack(t)
	var logBuf bytes.Buffer
	_, lis := startListenerOnBufconn(t, stack, &logBuf)

	// Mint a fresh client cert with a non-ContainerCP CN, signed by
	// the same test CA so the chain validates and reaches
	// pinPeerCNToCP.
	badClient, _ := signLeaf(t, stack.caCert, stack.caKey, "evil-cn",
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})

	conn := dialBufconn(t, lis, badClient, stack.caPool, "clawker.test.agent")
	client := clawkerdv1.NewClawkerdServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream, err := client.Session(ctx)
	if err == nil {
		// Some platforms surface the rejection on first Recv rather
		// than the Session call itself.
		_, err = stream.Recv()
	}
	require.Error(t, err, "listener must reject bad-CN client")
}
