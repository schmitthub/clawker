package docker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeEnv_Defaults(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{})
	require.NoError(t, err)

	assert.Contains(t, env, "EDITOR=nano")
	assert.Contains(t, env, "VISUAL=nano")
}

func TestRuntimeEnv_EditorOverride(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Editor: "vim",
		Visual: "code",
	})
	require.NoError(t, err)

	assert.Contains(t, env, "EDITOR=vim")
	assert.Contains(t, env, "VISUAL=code")
}

func TestRuntimeEnv_FirewallDomains(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled: true,
		FirewallDomains: []string{"custom.com", "registry.npmjs.org"},
	})
	require.NoError(t, err)

	var found bool
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "CLAWKER_FIREWALL_DOMAINS="); ok {
			found = true
			assert.Contains(t, val, "custom.com")
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
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled:  true,
		FirewallDomains:  []string{"only-this.com"},
		FirewallOverride: true,
	})
	require.NoError(t, err)

	assert.Contains(t, env, "CLAWKER_FIREWALL_OVERRIDE=true")
}

func TestRuntimeEnv_FirewallDisabled(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled: false,
	})
	require.NoError(t, err)

	for _, e := range env {
		assert.NotContains(t, e, "CLAWKER_FIREWALL_DOMAINS=",
			"should not set firewall domains when disabled")
	}
}

func TestRuntimeEnv_AgentEnv(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		AgentEnv: map[string]string{"FOO": "bar", "BAZ": "qux"},
	})
	require.NoError(t, err)

	assert.Contains(t, env, "FOO=bar")
	assert.Contains(t, env, "BAZ=qux")
}

func TestRuntimeEnv_InstructionEnv(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		InstructionEnv: map[string]string{"NODE_ENV": "production"},
	})
	require.NoError(t, err)

	assert.Contains(t, env, "NODE_ENV=production")
}

func TestRuntimeEnv_NilMaps(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{})
	require.NoError(t, err)

	assert.Contains(t, env, "EDITOR=nano")
}

func TestRuntimeEnv_Deterministic(t *testing.T) {
	opts := RuntimeEnvOpts{
		Editor:          "vim",
		FirewallEnabled: true,
		FirewallDomains: []string{"example.com"},
		AgentEnv:        map[string]string{"A": "1", "B": "2"},
	}

	env1, err := RuntimeEnv(opts)
	require.NoError(t, err)
	env2, err := RuntimeEnv(opts)
	require.NoError(t, err)

	assert.Equal(t, env1, env2, "RuntimeEnv should produce consistent output")
}

func TestRuntimeEnv_Precedence(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		AgentEnv:       map[string]string{"EDITOR": "vim", "SHARED": "from-agent"},
		InstructionEnv: map[string]string{"SHARED": "from-instructions"},
	})
	require.NoError(t, err)

	assert.Contains(t, env, "EDITOR=vim", "agent env should override base default")
	assert.Contains(t, env, "SHARED=from-instructions", "instruction env should override agent env")
}

func TestRuntimeEnv_256Color(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Is256Color: true,
	})
	require.NoError(t, err)

	assert.Contains(t, env, "TERM=xterm-256color")
}

func TestRuntimeEnv_TrueColor(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Is256Color: true,
		TrueColor:  true,
	})
	require.NoError(t, err)

	assert.Contains(t, env, "TERM=xterm-256color")
	assert.Contains(t, env, "COLORTERM=truecolor")
}

func TestRuntimeEnv_NoColorCapabilities(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Is256Color: false,
		TrueColor:  false,
	})
	require.NoError(t, err)

	for _, e := range env {
		assert.False(t, strings.HasPrefix(e, "TERM="),
			"should not set TERM when no color capabilities")
		assert.False(t, strings.HasPrefix(e, "COLORTERM="),
			"should not set COLORTERM when no color capabilities")
	}
}

func TestRuntimeEnv_AgentEnvOverridesTerm(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Is256Color: true,
		TrueColor:  true,
		AgentEnv:   map[string]string{"TERM": "xterm", "COLORTERM": ""},
	})
	require.NoError(t, err)

	assert.Contains(t, env, "TERM=xterm", "agent env should override terminal detection")
	assert.Contains(t, env, "COLORTERM=", "agent env should override terminal detection")
}

func TestRuntimeEnv_TrueColorWithout256Color(t *testing.T) {
	// Edge case: TrueColor=true with Is256Color=false
	// This is a caller bug (truecolor implies 256), but the function should handle it gracefully
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Is256Color: false,
		TrueColor:  true,
	})
	require.NoError(t, err)

	// Should set COLORTERM but not TERM (following the literal flags)
	assert.Contains(t, env, "COLORTERM=truecolor")
	for _, e := range env {
		assert.False(t, strings.HasPrefix(e, "TERM="),
			"should not set TERM when Is256Color=false")
	}
}

func TestRuntimeEnv_FirewallEnabledWithNilDomains(t *testing.T) {
	// Edge case: FirewallEnabled=true with FirewallDomains=nil
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled: true,
		FirewallDomains: nil,
	})
	require.NoError(t, err)

	// Should produce valid JSON (empty array)
	var found bool
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "CLAWKER_FIREWALL_DOMAINS="); ok {
			found = true
			assert.Equal(t, "[]", val, "nil domains should serialize as empty JSON array")
		}
	}
	require.True(t, found, "expected CLAWKER_FIREWALL_DOMAINS env var")
}

func TestRuntimeEnv_FirewallEnabledWithEmptyDomains(t *testing.T) {
	// Edge case: FirewallEnabled=true with FirewallDomains=[]string{}
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled: true,
		FirewallDomains: []string{},
	})
	require.NoError(t, err)

	// Should produce valid JSON (empty array)
	var found bool
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "CLAWKER_FIREWALL_DOMAINS="); ok {
			found = true
			assert.Equal(t, "[]", val, "empty domains should serialize as empty JSON array")
		}
	}
	require.True(t, found, "expected CLAWKER_FIREWALL_DOMAINS env var")
}
