// Package config provides types for interacting with clawker configuration files.
// It loads clawker.yaml (project) and settings.yaml (user) into a typed Config
// backed by storage.Store[T], with separate stores for project and settings schemas.
package config

import (
	"errors"
	"fmt"

	"github.com/schmitthub/clawker/internal/storage"
)

// Config is the public configuration contract.
// Add methods here as the config contract grows.
//
//go:generate moq -rm -pkg mocks -out mocks/config_mock.go . Config
type Config interface {
	ClawkerIgnoreName() string
	Project() *Project
	Settings() *Settings

	// ProjectStore returns the underlying project config store.
	// Prefer this over SetProject/WriteProject for direct store access.
	ProjectStore() *storage.Store[Project]

	// SettingsStore returns the underlying settings store.
	// Prefer this over SetSettings/WriteSettings for direct store access.
	SettingsStore() *storage.Store[Settings]

	// Deprecated: Use SettingsStore().Read().Logging instead.
	LoggingConfig() LoggingConfig

	// Deprecated: Use SettingsStore().Read().Monitoring instead.
	MonitoringConfig() MonitoringConfig

	// Deprecated: Use SettingsStore().Read().HostProxy instead.
	HostProxyConfig() HostProxyConfig

	Domain() string
	LabelDomain() string
	ConfigDirEnvVar() string
	StateDirEnvVar() string
	DataDirEnvVar() string
	TestRepoDirEnvVar() string
	MonitorSubdir() (string, error)
	BuildSubdir() (string, error)
	DockerfilesSubdir() (string, error)
	ClawkerNetwork() string
	LogsSubdir() (string, error)
	BridgesSubdir() (string, error)
	PidsSubdir() (string, error)
	BridgePIDFilePath(containerID string) (string, error)
	HostProxyLogFilePath() (string, error)
	HostProxyPIDFilePath() (string, error)
	ShareSubdir() (string, error)
	WorktreesSubdir() (string, error)
	LabelPrefix() string
	LabelManaged() string
	LabelMonitoringStack() string
	LabelProject() string
	LabelAgent() string
	LabelVersion() string
	LabelImage() string
	LabelCreated() string
	LabelWorkdir() string
	LabelPurpose() string
	LabelTestName() string
	LabelBaseImage() string
	LabelFlavor() string
	LabelTest() string
	LabelE2ETest() string
	ManagedLabelValue() string
	EngineLabelPrefix() string
	EngineManagedLabel() string
	ContainerUID() int
	ContainerGID() int
	GrafanaURL(host string, https bool) string
	JaegerURL(host string, https bool) string
	PrometheusURL(host string, https bool) string
	RequiredFirewallDomains() []string
	EgressRulesFileName() string
	FirewallDataSubdir() (string, error)
	RequiredFirewallRules() []EgressRule
	ProjectConfigFileName() string
	SettingsFileName() string
	ProjectRegistryFileName() string
	GetProjectRoot() (string, error)
	GetProjectIgnoreFile() (string, error)
}

var ErrNotInProject = errors.New("current directory is not within a configured project root")

type configImpl struct {
	project  *storage.Store[Project]
	settings *storage.Store[Settings]
}

// NewConfig loads all clawker configuration files into a Config.
// The project store discovers clawker.yaml via walk-up (CWD → project root)
// and config dir. The settings store loads settings.yaml from config dir.
// Both stores use defaults as the lowest-priority base layer.
func NewConfig() (Config, error) {
	projectStore, err := storage.NewStore[Project](
		storage.WithFilenames("clawker.local.yaml", "clawker.yaml"),
		storage.WithDefaults(defaultProjectYAML),
		storage.WithWalkUp(),
		storage.WithConfigDir(),
	)
	if err != nil {
		return nil, fmt.Errorf("config: loading project config: %w", err)
	}

	settingsStore, err := storage.NewStore[Settings](
		storage.WithFilenames("settings.yaml"),
		storage.WithDefaults(defaultSettingsYAML),
		storage.WithConfigDir(),
	)
	if err != nil {
		return nil, fmt.Errorf("config: loading settings: %w", err)
	}

	return &configImpl{
		project:  projectStore,
		settings: settingsStore,
	}, nil
}

// NewBlankConfig creates a Config with defaults but no file discovery.
// Useful as the default test double for consumers that don't care about
// specific config values.
func NewBlankConfig() (Config, error) {
	projectStore, err := storage.NewFromString[Project](defaultProjectYAML)
	if err != nil {
		return nil, fmt.Errorf("config: blank project: %w", err)
	}
	settingsStore, err := storage.NewFromString[Settings](defaultSettingsYAML)
	if err != nil {
		return nil, fmt.Errorf("config: blank settings: %w", err)
	}
	return &configImpl{
		project:  projectStore,
		settings: settingsStore,
	}, nil
}

// NewFromString creates a Config from raw YAML strings without defaults.
// Empty strings produce empty structs. Useful for test fixtures that need
// precise control over values without defaults being merged.
func NewFromString(projectYAML, settingsYAML string) (Config, error) {
	projectStore, err := storage.NewFromString[Project](projectYAML)
	if err != nil {
		return nil, fmt.Errorf("config: parsing project YAML: %w", err)
	}
	settingsStore, err := storage.NewFromString[Settings](settingsYAML)
	if err != nil {
		return nil, fmt.Errorf("config: parsing settings YAML: %w", err)
	}
	return &configImpl{
		project:  projectStore,
		settings: settingsStore,
	}, nil
}

// --- Store accessors ---

func (c *configImpl) ProjectStore() *storage.Store[Project] {
	return c.project
}

func (c *configImpl) SettingsStore() *storage.Store[Settings] {
	return c.settings
}

// --- Schema accessors ---

func (c *configImpl) RequiredFirewallDomains() []string {
	return append([]string(nil), requiredFirewallDomains...)
}

func (c *configImpl) Project() *Project {
	return c.project.Get()
}

func (c *configImpl) Settings() *Settings {
	return c.settings.Get()
}

func (c *configImpl) LoggingConfig() LoggingConfig {
	return c.settings.Get().Logging
}

func (c *configImpl) HostProxyConfig() HostProxyConfig {
	return c.settings.Get().HostProxy
}

func (c *configImpl) MonitoringConfig() MonitoringConfig {
	return c.settings.Get().Monitoring
}
