package otel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	ctest "github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cannedHandler struct {
	msg        *dns.Msg
	err        error
	returnCode int
}

func (c cannedHandler) ServeDNS(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	if c.err != nil {
		return dns.RcodeServerFailure, c.err
	}
	rc := c.returnCode
	if rc == 0 {
		rc = dns.RcodeSuccess
	}
	if c.msg == nil {
		return rc, nil
	}
	resp := c.msg.Copy()
	resp.SetReply(r)
	// SetReply zeroes Rcode; restore it so callers can drive the
	// downstream-rcode-override branch.
	resp.Rcode = c.msg.Rcode
	resp.Answer = c.msg.Answer
	if err := w.WriteMsg(resp); err != nil {
		return dns.RcodeServerFailure, err
	}
	return rc, nil
}

func (c cannedHandler) Name() string { return "test" }

var _ plugin.Handler = cannedHandler{}

type recordingEmitter struct {
	events []QueryEvent
	err    error
}

func (r *recordingEmitter) Emit(_ context.Context, event QueryEvent) error {
	r.events = append(r.events, event)
	return r.err
}

func answerMsg() *dns.Msg {
	m := new(dns.Msg)
	m.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: "github.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("140.82.121.4"),
		},
	}
	return m
}

func TestServeDNS(t *testing.T) {
	cases := []struct {
		name             string
		zone             string
		question         string
		next             cannedHandler
		emitErr          error
		wantRCode        int
		wantErr          bool
		wantEvents       int
		wantEventRCode   string
		wantEventErr     bool
		wantAnswerLen    int
		wantUpstreamSent bool
	}{
		{
			name:             "success forwards response and emits event",
			zone:             "github.com.",
			question:         "github.com.",
			next:             cannedHandler{msg: answerMsg()},
			wantRCode:        dns.RcodeSuccess,
			wantEvents:       1,
			wantEventRCode:   "NOERROR",
			wantAnswerLen:    1,
			wantUpstreamSent: true,
		},
		{
			name:             "resolver error emits SERVFAIL event and returns error",
			zone:             ".",
			question:         "blocked.example.",
			next:             cannedHandler{err: errors.New("boom")},
			wantRCode:        dns.RcodeServerFailure,
			wantErr:          true,
			wantEvents:       1,
			wantEventRCode:   "SERVFAIL",
			wantEventErr:     true,
			wantAnswerLen:    0,
			wantUpstreamSent: false,
		},
		{
			name:             "emit error does not break DNS forwarding",
			zone:             "github.com.",
			question:         "github.com.",
			next:             cannedHandler{msg: answerMsg()},
			emitErr:          errors.New("export failed"),
			wantRCode:        dns.RcodeSuccess,
			wantEvents:       1,
			wantEventRCode:   "NOERROR",
			wantAnswerLen:    1,
			wantUpstreamSent: true,
		},
		{
			name:             "downstream NXDOMAIN omits answers attribute",
			zone:             ".",
			question:         "blocked.example.",
			next:             cannedHandler{msg: &dns.Msg{MsgHdr: dns.MsgHdr{Rcode: dns.RcodeNameError}}},
			wantRCode:        dns.RcodeSuccess,
			wantEvents:       1,
			wantEventRCode:   "NXDOMAIN",
			wantAnswerLen:    0,
			wantUpstreamSent: true,
		},
		{
			name:             "downstream rcode overrides numeric rcode",
			zone:             "github.com.",
			question:         "github.com.",
			next:             cannedHandler{msg: &dns.Msg{MsgHdr: dns.MsgHdr{Rcode: dns.RcodeRefused}}},
			wantRCode:        dns.RcodeSuccess,
			wantEvents:       1,
			wantEventRCode:   "REFUSED",
			wantAnswerLen:    0,
			wantUpstreamSent: true,
		},
		{
			name:             "no downstream write skips upstream WriteMsg",
			zone:             "github.com.",
			question:         "github.com.",
			next:             cannedHandler{},
			wantRCode:        dns.RcodeSuccess,
			wantEvents:       1,
			wantEventRCode:   "NOERROR",
			wantAnswerLen:    0,
			wantUpstreamSent: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emitter := &recordingEmitter{err: tc.emitErr}
			h := Handler{Next: tc.next, Zone: tc.zone, Emitter: emitter}

			req := new(dns.Msg)
			req.SetQuestion(tc.question, dns.TypeA)
			w := dnstest.NewRecorder(&ctest.ResponseWriter{})

			rcode, err := h.ServeDNS(context.Background(), w, req)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantRCode, rcode)

			require.Len(t, emitter.events, tc.wantEvents)
			event := emitter.events[0]
			assert.Equal(t, tc.wantEventRCode, event.RCode)
			assert.Equal(t, tc.wantAnswerLen, event.AnswerCount)
			assert.Len(t, event.Answers, tc.wantAnswerLen)
			if tc.wantEventErr {
				assert.Error(t, event.Err)
			} else {
				assert.NoError(t, event.Err)
			}
			assert.NotZero(t, event.Timestamp)

			if tc.wantUpstreamSent {
				require.NotNil(t, w.Msg, "production code must call w.WriteMsg on outer writer")
			} else {
				assert.Nil(t, w.Msg, "production code must skip outer WriteMsg")
			}
		})
	}
}

