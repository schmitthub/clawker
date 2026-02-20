package config

import (
	"os"

	"github.com/spf13/viper"
)

type ConfigImplForTest = configImpl
type LoadOptionsForTest = loadOptions

const (
	ClawkerConfigFileNameForTest   = clawkerConfigFileName
	ClawkerSettingsFileNameForTest = clawkerSettingsFileName
	ClawkerProjectsFileNameForTest = clawkerProjectsFileName
	ClawkerConfigDirEnvForTest     = clawkerConfigDirEnv
	ClawkerDataDirEnvForTest       = clawkerDataDirEnv
	ClawkerStateDirEnvForTest      = clawkerStateDirEnv
	XDGConfigHomeForTest           = xdgConfigHome
	AppDataForTest                 = appData
)

func NewLoadOptionsForTest(settingsFile, userProjectConfigFile, projectRegistryPath string) LoadOptionsForTest {
	return loadOptions{
		settingsFile:          settingsFile,
		userProjectConfigFile: userProjectConfigFile,
		projectRegistryPath:   projectRegistryPath,
	}
}

func NewViperConfigForTest() *viper.Viper {
	return newViperConfig()
}

func NewConfigWithViperForTest(v *viper.Viper) *configImpl {
	return newConfig(v)
}

func (c *configImpl) SetFilePathsForTest(settingsFile, userProjectConfigFile, projectRegistryPath string) {
	c.settingsFile = settingsFile
	c.userProjectConfigFile = userProjectConfigFile
	c.projectRegistryPath = projectRegistryPath
}

func (c *configImpl) SetProjectRegistryPathForTest(projectRegistryPath string) {
	c.projectRegistryPath = projectRegistryPath
}

func (c *configImpl) LoadForTest(opts loadOptions) error {
	return c.load(opts)
}

func (c *configImpl) MergeProjectConfigForTest() error {
	return c.mergeProjectConfig()
}

func ExportAtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	return atomicWriteFile(path, data, perm)
}

func ExportWithFileLock(path string, fn func() error) error {
	return withFileLock(path, fn)
}
