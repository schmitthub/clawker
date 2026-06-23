package hostproxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRulesFile = "testdata/egress-rules.yaml"

func TestCheckURLAgainstEgressRules(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		allowed bool
	}{
		// --- Exact domain matches (TLS) ---
		{name: "github https allowed", url: "https://github.test/schmitthub/clawker", allowed: true},
		{name: "github https with path", url: "https://github.test/foo/bar/pulls", allowed: true},
		{name: "api.github.test allowed", url: "https://api.github.test/repos/foo/bar", allowed: true},
		{name: "anthropic api allowed", url: "https://api.anthropic.test/v1/messages", allowed: true},
		{name: "proxy.golang.test allowed", url: "https://proxy.golang.test/github.test/foo/@v/list", allowed: true},
		{name: "docs.clawker.test allowed", url: "https://docs.clawker.test/quickstart", allowed: true},

		// --- Wildcard domain matches ---
		{name: "wildcard subdomain", url: "https://api.claude.test/v1/messages", allowed: true},
		{name: "wildcard bare domain", url: "https://claude.test/", allowed: true},
		{name: "wildcard deep subdomain", url: "https://us-east.api.claude.test/chat", allowed: true},
		{name: "wildcard no match suffix", url: "https://notclaude.test/", allowed: false},
		{name: "wildcard no match embedded", url: "https://claude.test.evil.test/", allowed: false},

		// --- Explicit deny ---
		{name: "denied domain", url: "https://evil.test/exfil?data=stolen", allowed: false},

		// --- Exfil scenarios (must be blocked) ---
		{name: "unknown domain blocked", url: "https://attacker.test/c/16?c=secrets", allowed: false},
		{name: "localhost https blocked", url: "https://localhost:8443/c/01", allowed: false},
		{name: "localhost http blocked", url: "http://localhost:8080/c/01", allowed: false},
		{name: "random domain blocked", url: "https://random-exfil-server.test/", allowed: false},

		// --- HTTP with path rules ---
		{name: "http path allowed", url: "http://api.example.test/v1/messages", allowed: true},
		{name: "http path denied admin", url: "http://api.example.test/v1/admin/users", allowed: false},
		{name: "http path health", url: "http://api.example.test/health", allowed: true},
		{name: "http path healthcheck subpath", url: "http://api.example.test/healthcheck", allowed: true},
		{name: "http path default deny", url: "http://api.example.test/secret/data", allowed: false},
		{name: "http path root denied", url: "http://api.example.test/", allowed: false},

		// --- HTTP without path rules ---
		{name: "http cdn any path", url: "http://cdn.example.test/assets/img.png", allowed: true},

		// --- Regex (~) path rules: must match Envoy safe_regex exactly ---
		{name: "regex allow exact user", url: "http://allowlist.example.test/users/clawker", allowed: true},
		{name: "regex allow user trailing slash", url: "http://allowlist.example.test/users/anthropic/", allowed: true},
		{name: "regex allow blocks prefix bypass", url: "http://allowlist.example.test/users/clawker-evil", allowed: false},
		{name: "regex allow blocks other path", url: "http://allowlist.example.test/repos/clawker", allowed: false},
		{name: "regex deny blocks subtree", url: "http://denylist.example.test/admin", allowed: false},
		{name: "regex deny blocks subtree child", url: "http://denylist.example.test/admin/users", allowed: false},
		{name: "regex deny allows other path", url: "http://denylist.example.test/public", allowed: true},

		// --- Percent-encoding cannot evade a deny. The host browser + origin
		//     decode the path, so the host proxy decodes too (net/url) and the
		//     denied resource is what gets matched. Keeping %xx literal here —
		//     as Envoy's normalize_path does — would instead let "/%61dmin"
		//     miss the deny and fall through to default-allow, a browser-channel
		//     bypass. Decoding is the fail-closed direction for this channel. ---
		{name: "regex deny blocks percent-encoded admin", url: "http://denylist.example.test/%61dmin", allowed: false},
		{name: "regex deny blocks encoded slash child", url: "http://denylist.example.test/admin%2fusers", allowed: false},
		{name: "regex allow honors percent-encoded user", url: "http://allowlist.example.test/users/%63lawker", allowed: true},

		// --- Dot-segment directory normalization vs an end-anchored allow.
		//     "/blog/." and "/blog/x/.." resolve to the directory "/blog/"
		//     (RFC 3986 remove_dot_segments / Envoy normalize_path), which
		//     "~/blog$" does NOT match. The host proxy must DENY them, matching
		//     Envoy — else the host browser fetches a denied directory. ---
		{name: "anchored exact blog allowed", url: "http://anchored.example.test/blog", allowed: true},
		{name: "anchored blog dir denied", url: "http://anchored.example.test/blog/", allowed: false},
		{name: "anchored blog dot-segment dir denied", url: "http://anchored.example.test/blog/.", allowed: false},
		{name: "anchored blog dotdot dir denied", url: "http://anchored.example.test/blog/x/..", allowed: false},
		{name: "anchored blog parent denied", url: "http://anchored.example.test/blog/..", allowed: false},

		// --- IP address rules ---
		{name: "ip exact match", url: "https://93.184.216.34/resource", allowed: true},
		{name: "ip wrong address", url: "https://93.184.216.35/resource", allowed: false},
		{name: "cidr match", url: "https://10.1.2.3/internal", allowed: true},
		{name: "cidr match edge", url: "https://10.255.255.255/internal", allowed: true},
		{name: "cidr no match", url: "https://11.0.0.1/internal", allowed: false},

		// --- Non-standard port ---
		{name: "custom port allowed", url: "https://registry.internal:8443/v2/images", allowed: true},
		{name: "custom port wrong port", url: "https://registry.internal:443/v2/images", allowed: false},
		{name: "custom port default 443", url: "https://registry.internal/v2/images", allowed: false},

		// --- Same domain, different proto/port are separate rules ---
		{name: "github tls 443 allowed", url: "https://github.test/repo", allowed: true},
		{name: "github port 22 not matched by tls 443 rule", url: "https://github.test:22/repo", allowed: false},
		{name: "github http 80 not allowed", url: "http://github.test/foo", allowed: false},
		{name: "api.example.test http 80 allowed", url: "http://api.example.test/v1/ok", allowed: true},
		{name: "api.example.test https 443 not allowed", url: "https://api.example.test/v1/ok", allowed: false},

		// --- Unsupported schemes ---
		{name: "ftp rejected", url: "ftp://github.test/file", allowed: false},
		{name: "javascript rejected", url: "javascript:alert(1)", allowed: false},

		// --- Malformed URLs ---
		{name: "userinfo rejected", url: "https://user:pass@github.test/", allowed: false},
		{name: "opaque rejected", url: "mailto:user@example.test", allowed: false},
		{name: "no host rejected", url: "https:///path", allowed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckURLAgainstEgressRules(tt.url, testRulesFile)
			if tt.allowed && err != nil {
				t.Errorf("expected URL to be allowed, got error: %v", err)
			}
			if !tt.allowed && err == nil {
				t.Errorf("expected URL to be blocked, got nil")
			}
		})
	}
}