func TestRemoteIP(t *testing.T) {
	cases := []struct {
		name string
		addr net.Addr
		want string
	}{
		{name: "nil addr returns empty", addr: nil, want: ""},
		{name: "tcp host:port splits to host", addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.42"), Port: 5353}, want: "10.0.0.42"},
		{name: "non host:port falls back to String", addr: &net.UnixAddr{Name: "/run/foo.sock", Net: "unix"}, want: "/run/foo.sock"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, remoteIP(tc.addr))
		})
	}
}

func TestNewEmitter_EmptyEndpoint(t *testing.T) {
	em, err := NewEmitter(Options{Endpoint: "   "})
	require.NoError(t, err)
	require.IsType(t, noopEmitter{}, em)
	require.NoError(t, em.Emit(context.Background(), QueryEvent{}))
}

func TestBuildTLSConfig(t *testing.T) {
	dir := t.TempDir()
	cert, key, ca := writeTestCert(t, dir)

	cases := []struct {
		name    string
		opts    Options
		wantErr string
		setup   func(t *testing.T) Options
	}{
		{
			name:    "empty paths rejected",
			opts:    Options{},
			wantErr: "mTLS requires",
		},
		{
			name: "bad keypair file",
			setup: func(t *testing.T) Options {
				bad := filepath.Join(t.TempDir(), "bad.pem")
				require.NoError(t, os.WriteFile(bad, []byte("not a cert"), 0o600))
				return Options{ClientCertFile: bad, ClientKeyFile: bad, CACertFile: ca}
			},
			wantErr: "load client keypair",
		},
		{
			name:    "unreadable CA path",
			opts:    Options{ClientCertFile: cert, ClientKeyFile: key, CACertFile: filepath.Join(dir, "nonexistent.pem")},
			wantErr: "read CA bundle",
		},
		{
			name: "CA file with no PEM blocks",
			setup: func(t *testing.T) Options {
				badCA := filepath.Join(t.TempDir(), "bad-ca.pem")
				require.NoError(t, os.WriteFile(badCA, []byte("garbage bytes, not pem"), 0o600))
				return Options{ClientCertFile: cert, ClientKeyFile: key, CACertFile: badCA}
			},
			wantErr: "no PEM blocks",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts
			if tc.setup != nil {
				opts = tc.setup(t)
			}
			_, err := buildTLSConfig(opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestBuildTLSConfig_GetClientCertificateRotates(t *testing.T) {
	dir := t.TempDir()
	cert, key, ca := writeTestCert(t, dir)

	cfg, err := buildTLSConfig(Options{ClientCertFile: cert, ClientKeyFile: key, CACertFile: ca})
	require.NoError(t, err)
	require.Nil(t, cfg.Certificates, "leaf must come from GetClientCertificate, not static field")
	require.NotNil(t, cfg.GetClientCertificate)

	first, err := cfg.GetClientCertificate(nil)
	require.NoError(t, err)
	require.NotEmpty(t, first.Certificate)
	firstBytes := append([]byte(nil), first.Certificate[0]...)

	// Rotate leaf on disk by writing a freshly-generated cert/key over
	// the same paths. The provider would still hold the old cert if the
	// static tls.Config.Certificates field were used.
	rotDir := t.TempDir()
	newCert, newKey, _ := writeTestCert(t, rotDir)
	newCertBytes, err := os.ReadFile(newCert)
	require.NoError(t, err)
	newKeyBytes, err := os.ReadFile(newKey)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cert, newCertBytes, 0o600))
	require.NoError(t, os.WriteFile(key, newKeyBytes, 0o600))

	second, err := cfg.GetClientCertificate(nil)
	require.NoError(t, err)
	require.NotEqual(t, firstBytes, second.Certificate[0],
		"GetClientCertificate must re-read rotated leaf from disk")
}

// writeTestCert generates a self-signed ECDSA cert + key and writes them
// to dir along with a CA file (the cert itself, since it self-signs).
// Returns the three file paths.
func writeTestCert(t *testing.T, dir string) (certPath, keyPath, caPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "otel-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	certPath = filepath.Join(dir, "client.pem")
	keyPath = filepath.Join(dir, "client.key")
	caPath = filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(certPath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))
	require.NoError(t, os.WriteFile(caPath, certPEM, 0o600))
	return
}
