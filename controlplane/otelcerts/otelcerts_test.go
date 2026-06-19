package otelcerts

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeIssuer implements Issuer with deterministic mint outputs the
// test can drive. Callers can swap in invalid PEM to exercise the
// pair-check failure path. `calls` records every serviceName MintClient
// was invoked with so tests can pin the `-otel-client` suffix contract.
type fakeIssuer struct {
	mints     int
	calls     []string
	failNext  bool
	bogusPair bool
}

func (f *fakeIssuer) MintClient(serviceName string, _ time.Duration) ([]byte, []byte, error) {
	f.mints++
	f.calls = append(f.calls, serviceName)
	if f.failNext {
		f.failNext = false
		return nil, nil, errors.New("forced mint failure")
	}
	if f.bogusPair {
		// Cert and key from two unrelated key pairs — tls.X509KeyPair
		// catches the mismatch.
		certPEM, _ := newSelfSignedLeaf(t1Key())
		_, keyPEM := newSelfSignedLeaf(t2Key())
		return certPEM, keyPEM, nil
	}
	certPEM, keyPEM := newSelfSignedLeaf(t1Key())
	return certPEM, keyPEM, nil
}

func newSelfSignedLeaf(key *ecdsa.PrivateKey) ([]byte, []byte) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-otel-client"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		panic(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// Two distinct keys so tests can build provably mismatched pairs.
var (
	tKey1 *ecdsa.PrivateKey
	tKey2 *ecdsa.PrivateKey
)

func t1Key() *ecdsa.PrivateKey {
	if tKey1 == nil {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		tKey1 = k
	}
	return tKey1
}

func t2Key() *ecdsa.PrivateKey {
	if tKey2 == nil {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		tKey2 = k
	}
	return tKey2
}

func mustRootCAPEM() []byte {
	pemBytes, _ := newSelfSignedLeaf(t1Key())
	return pemBytes
}

// TestNew_RejectsInvalidArgs pins the message contract on each
// constructor guard. ErrorContains on the message (not just
// require.Error) is the part that catches a future refactor that
// re-orders the guards or copy-pastes the wrong error string.
func TestNew_RejectsInvalidArgs(t *testing.T) {
	tests := []struct {
		name        string
		issuer      Issuer
		destDir     string
		rootCABytes []byte
		wantSubstr  string
	}{
		{
			name:        "nil issuer",
			issuer:      nil,
			destDir:     t.TempDir(),
			rootCABytes: mustRootCAPEM(),
			wantSubstr:  "issuer must not be nil",
		},
		{
			name:        "empty destDir",
			issuer:      &fakeIssuer{},
			destDir:     "",
			rootCABytes: mustRootCAPEM(),
			wantSubstr:  "destDir must not be empty",
		},
		{
			name:        "empty rootCABytes",
			issuer:      &fakeIssuer{},
			destDir:     t.TempDir(),
			rootCABytes: nil,
			wantSubstr:  "rootCABytes must not be empty",
		},
		{
			name:        "garbage rootCABytes",
			issuer:      &fakeIssuer{},
			destDir:     t.TempDir(),
			rootCABytes: []byte("not a PEM block"),
			wantSubstr:  "no parseable PEM certificates",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.issuer, tt.destDir, tt.rootCABytes, nil)
			require.ErrorContains(t, err, tt.wantSubstr)
		})
	}
}

func TestEnsureClient_WritesAllThreeFiles(t *testing.T) {
	dir := t.TempDir()
	ca := mustRootCAPEM()
	iss := &fakeIssuer{}
	s, err := New(iss, dir, ca, nil)
	require.NoError(t, err)

	certPath, keyPath, caPath, err := s.EnsureClient("envoy")
	require.NoError(t, err)

	require.FileExists(t, certPath)
	require.FileExists(t, keyPath)
	require.FileExists(t, caPath)

	got, err := os.ReadFile(caPath)
	require.NoError(t, err)
	require.Equal(t, ca, got)

	// The `-otel-client` suffix on the serviceName passed to MintClient
	// is load-bearing for the OTel collector's authz config — the
	// otlp/infra receiver's `client_ca_file` chain is keyed on the leaf
	// CN. A future refactor that drops or changes the suffix would
	// silently break trusted-lane mTLS until the receiver config is
	// also updated.
	require.Equal(t, []string{"envoy-otel-client"}, iss.calls)
}