func TestCheckURLAgainstEgressRules_MissingFile(t *testing.T) {
	err := CheckURLAgainstEgressRules("https://github.test/", "/nonexistent/egress-rules.yaml")
	if err == nil {
		t.Fatal("expected error for missing rules file, got nil")
	}
}

func TestCheckURLAgainstEgressRules_EmptyRulesFile(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "empty.yaml")
	if err := os.WriteFile(f, []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := CheckURLAgainstEgressRules("https://github.test/", f)
	if err == nil {
		t.Fatal("expected block with empty rules, got nil")
	}
}

func TestMatchRules_ExactDenyBeatsWildcardAllow(t *testing.T) {
	// A wildcard allow for .example.test must NOT shadow an exact deny for example.test.
	// This is the critical case: exact rules always take priority regardless of order.
	rules := []egressRule{
		{Dst: ".example.test", Proto: "https", Port: "443", Action: "allow"},
		{Dst: "example.test", Proto: "https", Port: "443", Action: "deny"},
	}

	// Apex must be denied (exact deny wins over wildcard allow).
	err := matchRules(rules, "example.test", "https", 443, "/")
	if err == nil {
		t.Fatal("expected apex example.test to be denied, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected deny error, got: %v", err)
	}

	// Subdomain must still be allowed (wildcard covers subdomains).
	err = matchRules(rules, "sub.example.test", "https", 443, "/")
	if err != nil {
		t.Fatalf("expected subdomain to be allowed, got: %v", err)
	}
}

