package controlplane

import (
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
)

// leafWithSAN builds a bare parsed-cert stand-in carrying the given DNS
// SANs. requireSDSClientSAN only reads DNSNames from the already-verified
// leaf, so no signing is needed here — chain validity is the TLS stack's
// job (ClientAuth: RequireAndVerifyClientCert) and is not re-tested.
func leafWithSAN(sans ...string) *x509.Certificate {
	return &x509.Certificate{DNSNames: sans} //nolint:exhaustruct,exhaustruct_v5 // only DNSNames is read by the pin
}

// TestRequireSDSClientSAN pins the SDS listener's per-service authz: chain
// validation admits ANY infra-intermediate-signed leaf, so the pin must
// separate the dedicated SDS identity from every other infra client —
// most importantly the telemetry lane's envoy-otel-client leaf, which is
// signed by the same intermediate and would otherwise be able to fetch
// minted MITM certificates.
func TestRequireSDSClientSAN(t *testing.T) {
	t.Run("dedicated SDS leaf admitted", func(t *testing.T) {
		chains := [][]*x509.Certificate{{leafWithSAN(consts.EnvoySDSClientName)}}
		require.NoError(t, requireSDSClientSAN(chains))
	})

	t.Run("telemetry lane leaf refused", func(t *testing.T) {
		chains := [][]*x509.Certificate{{leafWithSAN("envoy-otel-client")}}
		err := requireSDSClientSAN(chains)
		require.Error(t, err)
		assert.Contains(t, err.Error(), consts.EnvoySDSClientName)
	})

	t.Run("no SANs refused", func(t *testing.T) {
		require.Error(t, requireSDSClientSAN([][]*x509.Certificate{{leafWithSAN()}}))
	})

	t.Run("empty chain set refused", func(t *testing.T) {
		require.Error(t, requireSDSClientSAN(nil))
	})
}
