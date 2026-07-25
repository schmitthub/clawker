package shared

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateName(t *testing.T) {
	assert.NoError(t, ValidateName("co"))
	assert.NoError(t, ValidateName("my-alias_2"))
	assert.Error(t, ValidateName(""))
	assert.Error(t, ValidateName("  "))
	assert.Error(t, ValidateName("two words"))
	assert.Error(t, ValidateName(" padded"))
	assert.Error(t, ValidateName("-flagish"))
	// Dots are addressed as one key segment ({"aliases", "a.b"}), never
	// reparsed as nesting, so a dotted name is a legal single-word alias.
	assert.NoError(t, ValidateName("a.b"))
	assert.NoError(t, ValidateName("a.b.c"))
}

func TestSplitExpansion(t *testing.T) {
	tokens, err := SplitExpansion(`container run --rm "a b"`)
	require.NoError(t, err)
	assert.Equal(t, []string{"container", "run", "--rm", "a b"}, tokens)

	_, err = SplitExpansion("")
	assert.Error(t, err)
	_, err = SplitExpansion("   ")
	assert.Error(t, err)
	_, err = SplitExpansion(`broken "quote`)
	assert.Error(t, err)
}

func TestValidateExpansionTarget(t *testing.T) {
	validCommand := func(name string) bool { return name == "run" || name == "version" }
	aliases := map[string]string{"existing": "version"}

	assert.NoError(t, ValidateExpansionTarget("x", "run --rm", validCommand, aliases))
	assert.NoError(t, ValidateExpansionTarget("x", "existing --flag", validCommand, aliases),
		"chaining onto another alias is allowed")

	assert.ErrorContains(t, ValidateExpansionTarget("x", "x foo", validCommand, aliases), "reference itself")
	assert.ErrorContains(t, ValidateExpansionTarget("x", "nosuch foo", validCommand, aliases), "not a clawker command")
	assert.Error(t, ValidateExpansionTarget("x", "", validCommand, aliases))
}

func TestExportTarget(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAWKER_CONFIG_DIR", configDir)

	write := func(t *testing.T, dir, name string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte("aliases:\n  a: version\n"), 0o644))
		return path
	}

	// newCfg loads a real config from projectDir the way a CLI run inside
	// that directory would: walk-up over the project dir, then the config dir.
	newCfg := func(t *testing.T, projectDir string) config.Config {
		t.Helper()
		t.Chdir(projectDir)
		cfg, err := config.NewConfig(config.WithProjectRoot(projectDir))
		require.NoError(t, err)
		return cfg
	}

	t.Run("most local highest-priority file wins, local variant included", func(t *testing.T) {
		proj := t.TempDir()
		write(t, proj, ".clawker.yaml")
		local := write(t, proj, ".clawker.local.yaml")

		got, err := ExportTarget(newCfg(t, proj))
		require.NoError(t, err)
		assert.Equal(t, local, got)
	})

	t.Run("only local variant present is a valid target", func(t *testing.T) {
		proj := t.TempDir()
		local := write(t, proj, ".clawker.local.yaml")

		got, err := ExportTarget(newCfg(t, proj))
		require.NoError(t, err)
		assert.Equal(t, local, got)
	})

	t.Run("user-level config-dir file is not a target", func(t *testing.T) {
		write(t, configDir, "clawker.yaml")

		_, err := ExportTarget(newCfg(t, t.TempDir()))
		assert.ErrorContains(t, err, "no project config found")
	})
}
