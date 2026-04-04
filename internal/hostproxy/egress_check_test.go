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
		{Dst: ".example.test", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "example.test", Proto: "tls", Port: 443, Action: "deny"},
	}

	// Apex must be denied (exact deny wins over wildcard allow).
	err := matchRules(rules, "example.test", "tls", 443, "/")
	if err == nil {
		t.Fatal("expected apex example.test to be denied, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected deny error, got: %v", err)
	}

	// Subdomain must still be allowed (wildcard covers subdomains).
	err = matchRules(rules, "sub.example.test", "tls", 443, "/")
	if err != nil {
		t.Fatalf("expected subdomain to be allowed, got: %v", err)
	}
}

func TestMatchRules_ExactAllowBeatsWildcardDeny(t *testing.T) {
	// Reverse case: wildcard deny + exact allow. Exact allow must win for apex.
	rules := []egressRule{
		{Dst: ".example.test", Proto: "tls", Port: 443, Action: "deny"},
		{Dst: "example.test", Proto: "tls", Port: 443, Action: "allow"},
	}

	// Apex must be allowed (exact allow wins).
	err := matchRules(rules, "example.test", "tls", 443, "/")
	if err != nil {
		t.Fatalf("expected apex to be allowed, got: %v", err)
	}

	// Subdomain must be denied (wildcard deny covers subdomains).
	err = matchRules(rules, "sub.example.test", "tls", 443, "/")
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
		wantPort              int
	}{
		{"empty defaults to tls/allow/443", egressRule{Dst: "example.test"}, "tls", "allow", 443},
		{"http proto keeps port 0", egressRule{Dst: "example.test", Proto: "http"}, "http", "allow", 0},
		{"explicit values preserved", egressRule{Dst: "x.test", Proto: "tls", Port: 8443, Action: "deny"}, "tls", "deny", 8443},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeEgressRule(tt.input)
			if got.Proto != tt.wantProto || got.Action != tt.wantAction || got.Port != tt.wantPort {
				t.Errorf("normalizeEgressRule() = {Proto:%q Action:%q Port:%d}, want {Proto:%q Action:%q Port:%d}",
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
		{"https", "tls", 443, false},
		{"HTTPS", "tls", 443, false},
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
