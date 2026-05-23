package firewall_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane/firewall"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
)

// TestValidateDst exercises the pure ValidateDst function across the full
// valid/invalid destination matrix. Lives at the store layer because
// ValidateDst is the gatekeeper for anything written to egress-rules.yaml.
func TestValidateDst(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dst     string
		wantErr bool
	}{
		// Valid domains.
		{name: "simple domain", dst: "example.com"},
		{name: "subdomain", dst: "api.github.com"},
		{name: "deep subdomain", dst: "a.b.c.example.com"},
		{name: "wildcard", dst: ".example.com"},
		{name: "trailing dot", dst: "example.com."},
		{name: "wildcard trailing dot", dst: ".example.com."},
		{name: "with hyphen", dst: "my-api.example.com"},
		{name: "with digits", dst: "api2.example.com"},
		{name: "all digits label", dst: "123.example.com"},
		{name: "single label", dst: "localhost"},
		{name: "underscore", dst: "_dmarc.example.com"},
		{name: "digits with hyphen", dst: "123-456"},

		// Case — must be lowercase.
		{name: "uppercase", dst: "EXAMPLE.COM", wantErr: true},
		{name: "mixed case", dst: "Api.GitHub.Com", wantErr: true},
		{name: "wildcard uppercase", dst: ".EXAMPLE.COM", wantErr: true},

		// Multi-dot TLD and new gTLD.
		{name: "co.uk", dst: "api.example.co.uk"},
		{name: "new gTLD", dst: "my.example.technology"},

		// Domain length boundaries (253 chars max after normalization).
		{name: "total 253 chars valid", dst: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)},
		{name: "total 254 chars invalid", dst: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62), wantErr: true},

		// Valid IPs and CIDRs.
		{name: "IPv4", dst: "192.168.1.1"},
		{name: "IPv6", dst: "::1"},
		{name: "CIDR", dst: "10.0.0.0/8"},

		// Wildcard-prefixed IPs/CIDRs — wildcards only make sense for domains.
		{name: "wildcard IPv4", dst: ".192.168.1.1", wantErr: true},
		{name: "wildcard CIDR", dst: ".10.0.0.0/8", wantErr: true},
		{name: "wildcard IPv6", dst: ".::1", wantErr: true},

		// Invalid.
		{name: "empty", dst: "", wantErr: true},
		{name: "just dot", dst: ".", wantErr: true},
		{name: "just dots", dst: "..", wantErr: true},
		{name: "spaces", dst: "example .com", wantErr: true},
		{name: "leading hyphen", dst: "-example.com", wantErr: true},
		{name: "trailing hyphen label", dst: "example-.com", wantErr: true},
		{name: "special chars", dst: "example!.com", wantErr: true},
		{name: "at sign", dst: "user@example.com", wantErr: true},
		{name: "path", dst: "example.com/path", wantErr: true},
		{name: "port", dst: "example.com:443", wantErr: true},
		{name: "scheme", dst: "https://example.com", wantErr: true},
		{name: "empty label", dst: "example..com", wantErr: true},
		{name: "double trailing dot", dst: "example.com..", wantErr: true},
		{name: "wildcard double trailing dot", dst: ".example.com..", wantErr: true},
		{name: "label too long", dst: strings.Repeat("a", 64) + ".com", wantErr: true},
		{name: "all numeric", dst: "123.456", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := firewall.ValidateDst(tt.dst)
			if tt.wantErr {
				assert.Error(t, err, "ValidateDst(%q) should fail", tt.dst)
			} else {
				assert.NoError(t, err, "ValidateDst(%q) should succeed", tt.dst)
			}
		})
	}
}

