package mocks

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
)

// NewBlankConfig returns an in-memory *ConfigMock seeded with defaults.
// It is the default test double for consumers that don't care about specific config values.
func NewBlankConfig() *ConfigMock {
	cfg, err := config.NewBlankConfig()
	if err != nil {
		panic(err)
	}
	return newMockFrom(cfg)
}

// NewFromString creates an in-memory *ConfigMock from YAML.
// All read methods delegate to a real config backed by defaults + the provided YAML.
// Panics on invalid YAML to match test-stub ergonomics.
// Set, Write, and Watch are NOT wired — calling them panics via moq's nil-func guard,
// signaling that NewIsolatedTestConfig should be used for mutation tests.
func NewFromString(cfgStr string) *ConfigMock {
	cfg, err := config.ReadFromString(cfgStr)
	if err != nil {
		panic(err)
	}
	return newMockFrom(cfg)
}

// newMockFrom wires all read-only Func fields on a ConfigMock to delegate to cfg.
// Set, Write, Watch, and path helpers are intentionally NOT wired — calling them
// panics via moq's nil-func guard, signaling that NewIsolatedTestConfig should be used.
func newMockFrom(cfg config.Config) *ConfigMock {
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
	mock.ProjectConfigFileNameFunc = cfg.ProjectConfigFileName
	mock.SettingsFileNameFunc = cfg.SettingsFileName
	mock.ProjectRegistryFileNameFunc = cfg.ProjectRegistryFileName
	mock.DomainFunc = cfg.Domain
	mock.LabelDomainFunc = cfg.LabelDomain
	mock.ConfigDirEnvVarFunc = cfg.ConfigDirEnvVar
	mock.StateDirEnvVarFunc = cfg.StateDirEnvVar
	mock.DataDirEnvVarFunc = cfg.DataDirEnvVar
	mock.ClawkerNetworkFunc = cfg.ClawkerNetwork
	mock.ContainerUIDFunc = cfg.ContainerUID
	mock.ContainerGIDFunc = cfg.ContainerGID
	mock.EngineLabelPrefixFunc = cfg.EngineLabelPrefix
	mock.EngineManagedLabelFunc = cfg.EngineManagedLabel
	mock.ManagedLabelValueFunc = cfg.ManagedLabelValue
	mock.GrafanaURLFunc = cfg.GrafanaURL
	mock.JaegerURLFunc = cfg.JaegerURL
	mock.PrometheusURLFunc = cfg.PrometheusURL

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

	return mock
}

// NewIsolatedTestConfig creates a file-backed config isolated to a temp directory.
// It returns a real Config (backed by configImpl with defaults) that supports Set/Write,
// and a reader callback that reads any data written to disk during the test.
// Use this for tests that need mutation, persistence, or env var overrides.
func NewIsolatedTestConfig(t *testing.T) (config.Config, func(io.Writer, io.Writer, io.Writer, io.Writer)) {
	t.Helper()

	cfg, err := config.NewBlankConfig()
	if err != nil {
		t.Fatalf("creating blank config for isolated test: %v", err)
	}

	base := t.TempDir()

	configDir := filepath.Join(base, "config")
	dataDir := filepath.Join(base, "data")
	testRepoDir := filepath.Join(base, "testrepo")
	stateDir := filepath.Join(base, "state")

	t.Setenv(cfg.ConfigDirEnvVar(), configDir)
	t.Setenv(cfg.DataDirEnvVar(), dataDir)
	t.Setenv(cfg.StateDirEnvVar(), stateDir)
	t.Setenv(cfg.TestRepoDirEnvVar(), testRepoDir)

	err = os.MkdirAll(configDir, 0o755)
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

	return cfg, func(settingsOut io.Writer, userProjectOut io.Writer, repoProjectOut io.Writer, registryOut io.Writer) {
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
