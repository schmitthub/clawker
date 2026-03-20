package mocks

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/testenv"
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
// projectYAML and settingsYAML are raw YAML strings with NO defaults merged.
// Pass empty strings for schemas you don't care about.
// Panics on invalid YAML to match test-stub ergonomics.
// SetProject, SetSettings, WriteProject, WriteSettings are NOT wired —
// calling them panics via moq's nil-func guard, signaling that
// NewIsolatedTestConfig should be used for mutation tests.
func NewFromString(projectYAML, settingsYAML string) *ConfigMock {
	cfg, err := config.NewFromString(projectYAML, settingsYAML)
	if err != nil {
		panic(err)
	}
	return newMockFrom(cfg)
}

// newMockFrom wires all read-only Func fields on a ConfigMock to delegate to cfg.
// Mutation methods (SetProject, SetSettings, WriteProject, WriteSettings) are
// intentionally NOT wired — calling them panics via moq's nil-func guard,
// signaling that NewIsolatedTestConfig should be used.
func newMockFrom(cfg config.Config) *ConfigMock {
	mock := &ConfigMock{}

	// Store accessors
	mock.ProjectStoreFunc = cfg.ProjectStore
	mock.SettingsStoreFunc = cfg.SettingsStore

	// Schema accessors
	mock.ProjectFunc = cfg.Project
	mock.SettingsFunc = cfg.Settings
	mock.LoggingConfigFunc = cfg.LoggingConfig
	mock.MonitoringConfigFunc = cfg.MonitoringConfig
	mock.HostProxyConfigFunc = cfg.HostProxyConfig
	mock.RequiredFirewallDomainsFunc = cfg.RequiredFirewallDomains
	mock.EgressRulesFileNameFunc = cfg.EgressRulesFileName
	mock.FirewallDataSubdirFunc = cfg.FirewallDataSubdir
	mock.FirewallPIDFilePathFunc = cfg.FirewallPIDFilePath
	mock.FirewallLogFilePathFunc = cfg.FirewallLogFilePath
	mock.RequiredFirewallRulesFunc = cfg.RequiredFirewallRules
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
	mock.TestRepoDirEnvVarFunc = cfg.TestRepoDirEnvVar
	mock.ClawkerNetworkFunc = cfg.ClawkerNetwork
	mock.ContainerUIDFunc = cfg.ContainerUID
	mock.ContainerGIDFunc = cfg.ContainerGID
	mock.EngineLabelPrefixFunc = cfg.EngineLabelPrefix
	mock.EngineManagedLabelFunc = cfg.EngineManagedLabel
	mock.ManagedLabelValueFunc = cfg.ManagedLabelValue
	mock.GrafanaURLFunc = cfg.GrafanaURL
	mock.JaegerURLFunc = cfg.JaegerURL
	mock.PrometheusURLFunc = cfg.PrometheusURL
	mock.EnvoyIPLastOctetFunc = cfg.EnvoyIPLastOctet
	mock.CoreDNSIPLastOctetFunc = cfg.CoreDNSIPLastOctet
	mock.EnvoyTLSPortFunc = cfg.EnvoyTLSPort
	mock.EnvoyTCPPortBaseFunc = cfg.EnvoyTCPPortBase
	mock.EnvoyHTTPPortFunc = cfg.EnvoyHTTPPort
	mock.EnvoyHealthHostPortFunc = cfg.EnvoyHealthHostPort
	mock.CoreDNSHealthHostPortFunc = cfg.CoreDNSHealthHostPort
	mock.CoreDNSHealthPathFunc = cfg.CoreDNSHealthPath

	// Path helpers
	mock.MonitorSubdirFunc = cfg.MonitorSubdir
	mock.BuildSubdirFunc = cfg.BuildSubdir
	mock.DockerfilesSubdirFunc = cfg.DockerfilesSubdir
	mock.LogsSubdirFunc = cfg.LogsSubdir
	mock.BridgesSubdirFunc = cfg.BridgesSubdir
	mock.PidsSubdirFunc = cfg.PidsSubdir
	mock.ShareSubdirFunc = cfg.ShareSubdir
	mock.WorktreesSubdirFunc = cfg.WorktreesSubdir
	mock.BridgePIDFilePathFunc = cfg.BridgePIDFilePath
	mock.HostProxyPIDFilePathFunc = cfg.HostProxyPIDFilePath
	mock.HostProxyLogFilePathFunc = cfg.HostProxyLogFilePath

	// Labels
	mock.LabelPrefixFunc = cfg.LabelPrefix
	mock.LabelManagedFunc = cfg.LabelManaged
	mock.LabelProjectFunc = cfg.LabelProject
	mock.LabelAgentFunc = cfg.LabelAgent
	mock.LabelVersionFunc = cfg.LabelVersion
	mock.LabelImageFunc = cfg.LabelImage
	mock.LabelCreatedFunc = cfg.LabelCreated
	mock.LabelWorkdirFunc = cfg.LabelWorkdir
	mock.LabelPurposeFunc = cfg.LabelPurpose
	mock.PurposeAgentFunc = cfg.PurposeAgent
	mock.PurposeMonitoringFunc = cfg.PurposeMonitoring
	mock.PurposeFirewallFunc = cfg.PurposeFirewall
	mock.LabelTestNameFunc = cfg.LabelTestName
	mock.LabelBaseImageFunc = cfg.LabelBaseImage
	mock.LabelFlavorFunc = cfg.LabelFlavor
	mock.LabelTestFunc = cfg.LabelTest
	mock.LabelE2ETestFunc = cfg.LabelE2ETest

	return mock
}

// NewIsolatedTestConfig creates a file-backed config isolated to a temp directory.
// It returns a real Config (backed by storage.Store) that supports Set/Write.
// Delegates directory setup to testenv.New with WithConfig.
func NewIsolatedTestConfig(t *testing.T) config.Config {
	t.Helper()
	env := testenv.New(t, testenv.WithConfig())
	return env.Config()
}
