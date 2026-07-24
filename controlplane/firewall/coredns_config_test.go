package firewall_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// corefileTestIDs builds a deterministic IdentityResolver for Corefile
// golden tests: reserved internal hosts then rule dsts (normalized), sorted,
// assigned sequential identities from MinIdentity. Stable across runs so the
// hand-edited goldens can pin exact dnsbpf directive arguments.
func corefileTestIDs(rules []config.EgressRule) firewall.IdentityResolver {
	dsts := append([]string{"docker.internal"}, consts.MonitoringServiceHostnames...)
	for _, r := range rules {
		dsts = append(dsts, strings.TrimSuffix(strings.TrimPrefix(r.Dst, "."), "."))
	}
	sort.Strings(dsts)
	ids := make(map[string]ebpf.RouteIdentity, len(dsts))
	next := firewall.MinIdentity
	for _, d := range dsts {
		if d == "" {
			continue
		}
		if _, ok := ids[d]; ok {
			continue
		}
		ids[d] = next
		next++
	}
	return func(dst string) (ebpf.RouteIdentity, bool) {
		id, ok := ids[dst]
		return id, ok
	}
}

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
			got, missed, err := firewall.GenerateCorefile(tt.rules, 18902, corefileTestIDs(tt.rules))
			require.NoError(t, err)
			assert.Empty(t, missed, "corefileTestIDs answers every dst — no misses expected")

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
	got, missed, err := firewall.GenerateCorefile(nil, 18902, corefileTestIDs(nil))
	require.NoError(t, err)
	assert.Empty(t, missed)

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
	baselineRules := []config.EgressRule{{
		Dst: "example.com", Action: "allow",
		Proto: "", Port: "", PathRules: nil, PathDefault: "", InsecureSkipTLSVerify: false,
	}}
	baseline, baselineMissed, err := firewall.GenerateCorefile(baselineRules, 18902, corefileTestIDs(baselineRules))
	require.NoError(t, err)
	assert.Empty(t, baselineMissed)

	typoRules := []config.EgressRule{
		{Dst: "example.com", Action: "allow"},
		{Dst: ".example.com", Action: "allwo"}, // typo: unknown action, not an effective allow
	}
	withTypo, typoMissed, err := firewall.GenerateCorefile(typoRules, 18902, corefileTestIDs(baselineRules))
	require.NoError(t, err)
	assert.Empty(t, typoMissed)

	assert.Equal(t, string(baseline), string(withTypo),
		"typo'd-action wildcard rule must not flip example.com out of exact-only scoping")
}

// TestGenerateCorefile_ResolverMissOmitsDnsbpf pins the fail-closed contract
// for a PARTIAL resolver miss: the missed domain keeps its forward zone
// (resolution keeps working) but gets no dnsbpf directive (nothing written to
// dns_cache — connect() denies), resolvable domains keep theirs, and the miss
// is reported to the caller.
func TestGenerateCorefile_ResolverMissOmitsDnsbpf(t *testing.T) {
	const missDst = "missing.example.com"
	rules := []config.EgressRule{
		{
			Dst: "github.com", Action: "allow",
			Proto: "", Port: "", PathRules: nil, PathDefault: "", InsecureSkipTLSVerify: false,
		},
		{
			Dst: missDst, Action: "allow",
			Proto: "", Port: "", PathRules: nil, PathDefault: "", InsecureSkipTLSVerify: false,
		},
	}
	full := corefileTestIDs(rules)
	missOne := func(dst string) (ebpf.RouteIdentity, bool) {
		if dst == missDst {
			return 0, false
		}
		return full(dst)
	}

	got, missed, err := firewall.GenerateCorefile(rules, 18902, missOne)
	require.NoError(t, err)

	assert.Equal(t, []string{missDst}, missed)

	zone := func(domain string) string {
		for block := range strings.SplitSeq(string(got), "\n\n") {
			if strings.HasPrefix(block, domain+" {") {
				return block
			}
		}
		return ""
	}

	missedZone := zone(missDst)
	require.NotEmpty(t, missedZone, "missed domain must keep its forward zone")
	assert.Contains(t, missedZone, "forward .", "missed domain must still forward (resolution keeps working)")
	assert.NotContains(t, missedZone, "dnsbpf", "missed domain must not carry a dnsbpf directive")

	githubZone := zone("github.com")
	require.NotEmpty(t, githubZone)
	githubID, ok := full("github.com")
	require.True(t, ok)
	assert.Contains(t, githubZone, fmt.Sprintf("dnsbpf %d", githubID),
		"resolvable domain must keep its dnsbpf directive")
}
