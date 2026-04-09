package firewall_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	fwmocks "github.com/schmitthub/clawker/internal/firewall/mocks"
	"github.com/schmitthub/clawker/internal/testenv"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestFormatPortMappings_LocalLayerPriority(t *testing.T) {
	// Store has gitlab SSH first (added earlier), then github SSH.
	// Local layer (CWD) has github SSH.
	// Result: github's mapping should come first because the local layer
	// is prepended before normalizeAndDedup (first-seen wins).
	env := testenv.New(t)

	parentDir := filepath.Join(env.Dirs.Base, "repo")
	childDir := filepath.Join(parentDir, "subproject")
	require.NoError(t, os.MkdirAll(childDir, 0o755))

	env.WriteYAML(t, testenv.ProjectConfig, parentDir, `
security:
  firewall:
    rules:
      - dst: gitlab.com
        proto: ssh
        port: 22
        action: allow
`)
	env.WriteYAML(t, testenv.ProjectConfig, childDir, `
security:
  firewall:
    rules:
      - dst: github.com
        proto: ssh
        port: 22
        action: allow
`)

	chdir(t, childDir)

	cfg, err := config.NewConfig()
	require.NoError(t, err)

	mgr := fwmocks.NewTestManager(t, cfg)

	// Populate the store with gitlab first, then github (simulates earlier session).
	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "gitlab.com", Proto: "ssh", Port: 22, Action: "allow"},
		{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
	}))

	result := mgr.FormatPortMappings()

	// github (from local layer) should be prepended and win first-seen dedup,
	// getting TCPPortBase (10001). gitlab gets 10002.
	parts := strings.Split(result, ";")
	require.Len(t, parts, 2, "expected 2 TCP mappings, got: %s", result)

	assert.Equal(t, "22|10001", parts[0], "github (local) should get first envoy port")
	assert.Equal(t, "22|10002", parts[1], "gitlab (parent) should get second envoy port")
}

func TestFormatPortMappings_ThreeProviders_LocalWins(t *testing.T) {
	// Store: gitlab, github, bitbucket (in that order).
	// Local layer: bitbucket only.
	// Result: bitbucket first, then gitlab, github.
	env := testenv.New(t)

	projectDir := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	env.WriteYAML(t, testenv.ProjectConfig, projectDir, `
security:
  firewall:
    rules:
      - dst: bitbucket.org
        proto: ssh
        port: 22
        action: allow
`)

	chdir(t, projectDir)

	cfg, err := config.NewConfig()
	require.NoError(t, err)

	mgr := fwmocks.NewTestManager(t, cfg)

	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "gitlab.com", Proto: "ssh", Port: 22, Action: "allow"},
		{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
		{Dst: "bitbucket.org", Proto: "ssh", Port: 22, Action: "allow"},
	}))

	result := mgr.FormatPortMappings()

	parts := strings.Split(result, ";")
	require.Len(t, parts, 3, "expected 3 TCP mappings, got: %s", result)

	assert.Equal(t, "22|10001", parts[0], "bitbucket (local) should be first")
	assert.Equal(t, "22|10002", parts[1], "gitlab should be second")
	assert.Equal(t, "22|10003", parts[2], "github should be third")
}

func TestFormatPortMappings_NoLocalLayer(t *testing.T) {
	// No local layer with firewall rules — store order preserved.
	env := testenv.New(t, testenv.WithConfig())
	cfg := env.Config()

	mgr := fwmocks.NewTestManager(t, cfg)

	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "gitlab.com", Proto: "ssh", Port: 22, Action: "allow"},
		{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
	}))

	result := mgr.FormatPortMappings()

	parts := strings.Split(result, ";")
	require.Len(t, parts, 2)

	// Store order: gitlab first.
	assert.Equal(t, "22|10001", parts[0])
	assert.Equal(t, "22|10002", parts[1])
}

func TestFormatPortMappings_LocalMatchesStoreOrder(t *testing.T) {
	// Local layer matches the first store entry — no reordering needed.
	env := testenv.New(t)

	projectDir := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	env.WriteYAML(t, testenv.ProjectConfig, projectDir, `
security:
  firewall:
    rules:
      - dst: github.com
        proto: ssh
        port: 22
        action: allow
`)

	chdir(t, projectDir)

	cfg, err := config.NewConfig()
	require.NoError(t, err)

	mgr := fwmocks.NewTestManager(t, cfg)

	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
		{Dst: "gitlab.com", Proto: "ssh", Port: 22, Action: "allow"},
	}))

	result := mgr.FormatPortMappings()

	parts := strings.Split(result, ";")
	require.Len(t, parts, 2)

	assert.Equal(t, "22|10001", parts[0])
	assert.Equal(t, "22|10002", parts[1])
}

func TestFormatPortMappings_EmptyStore(t *testing.T) {
	env := testenv.New(t, testenv.WithConfig())
	mgr := fwmocks.NewTestManager(t, env.Config())
	assert.Empty(t, mgr.FormatPortMappings())
}

func TestFormatPortMappings_OnlyTLSRules(t *testing.T) {
	env := testenv.New(t, testenv.WithConfig())
	mgr := fwmocks.NewTestManager(t, env.Config())

	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "github.com", Proto: "tls", Port: 443, Action: "allow"},
	}))

	assert.Empty(t, mgr.FormatPortMappings(), "TLS rules should not produce TCP port mappings")
}
