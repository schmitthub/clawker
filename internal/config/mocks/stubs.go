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
// projectYAML and settingsYAML are raw YAML strings with NO defaults merged
// (config.NewFromString is the option-free in-memory seam: no discovery, no
// disk). Pass empty strings for schemas you don't care about.
// Panics on invalid YAML to match test-stub ergonomics.
// Mutation goes through ProjectStore()/SettingsStore(), whose Write() fails on
// a seam store by design — use NewIsolatedTestConfig for mutation tests.
func NewFromString(projectYAML, settingsYAML string) *ConfigMock {
	cfg, err := config.NewFromString(projectYAML, settingsYAML)
	if err != nil {
		panic(err)
	}
	return newMockFrom(cfg)
}

// newMockFrom wires every read Func field on a ConfigMock to delegate to cfg —
// the seeded in-memory Config is the source of truth for reads.
//
// The store accessors (ProjectStore/SettingsStore) are the mutation surface,
// and they hand back the seam's real stores: those have no path options, so a
// Write() through them fails by design. Consumer tests that mutate config use
// NewIsolatedTestConfig instead, which is file-backed.
func newMockFrom(cfg config.Config) *ConfigMock {
	mock := &ConfigMock{}

	// Store accessors (the raw-verb escape hatch)
	mock.ProjectStoreFunc = cfg.ProjectStore
	mock.SettingsStoreFunc = cfg.SettingsStore
	mock.ProjectRootFunc = cfg.ProjectRoot

	// Project value accessors
	mock.ProjectNameFunc = cfg.ProjectName
	mock.AliasesFunc = cfg.Aliases
	mock.BuildConfigFunc = cfg.BuildConfig
	mock.AgentConfigFunc = cfg.AgentConfig
	mock.WorkspaceDefaultModeFunc = cfg.WorkspaceDefaultMode
	mock.SecurityConfigFunc = cfg.SecurityConfig
	mock.HarnessConfigForFunc = cfg.HarnessConfigFor
	mock.PostInitForFunc = cfg.PostInitFor
	mock.PreRunForFunc = cfg.PreRunFor
	mock.MonitorExtensionsFunc = cfg.MonitorExtensions
	mock.ProjectEgressRulesFunc = cfg.ProjectEgressRules
	mock.BundleDeclarationsFunc = cfg.BundleDeclarations

	// Settings value accessors
	mock.LoggingConfigFunc = cfg.LoggingConfig
	mock.MonitoringConfigFunc = cfg.MonitoringConfig
	mock.HostProxyConfigFunc = cfg.HostProxyConfig
	mock.ControlPlaneSettingsFunc = cfg.ControlPlaneSettings
	mock.FirewallEnabledFunc = cfg.FirewallEnabled
	mock.DockerHostFunc = cfg.DockerHost
	mock.EgressRulesFileNameFunc = cfg.EgressRulesFileName

	// Constants
	mock.ClawkerIgnoreNameFunc = cfg.ClawkerIgnoreName
	mock.ProjectConfigFileNameFunc = cfg.ProjectConfigFileName
	mock.SettingsFileNameFunc = cfg.SettingsFileName
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
	mock.OpenSearchURLFunc = cfg.OpenSearchURL
	mock.OpenSearchDashboardsURLFunc = cfg.OpenSearchDashboardsURL
	mock.PrometheusURLFunc = cfg.PrometheusURL
	mock.OtelCollectorURLFunc = cfg.OtelCollectorURL
	mock.EnvoyIPLastOctetFunc = cfg.EnvoyIPLastOctet
	mock.CoreDNSIPLastOctetFunc = cfg.CoreDNSIPLastOctet
	mock.CPIPLastOctetFunc = cfg.CPIPLastOctet
	mock.EnvoyEgressPortFunc = cfg.EnvoyEgressPort
	mock.EnvoyTCPPortBaseFunc = cfg.EnvoyTCPPortBase
	mock.EnvoyUDPPortBaseFunc = cfg.EnvoyUDPPortBase
	mock.EnvoyHealthPortFunc = cfg.EnvoyHealthPort
	mock.EnvoyHealthHostPortFunc = cfg.EnvoyHealthHostPort
	mock.CoreDNSHealthHostPortFunc = cfg.CoreDNSHealthHostPort
	mock.CoreDNSHealthPathFunc = cfg.CoreDNSHealthPath

	// Path helpers
	mock.MonitorSubdirFunc = cfg.MonitorSubdir
	mock.BuildSubdirFunc = cfg.BuildSubdir
	mock.LogsSubdirFunc = cfg.LogsSubdir
	mock.BridgesSubdirFunc = cfg.BridgesSubdir
	mock.PidsSubdirFunc = cfg.PidsSubdir
	mock.FirewallDataSubdirFunc = cfg.FirewallDataSubdir
	mock.FirewallCertSubdirFunc = cfg.FirewallCertSubdir
	mock.ShareSubdirFunc = cfg.ShareSubdir
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

// SecurityConfig builds the config.SecurityConfig value a test hands to code
// that takes one directly (rather than through a Config double).
//
// With no mutators it is the "project declares no security block" baseline:
// no firewall, no docker socket, no extra capabilities, and nil pointers that
// let SecurityConfig's own nil-tolerant methods apply their defaults. That
// baseline is what the vast majority of container-build tests want, so it
// lives here once instead of being respelled as a bare struct literal at
// every call site.
//
// Each mutator receives the value under construction, so a test that cares
// about one field states only that field:
//
//	sec := configmocks.SecurityConfig(func(s *config.SecurityConfig) {
//		s.CapAdd = []string{"NET_RAW"}
//	})
func SecurityConfig(mutators ...func(*config.SecurityConfig)) config.SecurityConfig {
	var security config.SecurityConfig
	for _, mutate := range mutators {
		mutate(&security)
	}
	return security
}
