package config

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// NewBlankConfig returns an in-memory *ConfigMock seeded with defaults.
// It is the default test double for consumers that don't care about specific config values.
func NewBlankConfig() *ConfigMock {
	return NewFromString("")
}

// NewFromString creates an in-memory *ConfigMock from YAML.
// All read methods delegate to a real config backed by defaults + the provided YAML.
// Set, Write, and Watch are not wired — calling them panics, signaling the wrong test double.
// Use NewIsolatedTestConfig for tests that need mutation or file-backed config.
// Panics on invalid YAML to match test-stub ergonomics.
func NewFromString(cfgStr string) *ConfigMock {
	cfg, err := ReadFromString(cfgStr)
	if err != nil {
		panic(err)
	}

	mock := &ConfigMock{}

	// Schema accessors
	mock.ProjectFunc = cfg.Project
	mock.SettingsFunc = cfg.Settings
	mock.LoggingFunc = cfg.Logging
	mock.LoggingConfigFunc = cfg.LoggingConfig
	mock.MonitoringConfigFunc = cfg.MonitoringConfig
	mock.HostProxyConfigFunc = cfg.HostProxyConfig
	mock.GetFunc = cfg.Get
	mock.RequiredFirewallDomainsFunc = cfg.RequiredFirewallDomains
	mock.GetProjectRootFunc = cfg.GetProjectRoot
	mock.GetProjectIgnoreFileFunc = cfg.GetProjectIgnoreFile

	// Constants
	mock.ClawkerIgnoreNameFunc = cfg.ClawkerIgnoreName
	mock.DomainFunc = cfg.Domain
	mock.LabelDomainFunc = cfg.LabelDomain
	mock.ConfigDirEnvVarFunc = cfg.ConfigDirEnvVar
	mock.ClawkerNetworkFunc = cfg.ClawkerNetwork
	mock.ContainerUIDFunc = cfg.ContainerUID
	mock.ContainerGIDFunc = cfg.ContainerGID
	mock.EngineLabelPrefixFunc = cfg.EngineLabelPrefix
	mock.EngineManagedLabelFunc = cfg.EngineManagedLabel
	mock.ManagedLabelValueFunc = cfg.ManagedLabelValue

	// Labels
	mock.LabelPrefixFunc = cfg.LabelPrefix
	mock.LabelManagedFunc = cfg.LabelManaged
	mock.LabelMonitoringStackFunc = cfg.LabelMonitoringStack
	mock.LabelProjectFunc = cfg.LabelProject
	mock.LabelAgentFunc = cfg.LabelAgent
	mock.LabelVersionFunc = cfg.LabelVersion
	mock.LabelImageFunc = cfg.LabelImage
	mock.LabelCreatedFunc = cfg.LabelCreated
	mock.LabelWorkdirFunc = cfg.LabelWorkdir
	mock.LabelPurposeFunc = cfg.LabelPurpose
	mock.LabelTestNameFunc = cfg.LabelTestName
	mock.LabelBaseImageFunc = cfg.LabelBaseImage
	mock.LabelFlavorFunc = cfg.LabelFlavor
	mock.LabelTestFunc = cfg.LabelTest
	mock.LabelE2ETestFunc = cfg.LabelE2ETest

	// Path helpers
	mock.MonitorSubdirFunc = cfg.MonitorSubdir
	mock.BuildSubdirFunc = cfg.BuildSubdir
	mock.DockerfilesSubdirFunc = cfg.DockerfilesSubdir
	mock.LogsSubdirFunc = cfg.LogsSubdir
	mock.BridgesSubdirFunc = cfg.BridgesSubdir
	mock.PidsSubdirFunc = cfg.PidsSubdir
	mock.BridgePIDFilePathFunc = cfg.BridgePIDFilePath
	mock.HostProxyLogFilePathFunc = cfg.HostProxyLogFilePath
	mock.HostProxyPIDFilePathFunc = cfg.HostProxyPIDFilePath
	mock.ShareSubdirFunc = cfg.ShareSubdir
	mock.WorktreesSubdirFunc = cfg.WorktreesSubdir

	// Set, Write, Watch are intentionally NOT wired.
	// Calling them panics via moq's nil-func guard, signaling
	// that NewIsolatedTestConfig should be used for mutation tests.

	return mock
}

// NewIsolatedTestConfig creates a blank file-backed config isolated to a temp config dir,
// and returns a reader callback for persisted settings/project/registry files.
func NewIsolatedTestConfig(t *testing.T) (Config, func(io.Writer, io.Writer, io.Writer)) {
	t.Helper()
	read := StubWriteConfig(t)

	cfgDir := ConfigDir()
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("creating isolated config dir %q: %v", cfgDir, err)
	}

	if err := writeIfMissing(settingsConfigFile(), []byte("{}\n")); err != nil {
		t.Fatalf("creating isolated settings file: %v", err)
	}
	if err := writeIfMissing(userProjectConfigFile(), []byte("{}\n")); err != nil {
		t.Fatalf("creating isolated user project file: %v", err)
	}
	if err := writeIfMissing(projectRegistryPath(), []byte("projects: []\n")); err != nil {
		t.Fatalf("creating isolated project registry file: %v", err)
	}

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("creating isolated test config: %v", err)
	}

	return cfg, read
}

// StubWriteConfig isolates config-file writes to a temp config directory and returns
// a reader callback for settings, user project config, and project registry content.
func StubWriteConfig(t *testing.T) func(io.Writer, io.Writer, io.Writer) {
	t.Helper()
	base := t.TempDir()
	t.Setenv(clawkerConfigDirEnv, filepath.Join(base, "config"))
	t.Setenv(clawkerDataDirEnv, filepath.Join(base, "data"))
	t.Setenv(clawkerStateDirEnv, filepath.Join(base, "state"))

	return func(settingsOut io.Writer, projectOut io.Writer, registryOut io.Writer) {
		copyFileToWriter(settingsConfigFile(), settingsOut)
		copyFileToWriter(userProjectConfigFile(), projectOut)
		copyFileToWriter(projectRegistryPath(), registryOut)
	}
}

func writeIfMissing(path string, content []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func copyFileToWriter(path string, out io.Writer) {
	if out == nil {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = io.Copy(out, file)
}
