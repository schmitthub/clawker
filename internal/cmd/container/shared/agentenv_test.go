package shared_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// envAgentCfg builds an AgentConfig carrying only the env spec under test.
func envAgentCfg(envFile, fromEnv []string, env map[string]string) config.AgentConfig {
	return config.AgentConfig{
		EnvFile:         envFile,
		FromEnv:         fromEnv,
		Env:             env,
		Editor:          "",
		Visual:          "",
		ClaudeCode:      nil,
		EnableSharedDir: nil,
		PostInit:        "",
		PreRun:          "",
	}
}

// envHarnessCfg builds a HarnessConfig carrying only the env spec under test.
func envHarnessCfg(envFile, fromEnv []string, env map[string]string) *config.HarnessConfig {
	return &config.HarnessConfig{
		Config:        config.HarnessConfigOptions{Strategy: ""},
		MountProjects: nil,
		EnvFile:       envFile,
		FromEnv:       fromEnv,
		Env:           env,
		PostInit:      "",
		PreRun:        "",
	}
}

// TestResolveAgentEnv_HarnessLayering pins the two-spec composition contract:
// the harness spec layers over the agent base (harness wins on collision),
// each spec keeps its internal env_file < from_env < env precedence, and a
// nil harness config applies the base only.
func TestResolveAgentEnv_HarnessLayering(t *testing.T) {
	log := logger.Nop()

	t.Run("harness env overrides agent env on collision", func(t *testing.T) {
		agent := envAgentCfg(nil, nil, map[string]string{"SHARED": "agent", "BASE_ONLY": "base"})
		harness := envHarnessCfg(nil, nil, map[string]string{"SHARED": "harness", "HARNESS_ONLY": "extra"})

		got, warnings, err := shared.ResolveAgentEnv(agent, harness, "codex", t.TempDir(), log)
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.Equal(t, map[string]string{
			"SHARED":       "harness",
			"BASE_ONLY":    "base",
			"HARNESS_ONLY": "extra",
		}, got)
	})

	t.Run("agent env_file is the lowest base layer", func(t *testing.T) {
		dir := t.TempDir()
		envPath := filepath.Join(dir, ".env")
		require.NoError(t, os.WriteFile(envPath, []byte("KEY=file\nFILE_ONLY=kept\n"), 0o600))

		agent := envAgentCfg([]string{".env"}, nil, map[string]string{"KEY": "env"})

		got, _, err := shared.ResolveAgentEnv(agent, nil, "claude", dir, log)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"KEY": "env", "FILE_ONLY": "kept"}, got)
	})

	t.Run("nil harness config applies base spec only", func(t *testing.T) {
		agent := envAgentCfg(nil, nil, map[string]string{"BASE": "v"})

		got, warnings, err := shared.ResolveAgentEnv(agent, nil, "codex", t.TempDir(), log)
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.Equal(t, map[string]string{"BASE": "v"}, got)
	})

	t.Run("harness env_file layers over agent env", func(t *testing.T) {
		dir := t.TempDir()
		envPath := filepath.Join(dir, ".env.codex")
		require.NoError(t, os.WriteFile(envPath, []byte("SHARED=from-file\n"), 0o600))

		agent := envAgentCfg(nil, nil, map[string]string{"SHARED": "agent"})
		harness := envHarnessCfg([]string{".env.codex"}, nil, nil)

		got, _, err := shared.ResolveAgentEnv(agent, harness, "codex", dir, log)
		require.NoError(t, err)
		assert.Equal(t, "from-file", got["SHARED"])
	})

	t.Run("harness env beats harness env_file within the spec", func(t *testing.T) {
		dir := t.TempDir()
		envPath := filepath.Join(dir, ".env.codex")
		require.NoError(t, os.WriteFile(envPath, []byte("KEY=file\n"), 0o600))

		harness := envHarnessCfg([]string{".env.codex"}, nil, map[string]string{"KEY": "explicit"})

		got, _, err := shared.ResolveAgentEnv(envAgentCfg(nil, nil, nil), harness, "codex", dir, log)
		require.NoError(t, err)
		assert.Equal(t, "explicit", got["KEY"])
	})

	t.Run("warnings and errors carry the harness scope", func(t *testing.T) {
		harness := envHarnessCfg(nil, []string{"CLAWKER_TEST_DEFINITELY_UNSET_VAR"}, nil)

		_, warnings, err := shared.ResolveAgentEnv(envAgentCfg(nil, nil, nil), harness, "codex", t.TempDir(), log)
		require.NoError(t, err)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "harnesses.codex.from_env")

		badFile := envHarnessCfg([]string{"does-not-exist.env"}, nil, nil)
		_, _, err = shared.ResolveAgentEnv(envAgentCfg(nil, nil, nil), badFile, "codex", t.TempDir(), log)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "harnesses.codex.env_file")
	})

	t.Run("agent-scope diagnostics unchanged", func(t *testing.T) {
		agent := envAgentCfg(nil, []string{"CLAWKER_TEST_DEFINITELY_UNSET_VAR"}, nil)

		_, warnings, err := shared.ResolveAgentEnv(agent, nil, "claude", t.TempDir(), log)
		require.NoError(t, err)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "agent.from_env")
	})
}

