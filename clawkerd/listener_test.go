package clawkerd

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
	// spawnEntry is a no-op: these tests exercise listener TLS guards
	// + audit logs, not the AgentReady handler.
	clawkerdv1.RegisterClawkerdServiceServer(srv, &clawkerdServer{
		log:        logger.NewWriter(logBuf),
		spawnEntry: func(string) error { return nil },
	})

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

// driveAndRequireError opens a Session, optionally Recv()s once to
// surface deferred handshake errors (some platforms do not error on
// the unary Session call itself), and asserts the dial path fails.
// Used by the three rejection tests below.
func driveAndRequireError(t *testing.T, conn *grpc.ClientConn, label string) {
	t.Helper()
	client := clawkerdv1.NewClawkerdServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Session(ctx)
	if err == nil {
		_, err = stream.Recv()
	}
	require.Error(t, err, label)
}

// TestListener_RejectsNoClientCert covers the load-bearing
// RequireAndVerifyClientCert path: a client that presents NO cert at
// all must be rejected at the TLS layer before any handler runs. This
// is the entire defense against unauthenticated peers reaching the
// root-level ShellCommand surface.
func TestListener_RejectsNoClientCert(t *testing.T) {
	stack := buildBufconnTLSStack(t)
	var logBuf bytes.Buffer
	_, lis := startListenerOnBufconn(t, stack, &logBuf)

	// Empty Certificates slice → client presents nothing during the
	// TLS handshake. RequireAndVerifyClientCert rejects.
	tlsCfg := &tls.Config{
		RootCAs:    stack.caPool,
		ServerName: "clawker.test.agent",
		MinVersion: tls.VersionTLS13,
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

	driveAndRequireError(t, conn, "listener must reject client with no cert")
}

// TestListener_RejectsUntrustedCAClientCert covers the chain-validation
// path: a client cert that is structurally valid but signed by a CA
// the listener does not trust must be rejected at the TLS layer (NOT
// reach pinPeerCNToCP). Without this, a third-party CA whose key
// leaked could mint a `ContainerCP`-CN cert and bypass the CN pin.
func TestListener_RejectsUntrustedCAClientCert(t *testing.T) {
	stack := buildBufconnTLSStack(t)
	var logBuf bytes.Buffer
	_, lis := startListenerOnBufconn(t, stack, &logBuf)

	// Mint a separate, untrusted CA + a client cert under it. The
	// listener's ClientCAs is the trusted CA's pool only, so the
	// chain check fails before pinPeerCNToCP runs. CN is set to
	// ContainerCP to prove the rejection happens at the chain check
	// (not the CN pin) — defense-in-depth ordering matters.
	rogueCAKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	rogueSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	now := time.Now()
	rogueCATmpl := &x509.Certificate{
		SerialNumber:          rogueSerial,
		Subject:               pkix.Name{CommonName: "rogue-CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rogueCADER, err := x509.CreateCertificate(rand.Reader, rogueCATmpl, rogueCATmpl, &rogueCAKey.PublicKey, rogueCAKey)
	require.NoError(t, err)
	rogueCACert, err := x509.ParseCertificate(rogueCADER)
	require.NoError(t, err)

	rogueClient, _ := signLeaf(t, rogueCACert, rogueCAKey, consts.ContainerCP,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})

	// Dial uses the trusted CA pool for server verify (so the dial
	// can validate the listener's server cert) but presents the
	// rogue-signed client cert. The listener's ClientCAs rejects.
	conn := dialBufconn(t, lis, rogueClient, stack.caPool, "clawker.test.agent")
	driveAndRequireError(t, conn, "listener must reject client cert signed by untrusted CA")
}

// TestBuildListenerTLSConfig_RejectsMalformedKeypair feeds garbage
// cert/key PEMs through the production constructor. The error
// must propagate so a regression that swallowed it (e.g. returning
// an empty *tls.Config + nil) couldn't pass — `tls.X509KeyPair` is
// the gate that anchors every downstream guard, and silently
// returning a default config would hand the listener a server with
// no usable cert.
func TestBuildListenerTLSConfig_RejectsMalformedKeypair(t *testing.T) {
	stack := buildBufconnTLSStack(t)
	cfg, err := buildListenerTLSConfig([]byte("not a cert"), []byte("not a key"), stack.caPEM)
	require.Error(t, err)
	require.Nil(t, cfg, "constructor must NOT return a usable *tls.Config when the keypair is invalid")
	assert.Contains(t, err.Error(), "agent leaf keypair")
}

// TestBuildListenerTLSConfig_RejectsUnparseableCAPEM covers the
// AppendCertsFromPEM-returns-false branch. A garbage CA PEM means
// ClientCAs cannot be populated, so RequireAndVerifyClientCert can
// never validate. Constructor must reject up front rather than
// hand back a config whose ClientCAs is empty.
func TestBuildListenerTLSConfig_RejectsUnparseableCAPEM(t *testing.T) {
	stack := buildBufconnTLSStack(t)
	cfg, err := buildListenerTLSConfig(stack.serverPEMs.cert, stack.serverPEMs.key, []byte("not a PEM"))
	require.Error(t, err)
	require.Nil(t, cfg, "constructor must NOT return a usable *tls.Config when the CA bundle is unparseable")
	assert.Contains(t, err.Error(), "CA PEM did not parse")
}

// TestListener_RejectsPlainTCP drives a raw (non-TLS) TCP write into
// the listener's bufconn endpoint and asserts the server tears the
// connection down without producing a response. The listener requires
// TLS handshake; bytes that don't begin a ClientHello are rejected.
func TestListener_RejectsPlainTCP(t *testing.T) {
	stack := buildBufconnTLSStack(t)
	var logBuf bytes.Buffer
	_, lis := startListenerOnBufconn(t, stack, &logBuf)

	rawConn, err := lis.Dial()
	require.NoError(t, err)
	t.Cleanup(func() { rawConn.Close() })

	// Send junk that's structurally not a TLS ClientHello. The server
	// will read, fail TLS handshake, and close the connection. A
	// subsequent Read on this side returns an error (typically EOF or
	// a reset). If the server were to mis-route this to a handler,
	// the read would either block waiting for a gRPC frame response
	// or surface a non-error successful read of structured bytes —
	// both are regressions.
	require.NoError(t, rawConn.SetDeadline(time.Now().Add(2*time.Second)))
	_, _ = rawConn.Write([]byte("not a TLS ClientHello\r\n"))

	buf := make([]byte, 64)
	_, readErr := rawConn.Read(buf)
	require.Error(t, readErr, "listener must close plain-TCP connection")
}