func TestMatchRules_ExactAllowBeatsWildcardDeny(t *testing.T) {
	// Reverse case: wildcard deny + exact allow. Exact allow must win for apex.
	rules := []egressRule{
		{Dst: ".example.test", Proto: "https", Port: "443", Action: "deny"},
		{Dst: "example.test", Proto: "https", Port: "443", Action: "allow"},
	}

	// Apex must be allowed (exact allow wins).
	err := matchRules(rules, "example.test", "https", 443, "/")
	if err != nil {
		t.Fatalf("expected apex to be allowed, got: %v", err)
	}

	// Subdomain must be denied (wildcard deny covers subdomains).
	err = matchRules(rules, "sub.example.test", "https", 443, "/")
	if err == nil {
		t.Fatal("expected subdomain to be denied, got nil")
	}
}

func TestCheckURLAgainstEgressRules_MalformedYAML(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(f, []byte("{{not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := CheckURLAgainstEgressRules("https://github.test/", f)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestDstMatchType(t *testing.T) {
	tests := []struct {
		dst, host string
		want      matchKind
	}{
		// Domain passthrough
		{"github.test", "github.test", matchExact},
		{".claude.test", "api.claude.test", matchWildcard},
		// IP exact match
		{"192.168.1.1", "192.168.1.1", matchExact},
		{"192.168.1.1", "192.168.1.2", matchNone},
		// IPv6
		{"::1", "::1", matchExact},
		{"::1", "::2", matchNone},
		// CIDR containment
		{"10.0.0.0/8", "10.1.2.3", matchExact},
		{"10.0.0.0/8", "11.0.0.1", matchNone},
		{"192.168.0.0/16", "192.168.1.1", matchExact},
		{"192.168.0.0/16", "192.169.0.1", matchNone},
		// IP dst vs domain host (no match)
		{"192.168.1.1", "example.test", matchNone},
		// Domain dst vs IP host (no match)
		{"example.test", "192.168.1.1", matchNone},
	}

	for _, tt := range tests {
		t.Run(tt.dst+"_vs_"+tt.host, func(t *testing.T) {
			got := dstMatchType(tt.dst, tt.host)
			if got != tt.want {
				t.Errorf("dstMatchType(%q, %q) = %v, want %v", tt.dst, tt.host, got, tt.want)
			}
		})
	}
}

func TestDomainMatchType(t *testing.T) {
	tests := []struct {
		dst, host string
		want      matchKind
	}{
		{"github.test", "github.test", matchExact},
		{"github.test", "GitHub.TEST", matchExact},
		{"github.test", "api.github.test", matchNone},
		{".claude.test", "claude.test", matchWildcard},
		{".claude.test", "api.claude.test", matchWildcard},
		{".claude.test", "deep.sub.claude.test", matchWildcard},
		{".claude.test", "notclaude.test", matchNone},
		{".claude.test", "claude.test.evil.test", matchNone},
		{"example.test.", "example.test", matchExact}, // trailing dot FQDN
		{"example.test", "example.test.", matchExact}, // trailing dot on host
		{".example.test.", "sub.example.test", matchWildcard},
	}

	for _, tt := range tests {
		t.Run(tt.dst+"_vs_"+tt.host, func(t *testing.T) {
			got := domainMatchType(tt.dst, tt.host)
			if got != tt.want {
				t.Errorf("domainMatchType(%q, %q) = %v, want %v", tt.dst, tt.host, got, tt.want)
			}
		})
	}
}

func TestNormalizeEgressRule(t *testing.T) {
	tests := []struct {
		name                  string
		input                 egressRule
		wantProto, wantAction string
		wantPort              string
	}{
		{"empty defaults to https/allow/443", egressRule{Dst: "example.test"}, "https", "allow", "443"},
		{"legacy tls translated to https", egressRule{Dst: "example.test", Proto: "tls"}, "https", "allow", "443"},
		{"tcp proto keeps empty port", egressRule{Dst: "example.test", Proto: "tcp"}, "tcp", "allow", ""},
		{"explicit values preserved", egressRule{Dst: "x.test", Proto: "https", Port: "8443", Action: "deny"}, "https", "deny", "8443"},
		{"https with empty port gets 443", egressRule{Dst: "x.test", Proto: "https"}, "https", "allow", "443"},
		{"http with empty port gets 80", egressRule{Dst: "x.test", Proto: "http"}, "http", "allow", "80"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeEgressRule(tt.input)
			if got.Proto != tt.wantProto || got.Action != tt.wantAction || got.Port != tt.wantPort {
				t.Errorf("normalizeEgressRule() = {Proto:%q Action:%q Port:%q}, want {Proto:%q Action:%q Port:%q}",
					got.Proto, got.Action, got.Port, tt.wantProto, tt.wantAction, tt.wantPort)
			}
		})
	}
}

func TestSchemeToProto(t *testing.T) {
	tests := []struct {
		scheme    string
		wantProto string
		wantPort  int
		wantErr   bool
	}{
		{"https", "https", 443, false},
		{"HTTPS", "https", 443, false},
		{"http", "http", 80, false},
		{"ftp", "", 0, true},
		{"", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.scheme, func(t *testing.T) {
			proto, port, err := schemeToProto(tt.scheme)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if proto != tt.wantProto {
				t.Errorf("proto = %q, want %q", proto, tt.wantProto)
			}
			if port != tt.wantPort {
				t.Errorf("port = %d, want %d", port, tt.wantPort)
			}
		})
	}
}

func TestCheckPathRules(t *testing.T) {
	rules := []pathRule{
		{Path: "/v1/", Action: "allow"},
		{Path: "/v1/admin/", Action: "deny"},
		{Path: "/health", Action: "allow"},
	}

	tests := []struct {
		path        string
		pathDefault string
		allowed     bool
	}{
		{"/v1/messages", "", true},
		{"/v1/admin/users", "", false},
		{"/v1/admin/", "", false},
		{"/health", "", true},
		{"/healthcheck", "", true}, // prefix match
		{"/v2/stuff", "", false},   // no match, default deny (empty = deny)
		{"/v2/stuff", "allow", true},
		{"/v2/stuff", "deny", false},
		{"/", "", false},
		{"/", "allow", true},
	}

	for _, tt := range tests {
		t.Run(tt.path+"_default_"+tt.pathDefault, func(t *testing.T) {
			err := checkPathRules(rules, tt.pathDefault, "host", tt.path)
			if tt.allowed && err != nil {
				t.Errorf("expected path allowed, got: %v", err)
			}
			if !tt.allowed && err == nil {
				t.Error("expected path blocked, got nil")
			}
		})
	}
}

// TestCheckPathRules_DenylistInference pins the parity with
// adminv1.EffectivePathDefault: a rule set where every PathRule is deny and
// path_default is empty resolves the catch-all to "allow" (denylist mode), so
// unmatched paths must pass on both Envoy and the host proxy.
func TestCheckPathRules_DenylistInference(t *testing.T) {
	denyOnly := []pathRule{
		{Path: "/admin", Action: "deny"},
		{Path: "/private", Action: "deny"},
	}

	tests := []struct {
		name        string
		rules       []pathRule
		pathDefault string
		urlPath     string
		allowed     bool
	}{
		{"denylist_unmatched_allowed", denyOnly, "", "/public", true},
		{"denylist_matched_denied", denyOnly, "", "/admin/users", false},
		{"denylist_explicit_default_deny_wins", denyOnly, "deny", "/public", false},
		{"empty_rules_no_default_allows", nil, "", "/anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkPathRules(tt.rules, tt.pathDefault, "host", tt.urlPath)
			if tt.allowed && err != nil {
				t.Errorf("expected path allowed, got: %v", err)
			}
			if !tt.allowed && err == nil {
				t.Error("expected path blocked, got nil")
			}
		})
	}
}