// resolveEnvFile writes content to a .env file in an isolated temp dir and
// resolves it through ResolveAgentEnv — the only public entry point over the
// env-file parser. Returns the resolved env map.
func resolveEnvFile(t *testing.T, content string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600))

	agent := envAgentCfg([]string{".env"}, nil, nil)
	got, _, err := shared.ResolveAgentEnv(agent, nil, "claude", dir, logger.Nop())
	require.NoError(t, err)
	if got == nil {
		got = map[string]string{}
	}
	return got
}

// TestResolveAgentEnv_EnvFileDotenvSemantics pins the .env parsing contract.
// The config schema documents env_file as ".env-style files", which users read
// as standard dotenv semantics (docker compose, via compose-go): surrounding
// quotes are syntax, not value; single quotes are literal; double quotes
// process escapes; `export` prefixes and inline comments are tolerated.
func TestResolveAgentEnv_EnvFileDotenvSemantics(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    map[string]string
	}{
		{
			name:    "unquoted value verbatim",
			content: "KEY=value\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "double-quoted value strips quotes",
			content: `KEY="quoted value"` + "\n",
			want:    map[string]string{"KEY": "quoted value"},
		},
		{
			name:    "double-quoted value with escaped inner quotes",
			content: `KEY="he said \"hi\" ok"` + "\n",
			want:    map[string]string{"KEY": `he said "hi" ok`},
		},
		{
			name:    "escaped quote at end of double-quoted value",
			content: `KEY="he said \"hi\""` + "\n",
			want:    map[string]string{"KEY": `he said "hi"`},
		},
		{
			name:    "hash without preceding space is not a comment",
			content: "KEY=value#notcomment\n",
			want:    map[string]string{"KEY": "value#notcomment"},
		},
		{
			name:    "single-quoted value strips quotes",
			content: "KEY='quoted value'\n",
			want:    map[string]string{"KEY": "quoted value"},
		},
		{
			name:    "single-quoted value is literal (no escape or dollar processing)",
			content: `KEY='$HOME \n literal'` + "\n",
			want:    map[string]string{"KEY": `$HOME \n literal`},
		},
		{
			name:    "double-quoted value expands backslash escapes",
			content: `KEY="line1\nline2"` + "\n",
			want:    map[string]string{"KEY": "line1\nline2"},
		},
		{
			name:    "export prefix is stripped",
			content: "export KEY=value\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "inline comment after unquoted value",
			content: "KEY=value # trailing comment\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "hash inside double-quoted value is not a comment",
			content: `KEY="value # kept"` + "\n",
			want:    map[string]string{"KEY": "value # kept"},
		},
		{
			name:    "whitespace around equals is trimmed",
			content: "KEY = value\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "empty value",
			content: "KEY=\n",
			want:    map[string]string{"KEY": ""},
		},
		{
			name:    "empty double-quoted value",
			content: `KEY=""` + "\n",
			want:    map[string]string{"KEY": ""},
		},
		{
			name:    "value containing equals sign",
			content: "KEY=a=b=c\n",
			want:    map[string]string{"KEY": "a=b=c"},
		},
		{
			name:    "unquoted value with internal spaces",
			content: "KEY=one two three\n",
			want:    map[string]string{"KEY": "one two three"},
		},
		{
			name:    "CRLF line endings",
			content: "KEY=value\r\nOTHER=x\r\n",
			want:    map[string]string{"KEY": "value", "OTHER": "x"},
		},
		{
			name:    "comments and blank lines skipped",
			content: "# header comment\n\nKEY=value\n   # indented comment\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "url value unquoted survives",
			content: "URL=https://example.com/path?a=1&b=2\n",
			want:    map[string]string{"URL": "https://example.com/path?a=1&b=2"},
		},
		{
			name:    "json value in single quotes survives verbatim",
			content: `CONFIG='{"key": "value", "n": 1}'` + "\n",
			want:    map[string]string{"CONFIG": `{"key": "value", "n": 1}`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveEnvFile(t, tt.content))
		})
	}
}

