package firewall

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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// mintTestCertKeyPEM produces a self-signed ECDSA cert + matching key
// as PEM. ensureInfraClientCerts now validates issuer output via
// tls.X509KeyPair before committing to disk; tests must provide a real
// matching pair instead of opaque bytes.
func mintTestCertKeyPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "test-infra-leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// fakeIssuer is a deterministic InfraIssuer for tests.
type fakeIssuer struct {
	calls    []string
	chainPEM []byte
	keyPEM   []byte
}

func (f *fakeIssuer) MintClient(svc string, _ time.Duration) ([]byte, []byte, error) {
	f.calls = append(f.calls, svc)
	return f.chainPEM, f.keyPEM, nil
}

// TestStack_ensureInfraClientCerts_WritesPerServiceMaterial pins the
// filesystem contract this helper has with the Envoy + CoreDNS
// container specs. The bind-mount Sources in
// envoyContainerSpec/corednsContainerSpec point at the exact paths
// this test asserts on; a drift between the two surfaces is silent
// at compile time and only fails at handshake-time in a running
// stack.
func TestStack_ensureInfraClientCerts_WritesPerServiceMaterial(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)

	caBytes := []byte("---FAKE-CA---\n")
	caSrc := filepath.Join(t.TempDir(), "root-ca.pem")
	require.NoError(t, os.WriteFile(caSrc, caBytes, 0o644))
	prev := rootCASourcePath
	rootCASourcePath = func() string { return caSrc }
	t.Cleanup(func() { rootCASourcePath = prev })

	chainPEM, keyPEM := mintTestCertKeyPEM(t)
	issuer := &fakeIssuer{
		chainPEM: chainPEM,
		keyPEM:   keyPEM,
	}
	s := NewStack(nil, cfg, logger.Nop(), nil, issuer)

	require.NoError(t, s.ensureInfraClientCerts())

	assert.ElementsMatch(t, []string{"envoy-otel-client", "coredns-otel-client"}, issuer.calls)

	dir, err := consts.FirewallOtelClientsDir()
	require.NoError(t, err)

	for _, svc := range []string{"envoy", "coredns"} {
		svcDir := filepath.Join(dir, svc)
		certPath := filepath.Join(svcDir, "client.pem")
		keyPath := filepath.Join(svcDir, "client.key")
		caPath := filepath.Join(svcDir, "ca.pem")

		cert, err := os.ReadFile(certPath)
		require.NoError(t, err, "%s cert", svc)
		assert.Equal(t, issuer.chainPEM, cert)

		key, err := os.ReadFile(keyPath)
		require.NoError(t, err, "%s key", svc)
		assert.Equal(t, issuer.keyPEM, key)

		serviceCA, err := os.ReadFile(caPath)
		require.NoError(t, err, "%s ca", svc)
		assert.Equal(t, caBytes, serviceCA)

		// 0o644 on the key is intentional — CoreDNS upstream runs as a
		// non-root uid and a stricter mode silently breaks load.
		info, err := os.Stat(keyPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "%s key mode", svc)

		// 0o700 on the per-service dir is the defense-in-depth the
		// 0o644 key file relies on: a non-root host user cannot
		// traverse into svcDir to read the otherwise world-readable
		// bind-mounted key. If this assertion ever drifts, the
		// "trusted infra-issuer-signed client" trust boundary on the
		// otlp/infra receiver collapses (any local user can forge
		// Envoy/CoreDNS log lines).
		dirInfo, err := os.Stat(svcDir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm(), "%s dir mode", svc)
	}
}

// TestStack_ensureInfraClientCerts_NilIssuer_NoOp pins the degraded-
// mode invariant: when the CP-side intermediate load fails at startup,
// Stack comes up cleanly with no cert files written. Sibling Envoy/
// CoreDNS specs omit the mTLS mounts in this state, so any written
// files would be unreferenced.
func TestStack_ensureInfraClientCerts_NilIssuer_NoOp(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)
	s := NewStack(nil, cfg, logger.Nop(), nil, nil)

	require.NoError(t, s.ensureInfraClientCerts())

	dir, err := consts.FirewallOtelClientsDir()
	require.NoError(t, err)
	for _, svc := range []string{"envoy", "coredns"} {
		_, err = os.Stat(filepath.Join(dir, svc, "ca.pem"))
		assert.True(t, errors.Is(err, os.ErrNotExist), "%s/ca.pem should not exist when issuer is nil", svc)
	}
}
