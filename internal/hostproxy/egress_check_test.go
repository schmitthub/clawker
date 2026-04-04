package hostproxy

import (
	"os"
	"path/filepath"
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
		{name: "github https allowed", url: "https://github.com/schmitthub/clawker", allowed: true},
		{name: "github https with path", url: "https://github.com/foo/bar/pulls", allowed: true},
		{name: "api.github.com allowed", url: "https://api.github.com/repos/foo/bar", allowed: true},
		{name: "anthropic api allowed", url: "https://api.anthropic.com/v1/messages", allowed: true},
		{name: "proxy.golang.org allowed", url: "https://proxy.golang.org/github.com/foo/@v/list", allowed: true},
		{name: "docs.clawker.dev allowed", url: "https://docs.clawker.dev/quickstart", allowed: true},

		// --- Wildcard domain matches ---
		{name: "wildcard subdomain", url: "https://api.claude.ai/v1/messages", allowed: true},
		{name: "wildcard bare domain", url: "https://claude.ai/", allowed: true},
		{name: "wildcard deep subdomain", url: "https://us-east.api.claude.ai/chat", allowed: true},
		{name: "wildcard no match suffix", url: "https://notclaude.ai/", allowed: false},
		{name: "wildcard no match embedded", url: "https://claude.ai.evil.com/", allowed: false},

		// --- Explicit deny ---
		{name: "denied domain", url: "https://evil.com/exfil?data=stolen", allowed: false},

		// --- Exfil scenarios (must be blocked) ---
		{name: "ngrok exfil blocked", url: "https://abc123.ngrok.app/c/16?c=c2VjcmV0cw==", allowed: false},
		{name: "attacker domain blocked", url: "https://attacker.com/c/16?c=secrets", allowed: false},
		{name: "localhost https blocked", url: "https://localhost:8443/c/01", allowed: false},
		{name: "localhost http blocked", url: "http://localhost:8080/c/01", allowed: false},
		{name: "random domain blocked", url: "https://random-exfil-server.com/", allowed: false},

		// --- HTTP with path rules ---
		{name: "http path allowed", url: "http://api.example.com/v1/messages", allowed: true},
		{name: "http path denied admin", url: "http://api.example.com/v1/admin/users", allowed: false},
		{name: "http path health", url: "http://api.example.com/health", allowed: true},
		{name: "http path healthcheck subpath", url: "http://api.example.com/healthcheck", allowed: true},
		{name: "http path default deny", url: "http://api.example.com/secret/data", allowed: false},
		{name: "http path root denied", url: "http://api.example.com/", allowed: false},

		// --- HTTP without path rules ---
		{name: "http cdn any path", url: "http://cdn.example.com/assets/img.png", allowed: true},

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

		// --- Proto mismatch ---
		{name: "http to tls-only domain", url: "http://github.com/foo", allowed: false},
		{name: "https to http-only domain", url: "https://api.example.com/v1/foo", allowed: false},
		{name: "https to ssh-only entry", url: "https://github.com:22/foo", allowed: false},

		// --- Unsupported schemes ---
		{name: "ftp rejected", url: "ftp://github.com/file", allowed: false},
		{name: "javascript rejected", url: "javascript:alert(1)", allowed: false},

		// --- Malformed URLs ---
		{name: "userinfo rejected", url: "https://user:pass@github.com/", allowed: false},
		{name: "opaque rejected", url: "mailto:user@example.com", allowed: false},
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
	err := CheckURLAgainstEgressRules("https://github.com/", "/nonexistent/egress-rules.yaml")
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

	err := CheckURLAgainstEgressRules("https://github.com/", f)
	if err == nil {
		t.Fatal("expected block with empty rules, got nil")
	}
}

func TestCheckURLAgainstEgressRules_MalformedYAML(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(f, []byte("{{not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := CheckURLAgainstEgressRules("https://github.com/", f)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestDstMatches(t *testing.T) {
	tests := []struct {
		dst, host string
		want      bool
	}{
		// Domain passthrough
		{"github.com", "github.com", true},
		{".claude.ai", "api.claude.ai", true},
		// IP exact match
		{"192.168.1.1", "192.168.1.1", true},
		{"192.168.1.1", "192.168.1.2", false},
		// IPv6
		{"::1", "::1", true},
		{"::1", "::2", false},
		// CIDR containment
		{"10.0.0.0/8", "10.1.2.3", true},
		{"10.0.0.0/8", "11.0.0.1", false},
		{"192.168.0.0/16", "192.168.1.1", true},
		{"192.168.0.0/16", "192.169.0.1", false},
		// IP dst vs domain host (no match)
		{"192.168.1.1", "example.com", false},
		// Domain dst vs IP host (no match)
		{"example.com", "192.168.1.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.dst+"_vs_"+tt.host, func(t *testing.T) {
			got := dstMatches(tt.dst, tt.host)
			if got != tt.want {
				t.Errorf("dstMatches(%q, %q) = %v, want %v", tt.dst, tt.host, got, tt.want)
			}
		})
	}
}

func TestDomainMatches(t *testing.T) {
	tests := []struct {
		dst, host string
		want      bool
	}{
		{"github.com", "github.com", true},
		{"github.com", "GitHub.COM", true},
		{"github.com", "api.github.com", false},
		{".claude.ai", "claude.ai", true},
		{".claude.ai", "api.claude.ai", true},
		{".claude.ai", "deep.sub.claude.ai", true},
		{".claude.ai", "notclaude.ai", false},
		{".claude.ai", "claude.ai.evil.com", false},
		{"example.com.", "example.com", true}, // trailing dot FQDN
		{"example.com", "example.com.", true}, // trailing dot on host
		{".example.com.", "sub.example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.dst+"_vs_"+tt.host, func(t *testing.T) {
			got := domainMatches(tt.dst, tt.host)
			if got != tt.want {
				t.Errorf("domainMatches(%q, %q) = %v, want %v", tt.dst, tt.host, got, tt.want)
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
		{"empty defaults to tls/allow/443", egressRule{Dst: "example.com"}, "tls", "allow", 443},
		{"http proto keeps port 0", egressRule{Dst: "example.com", Proto: "http"}, "http", "allow", 0},
		{"explicit values preserved", egressRule{Dst: "x.com", Proto: "tls", Port: 8443, Action: "deny"}, "tls", "deny", 8443},
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