// TestResolveAgentEnv_EnvFileOrdering pins that later env_file entries win on
// key collision (list order = precedence order within the layer).
func TestResolveAgentEnv_EnvFileOrdering(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.env"), []byte("KEY=first\nA_ONLY=a\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.env"), []byte("KEY=second\nB_ONLY=b\n"), 0o600))

	agent := envAgentCfg([]string{"a.env", "b.env"}, nil, nil)
	got, _, err := shared.ResolveAgentEnv(agent, nil, "claude", dir, logger.Nop())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"KEY": "second", "A_ONLY": "a", "B_ONLY": "b"}, got)
}

// TestResolveAgentEnv_EnvFileExpansion pins $VAR expansion semantics
// (compose-go dotenv, host-side at parse time): references resolve from the
// host OS environment first, then from earlier keys in the same file; a
// reference unset in both collapses to empty. Compose interpolation
// operators (${VAR:-default}, ${VAR:?required}) work, and single quotes
// suppress expansion.
func TestResolveAgentEnv_EnvFileExpansion(t *testing.T) {
	t.Setenv("CLAWKER_TEST_ENVFILE_HOST_VAR", "from-host")
	t.Setenv("CLAWKER_TEST_ENVFILE_SHADOWED", "host-wins")

	got := resolveEnvFile(t,
		"BASE=file-local\n"+
			`SAME_FILE="prefix $BASE"`+"\n"+
			"CLAWKER_TEST_ENVFILE_SHADOWED=file-value\n"+
			`SHADOW_REF="$CLAWKER_TEST_ENVFILE_SHADOWED"`+"\n"+
			"UNQUOTED=prefix-$BASE\n"+
			`HOST="prefix $CLAWKER_TEST_ENVFILE_HOST_VAR"`+"\n"+
			`UNSET="prefix $CLAWKER_TEST_ENVFILE_DEFINITELY_UNSET"`+"\n"+
			`DEFAULTED=${CLAWKER_TEST_ENVFILE_DEFINITELY_UNSET:-fallback}`+"\n"+
			`LITERAL='$BASE'`+"\n")

	assert.Equal(t, map[string]string{
		"BASE":      "file-local",
		"SAME_FILE": "prefix file-local",
		// Interpolation consults the host lookup BEFORE file-local keys
		// (compose-go expandVariables order): when a referenced name is set
		// in both, the host value shadows the file's — but the key itself
		// still resolves to the file value.
		"CLAWKER_TEST_ENVFILE_SHADOWED": "file-value",
		"SHADOW_REF":                    "host-wins",
		"UNQUOTED":                      "prefix-file-local",
		"HOST":                          "prefix from-host",
		"UNSET":                         "prefix ",
		"DEFAULTED":                     "fallback",
		"LITERAL":                       "$BASE",
	}, got)
}

