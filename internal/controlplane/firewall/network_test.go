package firewall_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fwcp "github.com/schmitthub/clawker/internal/controlplane/firewall"
)

func TestComputeStaticIP(t *testing.T) {
	cases := []struct {
		name      string
		gateway   string
		lastOctet byte
		want      string
		wantErr   bool
	}{
		{"replaces_last_octet", "172.20.0.1", 2, "172.20.0.2", false},
		{"zero_octet_allowed", "172.20.0.1", 0, "172.20.0.0", false},
		{"broadcast_octet_allowed", "172.20.0.1", 255, "172.20.0.255", false},
		{"idempotent_same_octet", "192.168.1.5", 5, "192.168.1.5", false},
		{"ipv6_rejected", "fd00::1", 2, "", true},
		{"ipv4_in_ipv6_rejected", "::ffff:172.20.0.1", 2, "", true},
		{"zero_addr_rejected", "", 2, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gw netip.Addr
			if tc.gateway != "" {
				parsed, err := netip.ParseAddr(tc.gateway)
				require.NoError(t, err)
				gw = parsed
			}
			got, err := fwcp.ComputeStaticIP(gw, tc.lastOctet)
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, netip.Addr{}, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.String())
			assert.True(t, got.Is4())
		})
	}
}