// TestPortSpecMatches pins the port matcher in lockstep with
// config.ParsePortSpec. The DENY direction is the security-critical one: a
// spec the firewall validated and wrote must parse here too, else a deny rule
// silently fails to match and falls through to a wildcard allow.
func TestPortSpecMatches(t *testing.T) {
	tests := []struct {
		name string
		spec string
		port int
		want bool
	}{
		{"single exact", "443", 443, true},
		{"single mismatch", "443", 8443, false},
		{"range low edge", "9000-9100", 9000, true},
		{"range high edge", "9000-9100", 9100, true},
		{"range inside", "9000-9100", 9050, true},
		{"range below", "9000-9100", 8999, false},
		{"range above", "9000-9100", 9101, false},
		// Whitespace: config.ParsePortSpec trims, so this matcher must too —
		// otherwise a trimmed-and-valid deny spec is skipped here (the hole).
		{"leading space single", " 443", 443, true},
		{"trailing space single", "443 ", 443, true},
		{"spaces around range", " 9000 - 9100 ", 9050, true},
		// Malformed / out-of-range: rejected upstream by ParsePortSpec, so they
		// should never appear in the file — but if they do, match nothing.
		{"empty matches nothing", "", 443, false},
		{"non-numeric", "https", 443, false},
		{"zero out of range", "0", 0, false},
		{"over 65535", "70000", 70000, false},
		{"reversed range", "9100-9000", 9050, false},
		{"range bad low", "abc-9100", 9050, false},
		{"range bad high", "9000-abc", 9050, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := portSpecMatches(tt.spec, tt.port); got != tt.want {
				t.Errorf("portSpecMatches(%q, %d) = %v, want %v", tt.spec, tt.port, got, tt.want)
			}
		})
	}
}

