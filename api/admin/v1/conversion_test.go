package v1

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/schmitthub/clawker/internal/config"
)

// TestProtoRulesRoundTrip pins representative rule shapes (path rules, port
// ranges, mixed protos) through the proto round-trip. Field-completeness is
// NOT this test's guarantee — its hand-enumerated assertions can only see the
// fields they name (the InsecureSkipTLSVerify drop lived through it).
// Completeness is TestEgressRuleConversion_NoFieldDrift's job.
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

// fillEveryField recursively sets every settable exported field of v to a
// distinct non-zero value, so a field the conversion drops cannot hide
// behind a zero value. It fails the test on any field kind it does not know
// how to fill — a new field of a new kind must extend the filler, never be
// silently skipped (zero drift tolerance: an unfilled field is an untested
// field).
func fillEveryField(t *testing.T, v reflect.Value, seed *int) {
	t.Helper()
	//nolint:exhaustive // default fails the test loudly — that IS the handling for every other kind
	switch v.Kind() {
	case reflect.String:
		*seed++
		v.SetString(fmt.Sprintf("filled-%d", *seed))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Slice:
		elem := reflect.New(v.Type().Elem()).Elem()
		fillEveryField(t, elem, seed)
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), elem))
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillEveryField(t, v.Elem(), seed)
	case reflect.Struct:
		for i := range v.NumField() {
			if !v.Type().Field(i).IsExported() {
				continue // protobuf runtime internals (state, sizeCache, ...)
			}
			fillEveryField(t, v.Field(i), seed)
		}
	default:
		t.Fatalf("fillEveryField: unsupported kind %s at %s — extend the filler so the new field is exercised",
			v.Kind(), v.Type())
	}
}

// TestEgressRuleConversion_NoFieldDrift is the drift guard for the
// config.EgressRule ↔ EgressRule wire boundary. Every field on either side
// is reflection-filled with a non-zero value and must survive the full
// round trip: a field added to one type but not propagated through
// EgressRuleToProto/EgressRuleFromProto (or absent from the wire message
// entirely) comes back zeroed and fails equality here. This is the
// Kubernetes apitesting/roundtrip approach sized for one type — rules a
// user writes in config MUST reach the store byte-identical; silent drops
// at this boundary are unenforced security config.
func TestEgressRuleConversion_NoFieldDrift(t *testing.T) {
	t.Run("config to proto and back", func(t *testing.T) {
		var rule config.EgressRule
		seed := 0
		fillEveryField(t, reflect.ValueOf(&rule).Elem(), &seed)

		got := EgressRuleFromProto(EgressRuleToProto(rule))
		require.Equal(t, rule, got,
			"a config.EgressRule field did not survive the wire round trip — update conversion.go AND admin.proto")
	})

	t.Run("proto to config and back", func(t *testing.T) {
		wire := &EgressRule{} //nolint:exhaustruct // the very next line reflection-fills every exported field
		seed := 0
		fillEveryField(t, reflect.ValueOf(wire).Elem(), &seed)

		got := EgressRuleToProto(EgressRuleFromProto(wire))
		require.True(t, proto.Equal(wire, got),
			"a wire EgressRule field did not survive the config round trip — update conversion.go\nwant: %v\ngot:  %v",
			wire, got)
	})
}