// TestRoutesFromRules_TCPMappingsParity locks in the invariant that
// RoutesFromRules and TCPMappings agree on (a) which rules produce
// output, (b) the effective destination port for TCP/SSH rules, and
// (c) the Envoy listener port assigned per rule. A mismatch silently
// misroutes traffic — e.g. an SSH rule whose BPF route points at the
// main TLS listener gets reset by tls_inspector's deny chain.
func TestRoutesFromRules_TCPMappingsParity(t *testing.T) {
	t.Parallel()
	ports := firewall.EnvoyPorts{EgressPort: 10000, TCPPortBase: 11000}

	tests := []struct {
		name  string
		rules []config.EgressRule
		want  []ebpf.Route
	}{
		{
			name: "tcp rule with empty action is treated as allow",
			rules: []config.EgressRule{
				{Dst: "github.com", Proto: "tcp", Port: 8080 /* Action: "" */},
			},
			want: []ebpf.Route{
				{DomainHash: ebpf.DomainHash("github.com"), DstPort: 8080, EnvoyPort: 11000},
			},
		},
		{
			name: "ssh rule with port=0 uses default 22",
			rules: []config.EgressRule{
				{Dst: "git.example.com", Proto: "ssh", Action: "allow" /* Port: 0 */},
			},
			want: []ebpf.Route{
				{DomainHash: ebpf.DomainHash("git.example.com"), DstPort: 22, EnvoyPort: 11000},
			},
		},
		{
			name: "tcp rule with port=0 uses default 443",
			rules: []config.EgressRule{
				{Dst: "a.example.com", Proto: "tcp", Action: "allow" /* Port: 0 */},
			},
			want: []ebpf.Route{
				{DomainHash: ebpf.DomainHash("a.example.com"), DstPort: 443, EnvoyPort: 11000},
			},
		},
		{
			name: "multiple tcp rules assign sequential EnvoyPorts, port defaulting does not desync the index",
			rules: []config.EgressRule{
				{Dst: "a.example.com", Proto: "tcp", Action: "allow" /* Port: 0 */},
				{Dst: "b.example.com", Proto: "tcp", Action: "allow", Port: 8080},
			},
			want: []ebpf.Route{
				{DomainHash: ebpf.DomainHash("a.example.com"), DstPort: 443, EnvoyPort: 11000},
				{DomainHash: ebpf.DomainHash("b.example.com"), DstPort: 8080, EnvoyPort: 11001},
			},
		},
		{
			name: "tls rule still routes to the main egress listener",
			rules: []config.EgressRule{
				{Dst: "api.example.com", Proto: "tls", Action: "allow", Port: 443},
			},
			want: []ebpf.Route{
				{DomainHash: ebpf.DomainHash("api.example.com"), DstPort: 443, EnvoyPort: 10000},
			},
		},
		{
			name: "deny rules produce no routes",
			rules: []config.EgressRule{
				{Dst: "api.example.com", Proto: "tls", Action: "deny", Port: 443},
				{Dst: "git.example.com", Proto: "ssh", Action: "deny"},
			},
			want: []ebpf.Route{},
		},
		{
			name: "ip and cidr destinations are skipped",
			rules: []config.EgressRule{
				{Dst: "10.0.0.1", Proto: "tcp", Action: "allow", Port: 22},
				{Dst: "10.0.0.0/24", Proto: "tls", Action: "allow", Port: 443},
			},
			want: []ebpf.Route{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := firewall.RoutesFromRules(tt.rules, ports)
			require.Equal(t, tt.want, got)

			// Parity check: every TCP/SSH mapping TCPMappings produces
			// must appear in the route set. This is the invariant that
			// guards against Envoy-side and eBPF-side drift.
			for _, m := range firewall.TCPMappings(tt.rules, ports) {
				want := ebpf.Route{
					DomainHash: ebpf.DomainHash(m.Dst),
					DstPort:    uint16(m.DstPort),
					EnvoyPort:  uint16(m.EnvoyPort),
				}
				assert.Contains(t, got, want,
					"TCPMappings produced a mapping with no matching BPF route — Envoy listener would be orphaned")
			}
		})
	}
}

// TestEgressRulesFileFields_AllFieldsHaveDescriptions guards the storage
// schema contract: every YAML field on EgressRulesFile must carry a desc tag
// so the storeui TUI can display meaningful help text.
func TestEgressRulesFileFields_AllFieldsHaveDescriptions(t *testing.T) {
	fs := firewall.EgressRulesFile{}.Fields()
	for _, f := range fs.All() {
		assert.NotEmptyf(t, f.Description(), "field %q has no desc tag", f.Path())
	}
}

// TestMergeRule_CallerWinsScalars asserts that on a same-RuleKey merge,
// the caller's Action and PathDefault overwrite the existing values.
func TestMergeRule_CallerWinsScalars(t *testing.T) {
	t.Parallel()
	existing := config.EgressRule{
		Dst: "api.example.com", Proto: "tls", Port: 443,
		Action:      "allow",
		PathDefault: "allow",
	}
	incoming := config.EgressRule{
		Dst: "api.example.com", Proto: "tls", Port: 443,
		Action:      "deny",
		PathDefault: "deny",
	}
	got := firewall.MergeRule(existing, incoming)
	assert.Equal(t, "deny", got.Action)
	assert.Equal(t, "deny", got.PathDefault)
}