func TestEnsureClient_FilePermsForDockerBindMount(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("perm assertions are POSIX-only")
	}
	dir := t.TempDir()
	s, err := New(&fakeIssuer{}, dir, mustRootCAPEM(), nil)
	require.NoError(t, err)

	certPath, keyPath, caPath, err := s.EnsureClient("envoy")
	require.NoError(t, err)

	// Per-svc dir 0o755: in-container UID 101 must be able to traverse
	// to the bind-mounted key, so the directory is intentionally world-
	// executable.
	svcDir := filepath.Dir(certPath)
	info, err := os.Stat(svcDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())

	for _, p := range []string{certPath, keyPath, caPath} {
		info, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o644), info.Mode().Perm(), p)
	}
}

// TestServiceMethods_RejectEmptyService pins the svc-must-not-be-empty
// guard across both consumption shapes. Message contract is asserted
// so a future refactor that returns a different sentinel breaks the
// test, not silently changes the API.
func TestServiceMethods_RejectEmptyService(t *testing.T) {
	s, err := New(&fakeIssuer{}, t.TempDir(), mustRootCAPEM(), nil)
	require.NoError(t, err)

	t.Run("EnsureClient", func(t *testing.T) {
		_, _, _, err := s.EnsureClient("")
		require.ErrorContains(t, err, "svc must not be empty")
	})
	t.Run("LoadTLSConfig", func(t *testing.T) {
		_, err := s.LoadTLSConfig("")
		require.ErrorContains(t, err, "svc must not be empty")
	})
}

func TestEnsureClient_MintFailureLeavesNoTmpFiles(t *testing.T) {
	dir := t.TempDir()
	iss := &fakeIssuer{failNext: true}
	s, err := New(iss, dir, mustRootCAPEM(), nil)
	require.NoError(t, err)

	_, _, _, err = s.EnsureClient("envoy")
	require.Error(t, err)

	// MkdirAll ran before the mint, so the svc dir exists — but no
	// .pem/.key/.tmp files should have been written.
	svcDir := filepath.Join(dir, "envoy")
	entries, err := os.ReadDir(svcDir)
	require.NoError(t, err)
	require.Empty(t, entries, "no files should land on a mint failure")
}

func TestEnsureClient_PairCheckRejectsMismatched(t *testing.T) {
	dir := t.TempDir()
	iss := &fakeIssuer{bogusPair: true}
	s, err := New(iss, dir, mustRootCAPEM(), nil)
	require.NoError(t, err)

	_, _, _, err = s.EnsureClient("envoy")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cert/key pair")
}

func TestEnsureClient_AtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	iss := &fakeIssuer{}
	s, err := New(iss, dir, mustRootCAPEM(), nil)
	require.NoError(t, err)

	certPath, _, _, err := s.EnsureClient("envoy")
	require.NoError(t, err)
	first, err := os.ReadFile(certPath)
	require.NoError(t, err)

	// Force the next mint to produce different bytes by rotating the
	// underlying test key.
	tKey1 = nil
	_, _, _, err = s.EnsureClient("envoy")
	require.NoError(t, err)
	second, err := os.ReadFile(certPath)
	require.NoError(t, err)

	require.NotEqual(t, first, second, "second EnsureClient should overwrite cert in place")

	// No stale .tmp left behind.
	entries, err := os.ReadDir(filepath.Join(dir, "envoy"))
	require.NoError(t, err)
	for _, e := range entries {
		require.NotContains(t, e.Name(), ".tmp")
	}
}

func TestLoadTLSConfig_GetClientCertificateRemintsEachCall(t *testing.T) {
	iss := &fakeIssuer{}
	s, err := New(iss, t.TempDir(), mustRootCAPEM(), nil)
	require.NoError(t, err)

	cfg, err := s.LoadTLSConfig("cp")
	require.NoError(t, err)
	require.NotNil(t, cfg.GetClientCertificate)
	require.Equal(t, uint16(tls12()), cfg.MinVersion)
	require.NotNil(t, cfg.RootCAs)

	require.Equal(t, 0, iss.mints, "constructing the config does not mint")

	_, err = cfg.GetClientCertificate(nil)
	require.NoError(t, err)
	require.Equal(t, 1, iss.mints)

	_, err = cfg.GetClientCertificate(nil)
	require.NoError(t, err)
	require.Equal(t, 2, iss.mints, "each handshake re-mints")
}

func TestLoadTLSConfig_PropagatesMintFailure(t *testing.T) {
	iss := &fakeIssuer{}
	s, err := New(iss, t.TempDir(), mustRootCAPEM(), nil)
	require.NoError(t, err)

	cfg, err := s.LoadTLSConfig("cp")
	require.NoError(t, err)

	iss.failNext = true
	_, err = cfg.GetClientCertificate(nil)
	require.Error(t, err)
}

// tls12 returns tls.VersionTLS12 without dragging the crypto/tls
// import into the test file's symbol surface — keeps the assertion
// readable.
func tls12() int {
	// 0x0303 is the wire constant for TLS 1.2; see RFC 5246 §A.1.
	return 0x0303
}
