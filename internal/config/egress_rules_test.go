// External test package: configmocks imports config, so exercising the real
// ProjectEgressRules() composition through configmocks requires package
// config_test.
package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
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

// TestProjectEgressRules_Composition proves ProjectEgressRules() returns the
// project's security.firewall contribution only — explicit rules verbatim
// (no Port/Action defaulting), then add_domains expansions, order asserted.
// The harness egress floor is deliberately absent: bundler.EgressRules
// composes it, and its content is guarded by the bundler egress tests.
func TestProjectEgressRules_Composition(t *testing.T) {
	// Explicit rules used by the pass-through case. Port/Action are NOT
	// defaulted by ProjectEgressRules() — explicit rules pass through
	// verbatim, proven by the port-less deny rule and the action-less tcp
	// range rule.
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
		want        []config.EgressRule
	}{
		{
			name:        "no firewall section yields nothing",
			projectYAML: "",
		},
		{
			name: "empty firewall section yields nothing",
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
			want: []config.EgressRule{
				httpsAllow("example.com"),
				httpsAllow("registry.example.dev"),
			},
		},
		{
			name: "rules and add_domains combine as rules then domains",
			projectYAML: explicitRulesYAML + `      - dst: 10.0.0.5
        proto: tcp
        port: 9000-9100
    add_domains:
      - example.com
`,
			want: []config.EgressRule{
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
			assert.Equal(t, tt.want, cfg.ProjectEgressRules())
		})
	}
}
