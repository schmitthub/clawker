package firewall_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/internal/controlplane/firewall"
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

// TestEgressRulesFileFields_AllFieldsHaveDescriptions guards the storage
// schema contract: every YAML field on EgressRulesFile must carry a desc tag
// so the storeui TUI can display meaningful help text.
func TestEgressRulesFileFields_AllFieldsHaveDescriptions(t *testing.T) {
	fs := firewall.EgressRulesFile{}.Fields()
	for _, f := range fs.All() {
		assert.NotEmptyf(t, f.Description(), "field %q has no desc tag", f.Path())
	}
}