// TestResolveAgentEnv_EnvFileRequiredVarError pins that the compose
// ${VAR:?message} required-variable operator surfaces as an error carrying
// the env_file scope so the user can find the offending file.
func TestResolveAgentEnv_EnvFileRequiredVarError(t *testing.T) {
	dir := t.TempDir()
	content := "KEY=${CLAWKER_TEST_ENVFILE_DEFINITELY_UNSET:?must be set}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600))

	agent := envAgentCfg([]string{".env"}, nil, nil)
	_, _, err := shared.ResolveAgentEnv(agent, nil, "claude", dir, logger.Nop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.env_file")
	assert.Contains(t, err.Error(), "must be set")
}

// TestResolveAgentEnv_EnvFileBareKeyPassthrough pins docker --env-file
// passthrough semantics for a bare `KEY` line (no separator): the value is
// inherited from the host environment, and the line is skipped silently when
// the host variable is unset.
func TestResolveAgentEnv_EnvFileBareKeyPassthrough(t *testing.T) {
	t.Setenv("CLAWKER_TEST_ENVFILE_BARE_KEY", "inherited")

	got := resolveEnvFile(t,
		"CLAWKER_TEST_ENVFILE_BARE_KEY\n"+
			"CLAWKER_TEST_ENVFILE_DEFINITELY_UNSET\n"+
			"OTHER=x\n")

	assert.Equal(t, map[string]string{
		"CLAWKER_TEST_ENVFILE_BARE_KEY": "inherited",
		"OTHER":                         "x",
	}, got)
}

func TestResolveAgentEnv_EnvFileBareKeyAtEOFNoNewline(t *testing.T) {
	t.Setenv("CLAWKER_TEST_ENVFILE_BARE_EOF", "inherited")

	// No trailing newline: bare inherited key terminated by EOF must still
	// inherit, not degrade into an empty-key entry.
	got := resolveEnvFile(t, "OTHER=x\nCLAWKER_TEST_ENVFILE_BARE_EOF")

	assert.Equal(t, map[string]string{
		"CLAWKER_TEST_ENVFILE_BARE_EOF": "inherited",
		"OTHER":                         "x",
	}, got)
}

// TestResolveAgentEnv_NoImplicitEnvFileLoad pins that a .env file in the
// project dir is loaded ONLY when env_file names it. compose-go's higher-level
// APIs auto-load .env from the working directory — clawker must never do that
// implicitly.
func TestResolveAgentEnv_NoImplicitEnvFileLoad(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("SHOULD_NOT_LOAD=leaked\n"), 0o600))

	agent := envAgentCfg(nil, nil, map[string]string{"EXPLICIT": "v"})
	got, warnings, err := shared.ResolveAgentEnv(agent, nil, "claude", dir, logger.Nop())
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, map[string]string{"EXPLICIT": "v"}, got)
}

// TestResolveAgentEnv_EnvFileUnsetVarWarning pins that an env-file reference
// to a variable unset in both the file and the host environment surfaces
// through the warnings return (scoped, like from_env warnings) — never
// through third-party logger output. Reporting is precise: a reference
// rescued by a ${VAR:-default} operator does NOT warn.
func TestResolveAgentEnv_EnvFileUnsetVarWarning(t *testing.T) {
	dir := t.TempDir()
	content := `KEY="prefix $CLAWKER_TEST_ENVFILE_DEFINITELY_UNSET"` + "\n" +
		"DEFAULTED=${CLAWKER_TEST_ENVFILE_ALSO_UNSET:-fallback}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600))

	agent := envAgentCfg([]string{".env"}, nil, nil)
	got, warnings, err := shared.ResolveAgentEnv(agent, nil, "claude", dir, logger.Nop())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"KEY": "prefix ", "DEFAULTED": "fallback"}, got)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], `agent.env_file ".env"`)
	assert.Contains(t, warnings[0], "CLAWKER_TEST_ENVFILE_DEFINITELY_UNSET")
}
