package bundler

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
)

// TestFirewallDomainsDeterministicOrder verifies that GetFirewallDomains returns domains in sorted order.
func TestFirewallDomainsDeterministicOrder(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.FirewallConfig
		defaults []string
		want     []string
	}{
		{
			name:     "nil config returns defaults unchanged",
			config:   nil,
			defaults: []string{"zebra.com", "apple.com", "mango.com"},
			want:     []string{"zebra.com", "apple.com", "mango.com"}, // unchanged when nil
		},
		{
			name:     "empty config returns sorted defaults via additive mode",
			config:   &config.FirewallConfig{Enable: true},
			defaults: []string{"zebra.com", "apple.com", "mango.com"},
			want:     []string{"apple.com", "mango.com", "zebra.com"}, // sorted
		},
		{
			name:     "add domains returns sorted result",
			config:   &config.FirewallConfig{Enable: true, AddDomains: []string{"banana.com"}},
			defaults: []string{"zebra.com", "apple.com"},
			want:     []string{"apple.com", "banana.com", "zebra.com"}, // sorted with addition
		},
		{
			name: "duplicate domains in add are deduplicated",
			config: &config.FirewallConfig{
				Enable:     true,
				AddDomains: []string{"github.com", "github.com", "api.github.com"},
			},
			defaults: []string{"github.com"},
			want:     []string{"api.github.com", "github.com"}, // deduplicated and sorted
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetFirewallDomains(tt.defaults)

			if len(got) != len(tt.want) {
				t.Errorf("GetFirewallDomains() returned %d items, want %d\ngot: %v\nwant: %v",
					len(got), len(tt.want), got, tt.want)
				return
			}

			for i, domain := range got {
				if domain != tt.want[i] {
					t.Errorf("GetFirewallDomains()[%d] = %q, want %q\nfull result: %v",
						i, domain, tt.want[i], got)
				}
			}
		})
	}
}

// TestFirewallDomainsDeterministicAcrossRuns verifies ordering is consistent across multiple calls.
func TestFirewallDomainsDeterministicAcrossRuns(t *testing.T) {
	cfg := &config.FirewallConfig{
		Enable:     true,
		AddDomains: []string{"delta.com", "alpha.com", "charlie.com", "bravo.com"},
	}
	defaults := []string{"zulu.com", "yankee.com", "xray.com"}

	// Run multiple times to ensure consistency
	var firstResult []string
	for i := 0; i < 10; i++ {
		result := cfg.GetFirewallDomains(defaults)

		if i == 0 {
			firstResult = result
			// Verify it's sorted
			for j := 1; j < len(result); j++ {
				if result[j] < result[j-1] {
					t.Errorf("Result not sorted: %v", result)
					break
				}
			}
		} else {
			// Verify consistent with first result
			if len(result) != len(firstResult) {
				t.Errorf("Iteration %d: length mismatch, got %d want %d", i, len(result), len(firstResult))
				continue
			}
			for j, domain := range result {
				if domain != firstResult[j] {
					t.Errorf("Iteration %d: result[%d] = %q, want %q", i, j, domain, firstResult[j])
				}
			}
		}
	}
}

// TestFirewallScriptSyntax validates that the firewall script has valid bash syntax.
func TestFirewallScriptSyntax(t *testing.T) {
	// Basic validation: check for balanced braces and quotes
	script := FirewallScript

	// Count unescaped quotes
	inSingleQuote := false
	inDoubleQuote := false
	braceDepth := 0

	for i := 0; i < len(script); i++ {
		c := script[i]

		// Skip escaped characters
		if c == '\\' && i+1 < len(script) {
			i++
			continue
		}

		switch c {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case '{':
			if !inSingleQuote && !inDoubleQuote {
				braceDepth++
			}
		case '}':
			if !inSingleQuote && !inDoubleQuote {
				braceDepth--
			}
		}
	}

	if inSingleQuote {
		t.Error("Firewall script has unbalanced single quotes")
	}
	if inDoubleQuote {
		t.Error("Firewall script has unbalanced double quotes")
	}
	if braceDepth != 0 {
		t.Errorf("Firewall script has unbalanced braces: depth=%d", braceDepth)
	}

	// Check for the ipset -exist flag usage (the fix we made)
	if !strings.Contains(script, "ipset add allowed-domains \"$cidr\" -exist") {
		t.Error("Firewall script should use 'ipset add ... -exist' for GitHub ranges")
	}
	if !strings.Contains(script, "ipset add allowed-domains \"$ip\" -exist") {
		t.Error("Firewall script should use 'ipset add ... -exist' for custom domains")
	}
}
