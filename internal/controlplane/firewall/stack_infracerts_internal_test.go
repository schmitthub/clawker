package firewall

import (
	"errors"
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

	issuer := &fakeIssuer{
		chainPEM: []byte("---CHAIN---\n"),
		keyPEM:   []byte("---KEY---\n"),
	}
	s := NewStack(nil, cfg, logger.Nop(), nil, issuer)

	require.NoError(t, s.ensureInfraClientCerts())

	assert.ElementsMatch(t, []string{"envoy-otel-client", "coredns-otel-client"}, issuer.calls)

	dir, err := consts.FirewallOtelClientsDir()
	require.NoError(t, err)

	gotCA, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	require.NoError(t, err)
	assert.Equal(t, caBytes, gotCA)

	for _, svc := range []string{"envoy", "coredns"} {
		certPath := filepath.Join(dir, svc, "client.pem")
		keyPath := filepath.Join(dir, svc, "client.key")

		cert, err := os.ReadFile(certPath)
		require.NoError(t, err, "%s cert", svc)
		assert.Equal(t, issuer.chainPEM, cert)

		key, err := os.ReadFile(keyPath)
		require.NoError(t, err, "%s key", svc)
		assert.Equal(t, issuer.keyPEM, key)

		// 0o644 on the key is intentional — CoreDNS upstream runs as a
		// non-root uid and a stricter mode silently breaks load.
		info, err := os.Stat(keyPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "%s key mode", svc)
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
	_, err = os.Stat(filepath.Join(dir, "ca.pem"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "ca.pem should not exist when issuer is nil")
}
