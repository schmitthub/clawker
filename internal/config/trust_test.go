package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/testenv"
)

// digestsFor loads a config anchored at an isolated project dir that holds
// projectYAML, with userYAML in the isolated user config dir, and returns
// its trust digests.
func digestsFor(t *testing.T, env *testenv.Env, projDir, projectYAML, userYAML string) map[string]string {
	t.Helper()
	env.WriteYAML(t, testenv.ProjectConfig, projDir, projectYAML)
	if userYAML != "" {
		require.NoError(t, os.WriteFile(filepath.Join(env.Dirs.Config, "clawker.yaml"), []byte(userYAML), 0o644))
	}
	cfg, err := config.NewConfig(config.WithProjectRoot(projDir))
	require.NoError(t, err)
	digests, err := config.ProjectTrustDigests(cfg)
	require.NoError(t, err)
	return digests
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

func TestProjectTrustDigests_ProjectLayersOnly(t *testing.T) {
	env := testenv.New(t)
	projDir := filepath.Join(env.Dirs.Base, "proj")
	t.Chdir(mkdir(t, projDir))

	first := digestsFor(t, env, projDir, "security:\n  docker_socket: true\n", "security:\n  cap_add: [SYS_PTRACE]\n")
	assert.Contains(t, first, "security")
	assert.NotContains(t, first, "build", "no project layer sets a build key")

	// The user config dir is not agent-writable: its change is invisible.
	userChanged := digestsFor(t, env, projDir,
		"security:\n  docker_socket: true\n", "security:\n  cap_add: [NET_RAW]\n")
	assert.Equal(t, first, userChanged)

	// A covered key in the project file changes the group digest.
	projectChanged := digestsFor(t, env, projDir, "security:\n  docker_socket: false\n", "")
	assert.NotEqual(t, first["security"], projectChanged["security"])
}

func TestProjectTrustDigests_IgnoresUncoveredKeys(t *testing.T) {
	env := testenv.New(t)
	projDir := filepath.Join(env.Dirs.Base, "proj")
	t.Chdir(mkdir(t, projDir))

	base := "agent:\n  from_env: [AWS_PROFILE]\n  editor: vim\nbuild:\n  packages: [curl]\n  instructions:\n    root_run: [\"apt-get install -y x\"]\n"
	first := digestsFor(t, env, projDir, base, "")
	assert.Contains(t, first, "agent")
	assert.Contains(t, first, "build")

	uncoveredEdit := "agent:\n  from_env: [AWS_PROFILE]\n  editor: emacs\n" +
		"build:\n  packages: [curl, jq]\n  instructions:\n    root_run: [\"apt-get install -y x\"]\n"
	uncovered := digestsFor(t, env, projDir, uncoveredEdit, "")
	assert.Equal(t, first, uncovered)

	coveredEdit := "agent:\n  from_env: [AWS_PROFILE, GITHUB_TOKEN]\n  editor: emacs\n" +
		"build:\n  packages: [curl]\n  instructions:\n    root_run: [\"apt-get install -y x\"]\n"
	covered := digestsFor(t, env, projDir, coveredEdit, "")
	assert.NotEqual(t, first["agent"], covered["agent"])
	assert.Equal(t, first["build"], covered["build"])
}

func TestProjectTrustDigests_StableAcrossKeyOrder(t *testing.T) {
	env := testenv.New(t)
	projDir := filepath.Join(env.Dirs.Base, "proj")
	t.Chdir(mkdir(t, projDir))

	ordered := "security:\n  docker_socket: true\n  cap_add: [SYS_PTRACE]\n" +
		"harnesses:\n  claude:\n    from_env: [A]\n    post_init: echo\n"
	first := digestsFor(t, env, projDir, ordered, "")

	shuffled := "harnesses:\n  claude:\n    post_init: echo\n    from_env: [A]\n" +
		"security:\n  cap_add: [SYS_PTRACE]\n  docker_socket: true\n"
	reordered := digestsFor(t, env, projDir, shuffled, "")
	assert.Equal(t, first, reordered)
	assert.Contains(t, first, "harnesses")
}

func TestProjectTrustDigests_NoProjectRoot(t *testing.T) {
	cfg, err := config.NewBlankConfig()
	require.NoError(t, err)
	digests, err := config.ProjectTrustDigests(cfg)
	require.NoError(t, err)
	assert.Empty(t, digests)
}

func TestChangedTrustGroups(t *testing.T) {
	changed := config.ChangedTrustGroups(
		map[string]string{"security": "a", "build": "b"},
		map[string]string{"security": "a", "agent": "c"},
	)
	assert.Equal(t, []string{"build", "agent"}, changed)
	assert.Empty(t, config.ChangedTrustGroups(map[string]string{"security": "a"}, map[string]string{"security": "a"}))
	assert.Equal(t, config.TrustedProjectKeyGroups(), config.ChangedTrustGroups(nil, map[string]string{
		"security": "1", "bundles": "1", "build": "1", "agent": "1", "harnesses": "1",
	}))
}
