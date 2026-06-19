package firewall_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
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
			name: "exact allows get subdomain-deny templates; deny gets NXDOMAIN zone; IPs skipped",
			rules: []config.EgressRule{
				{Dst: "github.com", Action: "allow"},
				{Dst: "api.github.com", Action: "allow"},
				{Dst: "registry.npmjs.org", Action: "allow"},
				{Dst: "10.0.0.0/8", Action: "allow"},      // CIDR — skip
				{Dst: "192.168.1.1", Action: "allow"},     // IP — skip
				{Dst: "evil.example.com", Action: "deny"}, // deny — NXDOMAIN zone
				{Dst: "proxy.golang.org", Action: "allow"},
				{Dst: "github.com", Action: "allow"}, // duplicate — skip
			},
			goldenFile: "corefile_basic.golden",
		},
		{
			// Wildcard (.X) forwards the subtree; an exact rule keeps subdomain
			// blocking; a wildcard coexisting with an exact apex (github.com +
			// .github.com) collapses to plain subtree forward; an exact deny
			// under a wildcard allow (.somedomain.com + deny sub.somedomain.com)
			// emits a more-specific NXDOMAIN zone that wins via longest-zone.
			name: "wildcard, exact, wildcard+exact coexist, and deny-under-wildcard",
			rules: []config.EgressRule{
				{Dst: ".datadoghq.com", Action: "allow"},
				{Dst: "api.anthropic.com", Action: "allow"},
				{Dst: ".somedomain.com", Action: "allow"},
				{Dst: "sub.somedomain.com", Action: "deny"},
				{Dst: "github.com", Action: "allow"},
				{Dst: ".github.com", Action: "allow"},
			},
			goldenFile: "corefile_wildcard_deny.golden",
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

// A wildcard rule carrying an unknown/typo'd action (e.g. "allwo") must not widen
// DNS scope. It contributes nothing to the allow set, so a colliding exact-host
// allow has to stay exact-only (subdomains NXDOMAIN'd) — otherwise a single config
// typo silently reopens the DNS subtree-exfil channel. Fail-closed means the typo'd
// rule is inert, so output must equal the exact-allow-alone baseline byte-for-byte.
func TestGenerateCorefile_UnknownActionDoesNotWidenScope(t *testing.T) {
	baseline, err := firewall.GenerateCorefile([]config.EgressRule{
		{Dst: "example.com", Action: "allow"},
	}, 18902)
	require.NoError(t, err)

	withTypo, err := firewall.GenerateCorefile([]config.EgressRule{
		{Dst: "example.com", Action: "allow"},
		{Dst: ".example.com", Action: "allwo"}, // typo: unknown action, not an effective allow
	}, 18902)
	require.NoError(t, err)

	assert.Equal(t, string(baseline), string(withTypo),
		"typo'd-action wildcard rule must not flip example.com out of exact-only scoping")
}