// TestCheckURLAgainstEgressRules_PathTraversal is the path-canonicalization
// fuzz table. The host proxy opens the RAW url string in the host browser,
// which normalizes the path per the WHATWG URL spec (backslash->slash for
// http/https, dot-segment popping) and the origin server then resolves the
// rest per RFC 3986 §5.2.4. So the path the matcher evaluates MUST equal the
// post-normalization path the server actually serves, or an attacker prefixes
// an allowed path and escapes to a denied one — defeating path_default:deny.
//
// The fixture `api.example.test` (http/80) is path_default:deny with allow
// /v1/, allow /health, deny /v1/admin/. Denied targets used below: /secret
// (default deny) and /v1/admin/x (explicit). Every evasion encoding that
// resolves to a denied (or non-allowlisted) target MUST block; every one that
// resolves to an allowed target MUST pass. net/url percent-decodes (%2e->'.',
// %2f->'/') before canonicalizePath runs, so encoded and literal variants
// collapse together; backslashes are folded to '/' to match browser behavior.
func TestCheckURLAgainstEgressRules_PathTraversal(t *testing.T) {
	const host = "http://api.example.test"
	tests := []struct {
		name    string
		path    string
		allowed bool
	}{
		// --- canonical baselines (behavior must be preserved) ---
		{"allowed v1 path", "/v1/things", true},
		{"allowed v1 deep", "/v1/a/b/c", true},
		{"allowed health", "/health", true},
		{"allowed v1 dir trailing slash", "/v1/", true},
		{"denied default root", "/secret", false},
		{"denied bare v1 no slash", "/v1", false},
		{"denied explicit admin", "/v1/admin/x", false},
		{"denied root", "/", false},

		// --- literal dot-segment traversal out of /v1/ to /secret ---
		{"dotdot single", "/v1/../secret", false},
		{"dotdot double", "/v1/../../secret", false},
		{"dotdot overshoot", "/v1/../../../../../../secret", false},
		{"dotdot trailing", "/v1/..", false},
		{"dot then dotdot", "/v1/./../secret", false},
		{"dotdot into explicit deny", "/v1/../v1/admin/x", false},
		{"admin out then to secret", "/v1/admin/../../secret", false},

		// --- percent-encoded dot evasions ---
		{"enc dotdot lower", "/v1/%2e%2e/secret", false},
		{"enc dotdot upper", "/v1/%2E%2E/secret", false},
		{"enc dot mixed a", "/v1/.%2e/secret", false},
		{"enc dot mixed b", "/v1/%2e./secret", false},
		{"enc full dotdotslash", "/v1/%2e%2e%2fsecret", false},

		// --- percent-encoded slash evasions ---
		{"enc slash lower", "/v1%2f..%2fsecret", false},
		{"enc slash upper", "/v1%2F..%2Fsecret", false},
		{"enc slash leading dotdot", "/v1/..%2fsecret", false},

		// --- backslash evasions (browser folds \ -> / for http/https) ---
		{"backslash dotdot", "/v1\\..\\secret", false},
		{"backslash mid segment", "/v1/..\\secret", false},
		{"backslash double", "/v1\\..\\..\\secret", false},

		// --- noise that must canonicalize, not bypass or mis-block ---
		{"single dot segments allowed", "/v1/./things", true},
		{"double slash allowed", "//v1//things", true},
		{"backslash separators allowed", "/v1\\things", true},

		// --- traversal that legitimately nets back to an allowed path ---
		{"dotdot net-allowed", "/v1/admin/../things", true},
		{"dotdot churn net-allowed", "/v1/a/../b/../things", true},

		// --- dot-segment that resolves to the allowed directory "/v1/" must
		//     stay allowed: RFC 3986 keeps the trailing slash ("/v1/." and
		//     "/v1/x/.." -> "/v1/"), so the canonical path still matches the
		//     "/v1/" prefix rule. path.Clean alone drops the slash, which would
		//     mis-block these as "/v1" (the bare, denied path). ---
		{"dot-segment to dir allowed", "/v1/.", true},
		{"dotdot back to dir allowed", "/v1/x/..", true},

		// --- double-encoding: stays literal on a single-decode server, so it
		// remains under /v1/ and never reaches /secret. Documents the boundary:
		// only a (broken) double-decoding origin would traverse, out of scope. ---
		{"double-encoded stays literal", "/v1/%252e%252e/x", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckURLAgainstEgressRules(host+tt.path, testRulesFile)
			if tt.allowed && err != nil {
				t.Errorf("path %q: expected ALLOWED, got blocked: %v", tt.path, err)
			}
			if !tt.allowed && err == nil {
				t.Errorf("path %q: expected BLOCKED, got allowed (EGRESS BYPASS)", tt.path)
			}
		})
	}
}