// TestMergeRule_PathRulesUnionByPath asserts existing + incoming PathRules
// are unioned by Path, with existing-side order preserved and incoming-only
// entries appended.
func TestMergeRule_PathRulesUnionByPath(t *testing.T) {
	t.Parallel()
	existing := config.EgressRule{
		Dst: "api.example.com", Proto: "tls", Port: 443,
		PathRules: []config.PathRule{
			{Path: "/v1", Action: "allow"},
		},
	}
	incoming := config.EgressRule{
		Dst: "api.example.com", Proto: "tls", Port: 443,
		PathRules: []config.PathRule{
			{Path: "/v2", Action: "deny"},
		},
	}
	got := firewall.MergeRule(existing, incoming)
	require.Len(t, got.PathRules, 2)
	assert.Equal(t, "/v1", got.PathRules[0].Path)
	assert.Equal(t, "allow", got.PathRules[0].Action)
	assert.Equal(t, "/v2", got.PathRules[1].Path)
	assert.Equal(t, "deny", got.PathRules[1].Action)
}

// TestMergeRule_PathRulesSamePathCallerWins asserts that on a same-Path
// collision inside PathRules, the caller's PathRule overwrites in place
// rather than appending a duplicate.
func TestMergeRule_PathRulesSamePathCallerWins(t *testing.T) {
	t.Parallel()
	existing := config.EgressRule{
		Dst: "api.example.com", Proto: "tls", Port: 443,
		PathRules: []config.PathRule{
			{Path: "/v1", Action: "allow"},
			{Path: "/v2", Action: "allow"},
		},
	}
	incoming := config.EgressRule{
		Dst: "api.example.com", Proto: "tls", Port: 443,
		PathRules: []config.PathRule{
			{Path: "/v1", Action: "deny"},
		},
	}
	got := firewall.MergeRule(existing, incoming)
	require.Len(t, got.PathRules, 2)
	assert.Equal(t, "/v1", got.PathRules[0].Path)
	assert.Equal(t, "deny", got.PathRules[0].Action, "caller's action wins on path collision")
	assert.Equal(t, "/v2", got.PathRules[1].Path)
	assert.Equal(t, "allow", got.PathRules[1].Action)
}

// TestMergeRule_EmptyIncomingPathRules_PreservesExisting asserts the
// mergePathRules len(incoming)==0 short-circuit: an incoming rule with no
// PathRules must NOT wipe the existing rule's PathRules, but scalars on
// the incoming rule (Action, PathDefault) still win per the caller-wins
// rule.
func TestMergeRule_EmptyIncomingPathRules_PreservesExisting(t *testing.T) {
	t.Parallel()
	existing := config.EgressRule{
		Dst: "api.example.com", Proto: "tls", Port: 443,
		Action:      "allow",
		PathDefault: "deny",
		PathRules: []config.PathRule{
			{Path: "/v1", Action: "allow"},
		},
	}
	incoming := config.EgressRule{
		Dst: "api.example.com", Proto: "tls", Port: 443,
		Action:      "deny",
		PathDefault: "allow",
		// No PathRules — represents e.g. a bare `clawker firewall add`.
	}
	got := firewall.MergeRule(existing, incoming)
	assert.Equal(t, "deny", got.Action, "caller wins on Action")
	assert.Equal(t, "allow", got.PathDefault, "caller wins on PathDefault")
	require.Len(t, got.PathRules, 1, "existing PathRules preserved when incoming is empty")
	assert.Equal(t, "/v1", got.PathRules[0].Path)
}

// TestMergeRule_EmptyIncomingPathDefault_PreservesExisting locks in the
// invariant that a bare CLI add (no --path, no --path-default) must NOT
// clear a yaml-set PathDefault on the same RuleKey. The string field can't
// distinguish "unset" from "explicitly empty" so empty incoming defers to
// existing.
func TestMergeRule_EmptyIncomingPathDefault_PreservesExisting(t *testing.T) {
	t.Parallel()
	existing := config.EgressRule{
		Dst: "api.example.com", Proto: "tls", Port: 443,
		Action:      "allow",
		PathDefault: "deny",
	}
	incoming := config.EgressRule{
		Dst: "api.example.com", Proto: "tls", Port: 443,
		Action: "allow",
		// PathDefault unset — bare CLI add has nothing to say.
	}
	got := firewall.MergeRule(existing, incoming)
	assert.Equal(t, "deny", got.PathDefault, "empty incoming PathDefault does not clobber existing")
}
