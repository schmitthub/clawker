package v1

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProtoRulesRoundTrip pins the field map between EgressRule/PathRule and
// config.EgressRule/PathRule via full round-trip equality. A new field on
// either side without a matching translator update loses data here and fails
// the test — not just the subset the test happens to sample.
func TestProtoRulesRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   []*EgressRule
	}{
		{
			name: "tls with path rules",
			in: []*EgressRule{{
				Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
				PathRules: []*PathRule{
					{Path: "/v1", Action: "allow", Methods: []string{"GET", "HEAD"}},
					{Path: "/admin", Action: "deny", Methods: []string{"POST"}},
				},
				PathDefault: "deny",
			}},
		},
		{
			name: "wildcard dst, no path rules",
			in: []*EgressRule{{
				Dst: "*.github.com", Proto: "https", Port: "443", Action: "allow",
			}},
		},
		{
			name: "http proto, path default only",
			in: []*EgressRule{{
				Dst: "plain.example.com", Proto: "http", Port: "80", Action: "allow",
				PathDefault: "deny",
			}},
		},
		{
			name: "multiple rules, mixed protos",
			in: []*EgressRule{
				{Dst: "a.example.com", Proto: "https", Port: "443", Action: "allow"},
				{Dst: "b.example.com", Proto: "ssh", Port: "22", Action: "allow"},
				{Dst: "c.example.com", Proto: "http", Port: "80", Action: "deny"},
			},
		},
		{
			// Regression: a port range MUST survive the proto round-trip. The
			// earlier split-field design (uint32 port + a config-only PortRange
			// string with no proto field) silently dropped the range here, so
			// every port_range rule collapsed to the default port on the live CP
			// path while golden/direct-gen tests — which bypass this boundary —
			// stayed green. The dynamic string `port` closes that gap.
			name: "opaque tcp port range survives round-trip",
			in: []*EgressRule{
				{Dst: "cluster.example.com", Proto: "tcp", Port: "9000-9002", Action: "allow"},
				{Dst: "198.51.100.9", Proto: "tcp", Port: "9100-9101", Action: "allow"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := EgressRulesToProto(EgressRulesFromProto(tc.in))
			require.Equal(t, len(tc.in), len(out), "rule count preserved")
			for i, want := range tc.in {
				got := out[i]
				assert.Equal(t, want.GetDst(), got.GetDst(), "Dst")
				assert.Equal(t, want.GetProto(), got.GetProto(), "Proto")
				assert.Equal(t, want.GetPort(), got.GetPort(), "Port")
				assert.Equal(t, want.GetAction(), got.GetAction(), "Action")
				assert.Equal(t, want.GetPathDefault(), got.GetPathDefault(), "PathDefault")
				require.Equal(t, len(want.GetPathRules()), len(got.GetPathRules()), "PathRules len")
				for j, wp := range want.GetPathRules() {
					gp := got.GetPathRules()[j]
					assert.Equal(t, wp.GetPath(), gp.GetPath(), "PathRules[%d].Path", j)
					assert.Equal(t, wp.GetAction(), gp.GetAction(), "PathRules[%d].Action", j)
					assert.Equal(t, wp.GetMethods(), gp.GetMethods(), "PathRules[%d].Methods", j)
				}
			}
		})
	}
}

// TestEffectivePathDefault_Inference covers the truth table that drives the
// catch-all action when no explicit r.PathDefault is set. The inference exists
// so `firewall add foo.com --path /x --action deny` gives users denylist
// semantics without forcing them to learn about path_default.
func TestEffectivePathDefault_Inference(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		rule config.EgressRule
		want string
	}{
		{
			name: "explicit override wins over inference",
			rule: config.EgressRule{
				PathDefault: "allow",
				PathRules:   []config.PathRule{{Path: "/x", Action: "allow"}},
			},
			want: "allow",
		},
		{
			name: "no path rules → allow (vacuous; callers gate on len(PathRules)>0)",
			rule: config.EgressRule{Action: "allow"},
			want: "allow",
		},
		{
			name: "only deny path rules → allow (denylist mode)",
			rule: config.EgressRule{
				PathRules: []config.PathRule{
					{Path: "/admin", Action: "deny"},
					{Path: "/internal", Action: "deny"},
				},
			},
			want: "allow",
		},
		{
			name: "only allow path rules → deny (allowlist mode)",
			rule: config.EgressRule{
				PathRules: []config.PathRule{{Path: "/v1", Action: "allow"}},
			},
			want: "deny",
		},
		{
			name: "mixed allow + deny → deny (any allow ⇒ allowlist semantics)",
			rule: config.EgressRule{
				PathRules: []config.PathRule{
					{Path: "/v1", Action: "allow"},
					{Path: "/admin", Action: "deny"},
				},
			},
			want: "deny",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, EffectivePathDefault(tt.rule))
		})
	}
}
