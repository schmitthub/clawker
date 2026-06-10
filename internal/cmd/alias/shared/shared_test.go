package shared

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/storage"
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

	newCfg := func(t *testing.T, dirs ...string) config.Config {
		t.Helper()
		opts := []storage.Option{storage.WithFilenames("clawker.local.yaml", "clawker.yaml")}
		if len(dirs) > 0 {
			opts = append(opts, storage.WithDirs(dirs...))
		}
		opts = append(opts, storage.WithConfigDir())
		store, err := storage.New[config.Project]("", opts...)
		require.NoError(t, err)
		mock := configmocks.NewBlankConfig()
		mock.ProjectStoreFunc = func() *storage.Store[config.Project] { return store }
		return mock
	}

	t.Run("prefers shared project file over local variant", func(t *testing.T) {
		proj := t.TempDir()
		shared := write(t, proj, ".clawker.yaml")
		write(t, proj, ".clawker.local.yaml")

		got, err := ExportTarget(newCfg(t, proj))
		require.NoError(t, err)
		assert.Equal(t, shared, got)
	})

	t.Run("only local variant present errors", func(t *testing.T) {
		proj := t.TempDir()
		write(t, proj, ".clawker.local.yaml")

		_, err := ExportTarget(newCfg(t, proj))
		assert.ErrorContains(t, err, "no shared project config")
	})

	t.Run("user-level config-dir file is not a target", func(t *testing.T) {
		write(t, configDir, "clawker.yaml")

		_, err := ExportTarget(newCfg(t))
		assert.ErrorContains(t, err, "no shared project config")
	})
}
