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
	t.Setenv(cfg.ConfigDirEnvVar(), filepath.Join(base, "config"))
	t.Setenv(cfg.DataDirEnvVar(), filepath.Join(base, "data"))
	t.Setenv(cfg.StateDirEnvVar(), filepath.Join(base, "state"))

	settingsFileName := cfg.SettingsFileName()
	userProjectFileName := cfg.ProjectConfigFileName()
	repoProjectFileName := cfg.ProjectConfigFileName()
	projectRegistryFileName := cfg.ProjectRegistryFileName()

	return func(settingsOut io.Writer, userProjectOut io.Writer, repoProjectOut io.Writer, registryOut io.Writer) {
		settings, err := os.Open(filepath.Join(base, settingsFileName))
		if err != nil {
			return
		}
		defer settings.Close()
		settingsData, err := io.ReadAll(settings)
		if err != nil {
			return
		}
		_, err = settingsOut.Write(settingsData)
		if err != nil {
			return
		}

		userProject, err := os.Open(filepath.Join(base, userProjectFileName))
		if err != nil {
			return
		}
		defer userProject.Close()
		userProjectData, err := io.ReadAll(userProject)
		if err != nil {
			return
		}
		_, err = userProjectOut.Write(userProjectData)
		if err != nil {
			return
		}

		repoProject, err := os.Open(filepath.Join(base, repoProjectFileName))
		if err != nil {
			return
		}
		defer repoProject.Close()
		repoProjectData, err := io.ReadAll(repoProject)
		if err != nil {
			return
		}
		_, err = repoProjectOut.Write(repoProjectData)
		if err != nil {
			return
		}

		registry, err := os.Open(filepath.Join(base, projectRegistryFileName))
		if err != nil {
			return
		}
		defer registry.Close()
		registryData, err := io.ReadAll(registry)
		if err != nil {
			return
		}
		_, err = registryOut.Write(registryData)
		if err != nil {
			return
		}

	}
}
