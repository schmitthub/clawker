package docker

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
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

func TestRuntimeEnv_FirewallIPRangeSources(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled: true,
		FirewallIPRangeSources: []config.IPRangeSource{
			{Name: "github"},
			{Name: "google-cloud"},
		},
	})
	require.NoError(t, err)

	var found bool
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "CLAWKER_FIREWALL_IP_RANGE_SOURCES="); ok {
			found = true
			assert.Contains(t, val, `"name":"github"`)
			assert.Contains(t, val, `"name":"google-cloud"`)
		}
	}
	require.True(t, found, "expected CLAWKER_FIREWALL_IP_RANGE_SOURCES env var")
}

func TestRuntimeEnv_FirewallIPRangeSourcesWithCustomURL(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled: true,
		FirewallIPRangeSources: []config.IPRangeSource{
			{Name: "custom", URL: "https://example.com/ranges.json", JQFilter: ".cidrs[]"},
		},
	})
	require.NoError(t, err)

	var found bool
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "CLAWKER_FIREWALL_IP_RANGE_SOURCES="); ok {
			found = true
			assert.Contains(t, val, `"name":"custom"`)
			assert.Contains(t, val, `"url":"https://example.com/ranges.json"`)
			assert.Contains(t, val, `"jq_filter":".cidrs[]"`)
		}
	}
	require.True(t, found, "expected CLAWKER_FIREWALL_IP_RANGE_SOURCES env var")
}

func TestRuntimeEnv_FirewallIPRangeSourcesNil(t *testing.T) {
	// When firewall is enabled but IP range sources is nil, should serialize as empty array
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled:        true,
		FirewallIPRangeSources: nil,
	})
	require.NoError(t, err)

	var found bool
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "CLAWKER_FIREWALL_IP_RANGE_SOURCES="); ok {
			found = true
			assert.Equal(t, "[]", val, "nil sources should serialize as empty JSON array")
		}
	}
	require.True(t, found, "expected CLAWKER_FIREWALL_IP_RANGE_SOURCES env var")
}

func TestRuntimeEnv_FirewallDisabledNoIPRangeSources(t *testing.T) {
	// When firewall is disabled, IP range sources env var should not be set
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled: false,
		FirewallIPRangeSources: []config.IPRangeSource{
			{Name: "github"},
		},
	})
	require.NoError(t, err)

	for _, e := range env {
		assert.NotContains(t, e, "CLAWKER_FIREWALL_IP_RANGE_SOURCES=",
			"should not set IP range sources when firewall disabled")
	}
}

func TestRuntimeEnv_ClawkerIdentity(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Project:         "myproject",
		Agent:           "ralph",
		WorkspaceMode:   "bind",
		WorkspaceSource: "/home/user/myproject",
	})
	require.NoError(t, err)

	assert.Contains(t, env, "CLAWKER_PROJECT=myproject")
	assert.Contains(t, env, "CLAWKER_AGENT=ralph")
	assert.Contains(t, env, "CLAWKER_WORKSPACE_MODE=bind")
	assert.Contains(t, env, "CLAWKER_WORKSPACE_SOURCE=/home/user/myproject")
}

func TestRuntimeEnv_ClawkerIdentitySnapshotMode(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Project:         "myapp",
		Agent:           "dev",
		WorkspaceMode:   "snapshot",
		WorkspaceSource: "/var/clawker/worktrees/feature-branch",
	})
	require.NoError(t, err)

	assert.Contains(t, env, "CLAWKER_PROJECT=myapp")
	assert.Contains(t, env, "CLAWKER_AGENT=dev")
	assert.Contains(t, env, "CLAWKER_WORKSPACE_MODE=snapshot")
	assert.Contains(t, env, "CLAWKER_WORKSPACE_SOURCE=/var/clawker/worktrees/feature-branch")
}

func TestRuntimeEnv_ClawkerIdentityEmpty(t *testing.T) {
	// When identity fields are empty, they should not be set
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Project:         "",
		Agent:           "",
		WorkspaceMode:   "",
		WorkspaceSource: "",
	})
	require.NoError(t, err)

	for _, e := range env {
		assert.False(t, strings.HasPrefix(e, "CLAWKER_PROJECT="),
			"should not set CLAWKER_PROJECT when empty")
		assert.False(t, strings.HasPrefix(e, "CLAWKER_AGENT="),
			"should not set CLAWKER_AGENT when empty")
		assert.False(t, strings.HasPrefix(e, "CLAWKER_WORKSPACE_MODE="),
			"should not set CLAWKER_WORKSPACE_MODE when empty")
		assert.False(t, strings.HasPrefix(e, "CLAWKER_WORKSPACE_SOURCE="),
			"should not set CLAWKER_WORKSPACE_SOURCE when empty")
	}
}

func TestRuntimeEnv_ClawkerIdentityPartial(t *testing.T) {
	// Partial identity (e.g., orphaned project with no project name)
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Agent:           "ralph",
		WorkspaceMode:   "bind",
		WorkspaceSource: "/tmp/orphaned-dir",
	})
	require.NoError(t, err)

	// Should NOT have project
	for _, e := range env {
		assert.False(t, strings.HasPrefix(e, "CLAWKER_PROJECT="),
			"should not set CLAWKER_PROJECT when empty")
	}

	// Should have agent, mode, and source
	assert.Contains(t, env, "CLAWKER_AGENT=ralph")
	assert.Contains(t, env, "CLAWKER_WORKSPACE_MODE=bind")
	assert.Contains(t, env, "CLAWKER_WORKSPACE_SOURCE=/tmp/orphaned-dir")
}
