package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveAgentEnv_Empty(t *testing.T) {
	result, warnings, err := ResolveAgentEnv(AgentConfig{}, t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Empty(t, warnings)
}

func TestResolveAgentEnv_StaticEnvOnly(t *testing.T) {
	agent := AgentConfig{
		Env: map[string]string{"FOO": "bar", "BAZ": "qux"},
	}
	result, _, err := ResolveAgentEnv(agent, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"FOO": "bar", "BAZ": "qux"}, result)
}

func TestResolveAgentEnv_EnvFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("FOO=from-file\nBAR=also-file\n"), 0644))

	agent := AgentConfig{
		EnvFile: []string{envFile},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"FOO": "from-file", "BAR": "also-file"}, result)
}

func TestResolveAgentEnv_EnvFileComments(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "# This is a comment\nFOO=bar\n\n# Another comment\nBAZ=qux\n"
	require.NoError(t, os.WriteFile(envFile, []byte(content), 0644))

	agent := AgentConfig{
		EnvFile: []string{envFile},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"FOO": "bar", "BAZ": "qux"}, result)
}

func TestResolveAgentEnv_EnvFileRelativePath(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "secrets.env")
	require.NoError(t, os.WriteFile(envFile, []byte("SECRET=value\n"), 0644))

	agent := AgentConfig{
		EnvFile: []string{"secrets.env"},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"SECRET": "value"}, result)
}

func TestResolveAgentEnv_EnvFileTildeExpansion(t *testing.T) {
	fakeHome := t.TempDir()
	userHomeDir = func() (string, error) { return fakeHome, nil }
	t.Cleanup(func() { userHomeDir = os.UserHomeDir })

	// Create a temp file in the fake home directory
	envFile := filepath.Join(fakeHome, ".clawker-test-env")
	require.NoError(t, os.WriteFile(envFile, []byte("TILDE=expanded\n"), 0644))

	agent := AgentConfig{
		EnvFile: []string{"~/.clawker-test-env"},
	}
	result, _, err := ResolveAgentEnv(agent, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"TILDE": "expanded"}, result)
}

func TestResolveAgentEnv_EnvFileEnvVarExpansion(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "test.env")
	require.NoError(t, os.WriteFile(envFile, []byte("KEY=value\n"), 0644))

	t.Setenv("CLAWKER_TEST_DIR", dir)

	agent := AgentConfig{
		EnvFile: []string{"$CLAWKER_TEST_DIR/test.env"},
	}
	result, _, err := ResolveAgentEnv(agent, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"KEY": "value"}, result)
}

func TestResolveAgentEnv_EnvFileNotFound(t *testing.T) {
	agent := AgentConfig{
		EnvFile: []string{"/nonexistent/path/to/file.env"},
	}
	_, _, err := ResolveAgentEnv(agent, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.env_file")
}

func TestResolveAgentEnv_MultipleEnvFiles(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "base.env")
	file2 := filepath.Join(dir, "override.env")
	require.NoError(t, os.WriteFile(file1, []byte("A=1\nB=2\n"), 0644))
	require.NoError(t, os.WriteFile(file2, []byte("B=overridden\nC=3\n"), 0644))

	agent := AgentConfig{
		EnvFile: []string{file1, file2},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"A": "1", "B": "overridden", "C": "3"}, result)
}

func TestResolveAgentEnv_FromEnv(t *testing.T) {
	t.Setenv("MY_API_KEY", "secret123")
	t.Setenv("MY_TOKEN", "tok456")

	agent := AgentConfig{
		FromEnv: []string{"MY_API_KEY", "MY_TOKEN"},
	}
	result, _, err := ResolveAgentEnv(agent, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"MY_API_KEY": "secret123", "MY_TOKEN": "tok456"}, result)
}

func TestResolveAgentEnv_FromEnvUnset(t *testing.T) {
	// Ensure the var is not set
	t.Setenv("CLAWKER_EXISTING", "exists")

	agent := AgentConfig{
		FromEnv: []string{"CLAWKER_EXISTING", "CLAWKER_NONEXISTENT_VAR_12345"},
	}
	result, warnings, err := ResolveAgentEnv(agent, t.TempDir())
	require.NoError(t, err)
	// Only the set variable should be included
	assert.Equal(t, map[string]string{"CLAWKER_EXISTING": "exists"}, result)
	// Unset var should produce a warning
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "CLAWKER_NONEXISTENT_VAR_12345")
	assert.Contains(t, warnings[0], "not set")
}

func TestResolveAgentEnv_FromEnvEmptyString(t *testing.T) {
	t.Setenv("EMPTY_VAR", "")
	agent := AgentConfig{FromEnv: []string{"EMPTY_VAR"}}
	result, warnings, err := ResolveAgentEnv(agent, t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, "", result["EMPTY_VAR"]) // set-but-empty IS included
}

