package config

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

// Type aliases — expose private types for external tests.
type ConfigImplForTest = configImpl
type DirtyNodeForTest = dirtyNode
type LoadOptionsForTest = loadOptions

// Const aliases — expose private constants for external tests.
const (
	ClawkerConfigFileNameForTest   = clawkerProjectConfigFileName
	ClawkerSettingsFileNameForTest = clawkerSettingsFileName
	ClawkerProjectsFileNameForTest = clawkerProjectsFileName
	ClawkerConfigDirEnvForTest     = clawkerConfigDirEnv
	ClawkerDataDirEnvForTest       = clawkerDataDirEnv
	ClawkerStateDirEnvForTest      = clawkerStateDirEnv
	XDGConfigHomeForTest           = xdgConfigHome
	AppDataForTest                 = appData
)

// Var aliases — expose private functions for external tests.
var NewViperConfigForTest = newViperConfig
var NewConfigForTest = newConfig

// Passthrough functions — expose private operations for external tests.

func NewConfigImplForTest(v *viper.Viper, settingsFile, userProjectConfigFile, projectRegistryPath, projectConfigFile string, dirty *dirtyNode) *configImpl {
	return &configImpl{
		v:                     v,
		settingsFile:          settingsFile,
		userProjectConfigFile: userProjectConfigFile,
		projectRegistryPath:   projectRegistryPath,
		projectConfigFile:     projectConfigFile,
		dirty:                 dirty,
	}
}

func SetProjectRegistryPathForTest(c *configImpl, path string) {
	c.projectRegistryPath = path
}

func SetFilePathsForTest(c *configImpl, settingsFile, userProjectConfigFile, projectRegistryPath string) {
	c.settingsFile = settingsFile
	c.userProjectConfigFile = userProjectConfigFile
	c.projectRegistryPath = projectRegistryPath
}

func LoadForTest(c *configImpl, settingsFile, userProjectConfigFile, projectRegistryPath string) error {
	return c.load(loadOptions{
		settingsFile:          settingsFile,
		userProjectConfigFile: userProjectConfigFile,
		projectRegistryPath:   projectRegistryPath,
	})
}

func MergeProjectConfigForTest(c *configImpl) error {
	return c.mergeProjectConfig()
}

func AtomicWriteFileForTest(path string, data []byte, perm os.FileMode) error {
	return atomicWriteFile(path, data, perm)
}

func WithFileLockForTest(path string, fn func() error) error {
	return withFileLock(path, fn)
}

func ValidateYAMLStrictForTest(yamlContent string, schema any) error {
	return validateYAMLStrict(yamlContent, schema)
}

func ValidateConfigFileExactForTest(path string, schema any) error {
	return validateConfigFileExact(path, schema)
}

func NamespacedKeyForTest(flat string) (string, error) {
	return namespacedKey(flat)
}

func NamespaceMapForTest(flat map[string]any, scope ConfigScope) map[string]any {
	return namespaceMap(flat, scope)
}

func ScopeFromNamespacedKeyForTest(key string) (ConfigScope, error) {
	return scopeFromNamespacedKey(key)
}

func ScopeForKeyForTest(key string) (ConfigScope, error) {
	return scopeForKey(key)
}

var KeyOwnershipForTest = keyOwnership

// NewIsolatedTestConfig creates a blank config of a real configImpl
// in the real implementation, sets the clawker dir envs var so that
// any call to Write goes to a different location on disk, and then returns
// the blank config and a function that reads any data written to disk.
// TODO: add mock keyring
func NewIsolatedTestConfig(t *testing.T) (*ConfigImplForTest, func(io.Writer, io.Writer, io.Writer, io.Writer)) {
	c, err := ReadFromString("")
	if err != nil {
		t.Fatalf("creating blank config for isolated test: %v", err)
	}
	impl, ok := c.(*ConfigImplForTest)
	if !ok {
		t.Fatalf("unexpected config type: %T", c)
	}
	readConfigs := StubWriteConfig(t, impl)

	return impl, readConfigs
}

// StubWriteConfig isolates config-file writes to a temp config directory and returns
// a reader callback for settings, user project config, repo project config, and project registry content.
func StubWriteConfig(t *testing.T, cfg *ConfigImplForTest) func(io.Writer, io.Writer, io.Writer, io.Writer) {
	t.Helper()
	base := t.TempDir()

	configDir := filepath.Join(base, "config")
	dataDir := filepath.Join(base, "data")
	stateDir := filepath.Join(base, "state")
	testRepoDir := filepath.Join(base, "testrepo")

	t.Setenv(cfg.ConfigDirEnvVar(), configDir)
	t.Setenv(cfg.DataDirEnvVar(), dataDir)
	t.Setenv(cfg.StateDirEnvVar(), stateDir)
	t.Setenv(cfg.TestRepoDirEnvVar(), testRepoDir)

	err := os.MkdirAll(configDir, 0o755)
	if err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	err = os.MkdirAll(dataDir, 0o755)
	if err != nil {
		t.Fatalf("creating data dir: %v", err)
	}
	err = os.MkdirAll(stateDir, 0o755)
	if err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	err = os.MkdirAll(testRepoDir, 0o755)
	if err != nil {
		t.Fatalf("creating testrepo dir: %v", err)
	}

	settingsFileName := cfg.SettingsFileName()
	userProjectFileName := cfg.ProjectConfigFileName()
	repoProjectFileName := cfg.ProjectConfigFileName()
	projectRegistryFileName := cfg.ProjectRegistryFileName()

	return func(settingsOut io.Writer, userProjectOut io.Writer, repoProjectOut io.Writer, registryOut io.Writer) {
		copyFile := func(path string, out io.Writer) {
			f, err := os.Open(path)
			if err != nil {
				return // file not written, skip silently
			}
			defer f.Close()
			io.Copy(out, f)
		}

		copyFile(filepath.Join(configDir, settingsFileName), settingsOut)
		copyFile(filepath.Join(configDir, userProjectFileName), userProjectOut)
		copyFile(filepath.Join(testRepoDir, repoProjectFileName), repoProjectOut)
		copyFile(filepath.Join(configDir, projectRegistryFileName), registryOut)

	}
}
