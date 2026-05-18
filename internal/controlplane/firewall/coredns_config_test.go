package firewall_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/firewall"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateCorefile(t *testing.T) {
	tests := []struct {
		name       string
		rules      []config.EgressRule
		goldenFile string
	}{
		{
			name: "basic rules: allow domains, skip IPs, skip deny",
			rules: []config.EgressRule{
				{Dst: "github.com", Action: "allow"},
				{Dst: "api.github.com", Action: "allow"},
				{Dst: "registry.npmjs.org", Action: "allow"},
				{Dst: "10.0.0.0/8", Action: "allow"},      // CIDR — skip
				{Dst: "192.168.1.1", Action: "allow"},     // IP — skip
				{Dst: "evil.example.com", Action: "deny"}, // deny — skip
				{Dst: "proxy.golang.org", Action: "allow"},
				{Dst: "github.com", Action: "allow"}, // duplicate — skip
			},
			goldenFile: "corefile_basic.golden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := firewall.GenerateCorefile(tt.rules, 18902)
			require.NoError(t, err)

			goldenPath := filepath.Join("testdata", tt.goldenFile)
			want, err := os.ReadFile(goldenPath)
			require.NoError(t, err, "golden file %s must exist — hand-edit to update", goldenPath)
			assert.Equal(t, string(want), string(got))
		})
	}
}

// TestGenerateCorefile_MonitoringHostnamesEmitted asserts every hostname
// in consts.MonitoringServiceHostnames produces a forward zone in the
// generated Corefile. Drift guard: if a future commit adds an entry to
// MonitoringServiceHostnames without coredns_config rendering it, this
// test fails — keeping the firewall plane and the compose plane in
// lockstep.
func TestGenerateCorefile_MonitoringHostnamesEmitted(t *testing.T) {
	got, err := firewall.GenerateCorefile(nil, 18902)
	require.NoError(t, err)

	for _, host := range consts.MonitoringServiceHostnames {
		zone := fmt.Sprintf("%s {", host)
		assert.Contains(t, string(got), zone, "expected zone block for monitoring hostname %q", host)
	}
}

func TestGenerateCorefile_WildcardDomain(t *testing.T) {
	rules := []config.EgressRule{
		{Dst: ".datadoghq.com", Action: "allow"},
		{Dst: "api.anthropic.com", Action: "allow"},
	}

	got, err := firewall.GenerateCorefile(rules, 18902)
	require.NoError(t, err)

	out := string(got)
	// Leading dot should be stripped — CoreDNS zone "datadoghq.com" matches all subdomains.
	assert.Contains(t, out, "datadoghq.com {")
	assert.NotContains(t, out, ".datadoghq.com {")
	assert.Contains(t, out, "api.anthropic.com {")
}