func TestResolveAgentEnv_Precedence(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("SHARED=from-file\nFILE_ONLY=file-val\n"), 0644))

	t.Setenv("SHARED", "from-host")
	t.Setenv("HOST_ONLY", "host-val")

	agent := AgentConfig{
		EnvFile: []string{envFile},
		FromEnv: []string{"SHARED", "HOST_ONLY"},
		Env:     map[string]string{"SHARED": "from-static", "STATIC_ONLY": "static-val"},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)

	// env_file < from_env < env
	assert.Equal(t, "from-static", result["SHARED"], "static env should win")
	assert.Equal(t, "file-val", result["FILE_ONLY"], "file-only var should be present")
	assert.Equal(t, "host-val", result["HOST_ONLY"], "host-only var should be present")
	assert.Equal(t, "static-val", result["STATIC_ONLY"], "static-only var should be present")
}

func TestResolveAgentEnv_FromEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("KEY=from-file\n"), 0644))

	t.Setenv("KEY", "from-host")

	agent := AgentConfig{
		EnvFile: []string{envFile},
		FromEnv: []string{"KEY"},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)
	assert.Equal(t, "from-host", result["KEY"], "from_env should override env_file")
}

func TestResolveAgentEnv_EnvOverridesAll(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("KEY=from-file\n"), 0644))

	t.Setenv("KEY", "from-host")

	agent := AgentConfig{
		EnvFile: []string{envFile},
		FromEnv: []string{"KEY"},
		Env:     map[string]string{"KEY": "from-static"},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)
	assert.Equal(t, "from-static", result["KEY"], "static env should win over all")
}

func TestResolveAgentEnv_EnvFileValueWithEquals(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("CONN=host=localhost;port=5432\n"), 0644))

	agent := AgentConfig{
		EnvFile: []string{envFile},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)
	assert.Equal(t, "host=localhost;port=5432", result["CONN"])
}

func TestResolveAgentEnv_EnvFileBareKey(t *testing.T) {
	// A bare KEY line (no =) in an env file should set the key with empty value
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("BARE_KEY\nFOO=bar\n"), 0644))

	agent := AgentConfig{
		EnvFile: []string{envFile},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)
	assert.Equal(t, "", result["BARE_KEY"])
	assert.Equal(t, "bar", result["FOO"])
}

func TestResolveAgentEnv_EnvFileEmptyKey(t *testing.T) {
	// A line starting with = should be skipped (empty key)
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("=empty-key-value\nFOO=bar\n"), 0644))

	agent := AgentConfig{
		EnvFile: []string{envFile},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"FOO": "bar"}, result)
	assert.NotContains(t, result, "")
}

func TestResolveAgentEnv_EnvFileWhitespaceAroundEquals(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("KEY = value\n SPACED =val2\n"), 0644))

	agent := AgentConfig{
		EnvFile: []string{envFile},
	}
	result, _, err := ResolveAgentEnv(agent, dir)
	require.NoError(t, err)
	// Document actual behavior: whitespace is part of key/value (no trimming around =).
	// "KEY " = " value" — this matches Docker's behavior.
	assert.Contains(t, result, "KEY ")
}

func TestExpandPath_Absolute(t *testing.T) {
	result, err := expandPath("/usr/local/bin")
	require.NoError(t, err)
	assert.Equal(t, "/usr/local/bin", result)
}

func TestExpandPath_Tilde(t *testing.T) {
	fakeHome := t.TempDir()
	userHomeDir = func() (string, error) { return fakeHome, nil }
	t.Cleanup(func() { userHomeDir = os.UserHomeDir })

	result, err := expandPath("~/.config")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(fakeHome, ".config"), result)
}

func TestExpandPath_TildeOnly(t *testing.T) {
	fakeHome := t.TempDir()
	userHomeDir = func() (string, error) { return fakeHome, nil }
	t.Cleanup(func() { userHomeDir = os.UserHomeDir })

	result, err := expandPath("~")
	require.NoError(t, err)
	assert.Equal(t, fakeHome, result)
}

func TestExpandPath_EnvVar(t *testing.T) {
	t.Setenv("MY_DIR", "/opt/custom")
	result, err := expandPath("$MY_DIR/file.env")
	require.NoError(t, err)
	assert.Equal(t, "/opt/custom/file.env", result)
}

func TestExpandPath_EnvVarBraces(t *testing.T) {
	t.Setenv("MY_DIR", "/opt/custom")
	result, err := expandPath("${MY_DIR}/file.env")
	require.NoError(t, err)
	assert.Equal(t, "/opt/custom/file.env", result)
}

func TestExpandPath_Relative(t *testing.T) {
	// Relative paths should pass through unchanged
	result, err := expandPath("relative/path.env")
	require.NoError(t, err)
	assert.Equal(t, "relative/path.env", result)
}

func TestExpandPath_UnsetEnvVar(t *testing.T) {
	_, err := expandPath("$NONEXISTENT_VAR_12345/file.env")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not set")
	assert.Contains(t, err.Error(), "NONEXISTENT_VAR_12345")
}

func TestExpandPath_HomeDirError(t *testing.T) {
	userHomeDir = func() (string, error) { return "", fmt.Errorf("no home") }
	t.Cleanup(func() { userHomeDir = os.UserHomeDir })

	_, err := expandPath("~/config")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no home")
}
