package docker

import (
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

	env, err := RuntimeEnv(cfg)
	require.NoError(t, err)

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

	env, err := RuntimeEnv(cfg)
	require.NoError(t, err)

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

	env, err := RuntimeEnv(cfg)
	require.NoError(t, err)

	var found bool
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "CLAWKER_FIREWALL_DOMAINS="); ok {
			found = true
			assert.Contains(t, val, "custom.com")
			// Should also contain default domains (e.g. registry.npmjs.org from defaults)
			assert.Contains(t, val, "registry.npmjs.org")
		}
	}
	require.True(t, found, "expected CLAWKER_FIREWALL_DOMAINS env var")

	// Should NOT have override flag
	for _, e := range env {
		assert.NotEqual(t, "CLAWKER_FIREWALL_OVERRIDE=true", e,
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

	env, err := RuntimeEnv(cfg)
	require.NoError(t, err)

	assert.Contains(t, env, "CLAWKER_FIREWALL_OVERRIDE=true")
}

func TestRuntimeEnv_FirewallDisabled(t *testing.T) {
	cfg := &config.Config{
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: false},
		},
	}

	env, err := RuntimeEnv(cfg)
	require.NoError(t, err)

	for _, e := range env {
		assert.NotContains(t, e, "CLAWKER_FIREWALL_DOMAINS=",
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

	env, err := RuntimeEnv(cfg)
	require.NoError(t, err)

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

	env, err := RuntimeEnv(cfg)
	require.NoError(t, err)

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

	env, err := RuntimeEnv(cfg)
	require.NoError(t, err)

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

	// Run multiple times — output should be identical (sorted keys)
	env1, err := RuntimeEnv(cfg)
	require.NoError(t, err)
	env2, err := RuntimeEnv(cfg)
	require.NoError(t, err)

	assert.Equal(t, env1, env2, "RuntimeEnv should produce consistent output")
}

func TestRuntimeEnv_Precedence(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			Env: map[string]string{
				"EDITOR": "vim",        // Overrides base default of "nano"
				"SHARED": "from-agent",
			},
		},
		Build: config.BuildConfig{
			Instructions: &config.DockerInstructions{
				Env: map[string]string{
					"SHARED": "from-instructions", // Overrides agent env
				},
			},
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: false},
		},
	}

	env, err := RuntimeEnv(cfg)
	require.NoError(t, err)

	assert.Contains(t, env, "EDITOR=vim", "agent env should override base default")
	assert.Contains(t, env, "SHARED=from-instructions", "instruction env should override agent env")
}
