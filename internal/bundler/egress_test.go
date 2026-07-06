package bundler_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/harness"
)

// isolateConfigDir points the conventional bundle location at an empty temp
// dir so floor resolution deterministically falls back to the embedded
// bundle, regardless of what the developer machine has materialized.
func isolateConfigDir(t *testing.T) {
	t.Helper()
	t.Setenv("CLAWKER_CONFIG_DIR", t.TempDir())
}

func dstSet(rules []config.EgressRule) map[string]config.EgressRule {
	m := make(map[string]config.EgressRule, len(rules))
	for _, r := range rules {
		m[r.Dst] = r
	}
	return m
}

// TestEgressRules_ClaudeFloor guards the semantic security properties of the
// claude harness's required egress floor (formerly requiredFirewallRules in
// config/defaults.go — the guards moved here with the data).
func TestEgressRules_ClaudeFloor(t *testing.T) {
	isolateConfigDir(t)
	cfg := configmocks.NewBlankConfig()

	rules, err := bundler.EgressRules(cfg, "")
	require.NoError(t, err)
	require.NotEmpty(t, rules)

	byDst := dstSet(rules)

	// OAuth domains listed explicitly (SNI filtering selects per-domain TLS
	// filter chains — shared IPs don't cover for missing entries).
	for _, dst := range []string{
		"api.anthropic.com", "claude.com", "platform.claude.com",
		".claude.ai", "mcp-proxy.anthropic.com", "registry.npmjs.org",
	} {
		assert.Contains(t, byDst, dst)
	}
	assert.Contains(t, byDst, ".datadoghq.com", "Datadog wildcard should use leading-dot convention")
	assert.Contains(t, byDst, ".datadoghq.eu", "Datadog EU wildcard should use leading-dot convention")

	// Image pulls run on the host daemon — no docker registry egress needed.
	assert.NotContains(t, byDst, "registry-1.docker.io",
		"Docker registry domains should not be in the floor — image pulls go through host daemon")

	// .claude.ai UGC surfaces are denied so an injected prompt can't pivot
	// into fetching attacker-authored content from a trusted origin;
	// PathDefault stays empty so EffectivePathDefault yields denylist mode
	// (OAuth/login flows stay intact).
	claudeAI := byDst[".claude.ai"]
	require.Len(t, claudeAI.PathRules, 2)
	assert.Equal(t,
		config.PathRule{Path: "/public/", Action: config.EgressActionDeny, Methods: nil},
		claudeAI.PathRules[0])
	assert.Equal(t,
		config.PathRule{Path: "/share/", Action: config.EgressActionDeny, Methods: nil},
		claudeAI.PathRules[1])
	assert.Empty(t, claudeAI.PathDefault)

	// No deny rules besides path scoping — every floor entry is an allow
	// (empty action normalizes to allow server-side).
	for _, r := range rules {
		assert.NotEqual(t, config.EgressActionDeny, r.Action, "floor rule %s must not be a whole-host deny", r.Dst)
	}
}

// TestEgressRules_ComposesProjectRules proves composition order: harness
// floor first, then the project's explicit rules, then add_domains
// expansions.
func TestEgressRules_ComposesProjectRules(t *testing.T) {
	isolateConfigDir(t)
	cfg := configmocks.NewFromString(`
security:
  firewall:
    rules:
      - dst: internal.corp
        proto: ssh
        port: "22"
    add_domains:
      - example.com
`, "")

	rules, err := bundler.EgressRules(cfg, "")
	require.NoError(t, err)

	floorOnly := configmocks.NewBlankConfig()
	floor, err := bundler.EgressRules(floorOnly, "")
	require.NoError(t, err)

	require.Len(t, rules, len(floor)+2)
	assert.Equal(t, floor, rules[:len(floor)])
	assert.Equal(t, config.EgressRule{
		Dst: "internal.corp", Proto: "ssh", Port: "22", Action: "",
		PathRules: nil, PathDefault: "", InsecureSkipTLSVerify: false,
	}, rules[len(floor)])
	assert.Equal(t, config.EgressRule{
		Dst: "example.com", Proto: config.EgressProtoHTTPS, Port: config.EgressPortHTTPS,
		Action: config.EgressActionAllow, PathRules: nil, PathDefault: "",
		InsecureSkipTLSVerify: false,
	}, rules[len(floor)+1])
}

// TestEgressRules_ExternalBundle proves a user-authored bundle wired in via
// a registry path entry supplies the floor — the harness swap swaps the
// floor, with no anthropic egress forced on a non-claude harness.
func TestEgressRules_ExternalBundle(t *testing.T) {
	isolateConfigDir(t)
	dir := t.TempDir()
	writeBundle(t, dir, `
version:
  resolver: none
egress:
  - dst: api.openai.com
  - dst: auth.openai.com
    path_rules:
      - { path: /oauth/, action: allow }
  - dst: git.example.com
    proto: ssh
    port: "22"
`)

	cfg := configmocks.NewFromString("", fmt.Sprintf(`
harnesses:
  codex: { default: true, path: %s }
`, dir))

	rules, err := bundler.EgressRules(cfg, "")
	require.NoError(t, err)

	byDst := dstSet(rules)
	assert.NotContains(t, byDst, "api.anthropic.com", "claude floor must not leak into another harness")
	assert.Contains(t, byDst, "api.openai.com")
	assert.Equal(t, "ssh", byDst["git.example.com"].Proto)
	require.Len(t, byDst["auth.openai.com"].PathRules, 1)
	assert.Equal(t,
		config.PathRule{Path: "/oauth/", Action: config.EgressActionAllow, Methods: nil},
		byDst["auth.openai.com"].PathRules[0])
}

// TestEgressRules_ResolutionErrorsPropagate: a broken registry (two defaults)
// or an unknown explicit harness must fail the sync loudly, never fall back
// to a silently wrong floor.
func TestEgressRules_ResolutionErrorsPropagate(t *testing.T) {
	isolateConfigDir(t)

	t.Run("multiple defaults", func(t *testing.T) {
		cfg := configmocks.NewFromString("", `
harnesses:
  a: { default: true }
  b: { default: true }
`)
		_, err := bundler.EgressRules(cfg, "")
		require.ErrorContains(t, err, "multiple harnesses marked default")
	})

	t.Run("unknown explicit harness", func(t *testing.T) {
		cfg := configmocks.NewBlankConfig()
		_, err := bundler.EgressRules(cfg, "nonexistent")
		require.ErrorContains(t, err, "is not registered")
	})
}

// writeBundle writes a minimal loadable bundle (manifest + template) to dir.
func writeBundle(t *testing.T, dir, manifestYAML string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, harness.ManifestFile), []byte(manifestYAML), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, harness.TemplateFile),
		[]byte("{{define \"block_5\"}}CMD [\"codex\"]\n{{end}}\n"), 0o644))
}
