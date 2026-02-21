package config

import (
	"os"

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