// TestMatchRules_DenyWins pins deny-always-wins + port-span semantics, the
// host-proxy counterpart to the firewall generator's
// TestNormalizeAndDedup_ResolvesOpaquePortConflicts. The firewall side carves
// overlapping rules into disjoint spans at store-write time; the host proxy
// reads the same file but matches directly, so for any (rules, port) it MUST
// return the verdict the carved rule set would enforce. The reachable hazard:
// a range allow ("9000-9100") and a single-port deny ("9050") live under
// different dst:proto:port keys, so both survive on disk and both match port
// 9050. First-match-wins (the old behavior) let an earlier allow shadow the
// deny — an egress bypass relative to Envoy. A deny only wins when its own
// port spec covers the request port.
//
// Each case fixes a rule set and asserts the verdict at several probe ports,
// mirroring the carve scenarios as per-port verdicts.
func TestMatchRules_DenyWins(t *testing.T) {
	const dst = "x.test" // exact host; wildcard cases use ".x.test" + sub host

	tests := []struct {
		name  string
		rules []egressRule
		host  string
		// port -> expected allowed
		probes map[int]bool
	}{
		{
			name: "allow range + deny single inside (deny wins at that port only)",
			rules: []egressRule{
				{Dst: dst, Proto: "https", Port: "9000-9100", Action: "allow"},
				{Dst: dst, Proto: "https", Port: "9050", Action: "deny"},
			},
			host:   dst,
			probes: map[int]bool{9000: true, 9049: true, 9050: false, 9051: true, 9100: true, 9101: false},
		},
		{
			name: "deny single + allow range (reverse order, order-independent)",
			rules: []egressRule{
				{Dst: dst, Proto: "https", Port: "9050", Action: "deny"},
				{Dst: dst, Proto: "https", Port: "9000-9100", Action: "allow"},
			},
			host:   dst,
			probes: map[int]bool{9050: false, 9070: true},
		},
		{
			name: "deny single in the middle splits the allow span",
			rules: []egressRule{
				{Dst: dst, Proto: "https", Port: "4242-4250", Action: "allow"},
				{Dst: dst, Proto: "https", Port: "4246", Action: "deny"},
			},
			host:   dst,
			probes: map[int]bool{4245: true, 4246: false, 4247: true},
		},
		{
			name: "deny range swallows allow single inside (fully denied)",
			rules: []egressRule{
				{Dst: dst, Proto: "https", Port: "4242-4250", Action: "deny"},
				{Dst: dst, Proto: "https", Port: "4245", Action: "allow"},
			},
			host:   dst,
			probes: map[int]bool{4242: false, 4245: false, 4250: false, 4251: false},
		},
		{
			name: "two overlapping deny+allow ranges (deny wins across the overlap)",
			rules: []egressRule{
				{Dst: dst, Proto: "https", Port: "4242-4250", Action: "allow"},
				{Dst: dst, Proto: "https", Port: "4248-4260", Action: "deny"},
			},
			host:   dst,
			probes: map[int]bool{4247: true, 4248: false, 4250: false, 4260: false, 4261: false},
		},
		{
			name: "disjoint allow ports stay independent",
			rules: []egressRule{
				{Dst: dst, Proto: "https", Port: "4242", Action: "allow"},
				{Dst: dst, Proto: "https", Port: "5000", Action: "allow"},
			},
			host:   dst,
			probes: map[int]bool{4242: true, 4243: false, 5000: true},
		},
		{
			name: "wildcard range allow + single deny overlap (deny wins on subdomain)",
			rules: []egressRule{
				{Dst: ".x.test", Proto: "https", Port: "9000-9100", Action: "allow"},
				{Dst: ".x.test", Proto: "https", Port: "9050", Action: "deny"},
			},
			host:   "sub.x.test",
			probes: map[int]bool{9049: true, 9050: false, 9051: true},
		},
		{
			name: "exact allow beats wildcard deny (specificity tier, not deny-across-tiers)",
			rules: []egressRule{
				{Dst: ".x.test", Proto: "https", Port: "443", Action: "deny"},
				{Dst: "host.x.test", Proto: "https", Port: "443", Action: "allow"},
			},
			host:   "host.x.test",
			probes: map[int]bool{443: true},
		},
		{
			name: "exact deny beats wildcard allow (specificity tier)",
			rules: []egressRule{
				{Dst: ".x.test", Proto: "https", Port: "443", Action: "allow"},
				{Dst: "host.x.test", Proto: "https", Port: "443", Action: "deny"},
			},
			host:   "host.x.test",
			probes: map[int]bool{443: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for port, wantAllow := range tt.probes {
				err := matchRules(tt.rules, tt.host, "https", port, "/")
				if wantAllow && err != nil {
					t.Errorf("port %d: expected ALLOWED, got blocked: %v", port, err)
				}
				if !wantAllow && err == nil {
					t.Errorf("port %d: expected DENIED, got allowed (deny-wins/span violated — egress bypass)", port)
				}
			}
		})
	}
}
