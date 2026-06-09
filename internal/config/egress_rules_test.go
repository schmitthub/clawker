// External test package: configmocks imports config, so exercising the real
// EgressRules() composition through configmocks requires package config_test.
package config_test

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/stretchr/testify/assert"
)

// httpsAllow is the shape every add_domains shorthand entry expands to — the
// documented add_domains expansion contract (https, default port, allow).
func httpsAllow(dst string) config.EgressRule {
	return config.EgressRule{
		Dst:    dst,
		Proto:  config.EgressProtoHTTPS,
		Port:   config.EgressPortHTTPS,
		Action: config.EgressActionAllow,
	}
}

// TestEgressRules_Composition proves how EgressRules() composes the required
// baseline with project firewall config: baseline first, then explicit rules
// verbatim (no Port/Action defaulting), then add_domains expansions — order
// asserted. The baseline's own content is taken from RequiredFirewallRules()
// at runtime; its semantic security properties are guarded separately by
// TestRequiredFirewallRules in config_test.go.
func TestEgressRules_Composition(t *testing.T) {
	// Explicit rules used by the pass-through case. Port/Action are NOT
	// defaulted by EgressRules() — explicit rules pass through verbatim,
	// proven by the port-less deny rule and the action-less tcp range rule.
	sshRule := config.EgressRule{Dst: "internal.corp", Proto: "ssh", Port: "22", Action: config.EgressActionAllow}
	denyRule := config.EgressRule{Dst: "evil.example", Proto: config.EgressProtoHTTPS, Action: config.EgressActionDeny}

	const explicitRulesYAML = `
security:
  firewall:
    rules:
      - dst: internal.corp
        proto: ssh
        port: "22"
        action: allow
      - dst: evil.example
        proto: https
        action: deny
`

	tests := []struct {
		name        string
		projectYAML string
		// additions is what EgressRules() must append after the baseline:
		// explicit rules first, then add_domains expansions — order asserted.
		additions []config.EgressRule
	}{
		{
			name:        "no firewall section yields baseline only",
			projectYAML: "",
		},
		{
			name: "empty firewall section yields baseline only",
			projectYAML: `
security:
  firewall: {}
`,
		},
		{
			name: "add_domains each expand to https 443 allow",
			projectYAML: `
security:
  firewall:
    add_domains:
      - example.com
      - registry.example.dev
`,
			additions: []config.EgressRule{
				httpsAllow("example.com"),
				httpsAllow("registry.example.dev"),
			},
		},
		{
			name: "rules and add_domains combine as baseline then rules then domains",
			projectYAML: explicitRulesYAML + `      - dst: 10.0.0.5
        proto: tcp
        port: 9000-9100
    add_domains:
      - example.com
`,
			additions: []config.EgressRule{
				sshRule,
				denyRule,
				{Dst: "10.0.0.5", Proto: "tcp", Port: "9000-9100"},
				httpsAllow("example.com"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := configmocks.NewFromString(tt.projectYAML, "")

			baseline := cfg.RequiredFirewallRules()
			want := append(append([]config.EgressRule{}, baseline...), tt.additions...)
			assert.Equal(t, want, cfg.EgressRules())
		})
	}
}
