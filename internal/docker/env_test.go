package docker

import (
	"sort"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeEnv_Defaults(t *testing.T) {
	cfg := &config.Config{
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: false},
		},
	}

	env := RuntimeEnv(cfg)

	assert.Contains(t, env, "EDITOR=nano")
	assert.Contains(t, env, "VISUAL=nano")
}

func TestRuntimeEnv_EditorOverride(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Editor: "vim",
			Visual: "code",
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: false},
		},
	}

	env := RuntimeEnv(cfg)

	assert.Contains(t, env, "EDITOR=vim")
	assert.Contains(t, env, "VISUAL=code")
}

func TestRuntimeEnv_FirewallDomains(t *testing.T) {
	cfg := &config.Config{
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{
				Enable:     true,
				AddDomains: []string{"custom.com"},
			},
		},
	}

	env := RuntimeEnv(cfg)

	var found bool
	for _, e := range env {
		if strings.HasPrefix(e, "CLAWKER_FIREWALL_DOMAINS=") {
			found = true
			assert.Contains(t, e, "custom.com")
			// Should also contain default domains (e.g. registry.npmjs.org from defaults)
			assert.Contains(t, e, "registry.npmjs.org")
		}
	}
	require.True(t, found, "expected CLAWKER_FIREWALL_DOMAINS env var")

	// Should NOT have override flag
	for _, e := range env {
		assert.False(t, strings.HasPrefix(e, "CLAWKER_FIREWALL_OVERRIDE="),
			"should not set override when not in override mode")
	}
}

func TestRuntimeEnv_FirewallOverride(t *testing.T) {
	cfg := &config.Config{
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{
				Enable:          true,
				OverrideDomains: []string{"only-this.com"},
			},
		},
	}

	env := RuntimeEnv(cfg)

	assert.Contains(t, env, "CLAWKER_FIREWALL_OVERRIDE=true")
}

func TestRuntimeEnv_FirewallDisabled(t *testing.T) {
	cfg := &config.Config{
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: false},
		},
	}

	env := RuntimeEnv(cfg)

	for _, e := range env {
		assert.False(t, strings.HasPrefix(e, "CLAWKER_FIREWALL_DOMAINS="),
			"should not set firewall domains when disabled")
	}
}

func TestRuntimeEnv_AgentEnv(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Env: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: false},
		},
	}

	env := RuntimeEnv(cfg)

	assert.Contains(t, env, "FOO=bar")
	assert.Contains(t, env, "BAZ=qux")
}

func TestRuntimeEnv_InstructionEnv(t *testing.T) {
	cfg := &config.Config{
		Build: config.BuildConfig{
			Instructions: &config.DockerInstructions{
				Env: map[string]string{
					"NODE_ENV": "production",
				},
			},
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: false},
		},
	}

	env := RuntimeEnv(cfg)

	assert.Contains(t, env, "NODE_ENV=production")
}

func TestRuntimeEnv_NilInstructions(t *testing.T) {
	cfg := &config.Config{
		Build: config.BuildConfig{
			Instructions: nil,
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: false},
		},
	}

	env := RuntimeEnv(cfg)

	// Should not panic, should still have editor defaults
	assert.Contains(t, env, "EDITOR=nano")
}

func TestRuntimeEnv_Deterministic(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Editor: "vim",
			Env:    map[string]string{"A": "1", "B": "2"},
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: true},
		},
	}

	// Run multiple times to check determinism of non-map parts
	env1 := RuntimeEnv(cfg)
	env2 := RuntimeEnv(cfg)

	// Sort both for comparison (map iteration order may vary)
	sort.Strings(env1)
	sort.Strings(env2)

	assert.Equal(t, env1, env2, "RuntimeEnv should produce consistent output")
}
