// Package config provides types for interacting with clawker configuration files.
// It loads clawker.yaml (project) and settings.yaml (user) into one merged
// in-memory Config backed by viper, with key-path traversal via Get/Set/Keys/Remove.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Config is the public configuration contract.
// Add methods here as the config contract grows.
//
//go:generate moq -rm -pkg mocks -out mocks/config_mock.go . Config
type Config interface {
	ClawkerIgnoreName() string
	Logging() map[string]any
	Project() *Project
	Settings() Settings
	LoggingConfig() LoggingConfig
	MonitoringConfig() MonitoringConfig
	Get(key string) (any, error)
	Set(key string, value any) error
	Write(opts WriteOptions) error
	Watch(onChange func(fsnotify.Event)) error
	Domain() string
	LabelDomain() string
	ConfigDirEnvVar() string
	StateDirEnvVar() string
	DataDirEnvVar() string
	MonitorSubdir() (string, error)
	BuildSubdir() (string, error)
	DockerfilesSubdir() (string, error)
	ClawkerNetwork() string
	LogsSubdir() (string, error)
	BridgesSubdir() (string, error)
	PidsSubdir() (string, error)
	BridgePIDFilePath(containerID string) (string, error)
	HostProxyConfig() HostProxyConfig
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
	ProjectConfigFileName() string
	SettingsFileName() string
	ProjectRegistryFileName() string
	GetProjectRoot() (string, error)
	GetProjectIgnoreFile() (string, error)
}

var ErrNotInProject = errors.New("current directory is not within a configured project root")

type configImpl struct {
	v *viper.Viper

	settingsFile          string
	userProjectConfigFile string
	projectRegistryPath   string
	projectConfigFile     string
	dirty                 *dirtyNode

	mu sync.RWMutex
}

type ConfigScope string

const (
	ScopeSettings ConfigScope = "settings"
	ScopeProject  ConfigScope = "project"
	ScopeRegistry ConfigScope = "registry"
)

var keyOwnership = map[string]ConfigScope{
	"logging":       ScopeSettings,
	"monitoring":    ScopeSettings,
	"host_proxy":    ScopeSettings,
	"default_image": ScopeSettings,

	"projects": ScopeRegistry,

	"version":   ScopeProject,
	"name":      ScopeProject,
	"build":     ScopeProject,
	"agent":     ScopeProject,
	"workspace": ScopeProject,
	"security":  ScopeProject,
	"loop":      ScopeProject,
}

// validScopes is the set of recognised namespace prefixes, derived from keyOwnership.
var validScopes map[string]ConfigScope

func init() {
	validScopes = make(map[string]ConfigScope)
	for _, scope := range keyOwnership {
		validScopes[string(scope)] = scope
	}
}

func newConfig(v *viper.Viper) *configImpl {
	return &configImpl{
		v:     v,
		dirty: newDirtyNode(),
	}
}

// --- Schema accessors ---

func (c *configImpl) RequiredFirewallDomains() []string {
	return append([]string(nil), requiredFirewallDomains...)
}

func (c *configImpl) Logging() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.v.GetStringMap("settings.logging")
}

func (c *configImpl) Project() *Project {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p := &Project{}
	// Use AllSettings() to get a fully merged map across all layers
	// (config, defaults, env). UnmarshalKey("project") returns only the
	// config-layer subtree, which omits default values for nested keys
	// that weren't explicitly set in config files.
	all := c.v.AllSettings()
	projectRaw, ok := all["project"]
	if !ok {
		return p
	}
	projectMap, ok := projectRaw.(map[string]any)
	if !ok {
		fmt.Fprintf(os.Stderr, "config: Project() expected map[string]any, got %T\n", projectRaw)
		return p
	}
	sub := viper.New()
	if err := sub.MergeConfigMap(projectMap); err != nil {
		fmt.Fprintf(os.Stderr, "config: Project() MergeConfigMap: %v\n", err)
		return p
	}
	if err := sub.Unmarshal(p); err != nil {
		fmt.Fprintf(os.Stderr, "config: Project() Unmarshal: %v\n", err)
		return p
	}
	restoreDottedLabelKeys(p)
	return p
}

func restoreDottedLabelKeys(project *Project) {
	if project == nil || project.Build.Instructions == nil {
		return
	}

	labels := project.Build.Instructions.Labels
	if len(labels) == 0 {
		return
	}

	restored := make(map[string]string, len(labels))
	for key, value := range labels {
		restored[strings.ReplaceAll(key, dottedLabelKeySentinel, ".")] = value
	}

	project.Build.Instructions.Labels = restored
}

func (c *configImpl) Settings() Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Settings{
		Logging: LoggingConfig{
			FileEnabled: boolPtr(c.v.GetBool("settings.logging.file_enabled")),
			MaxSizeMB:   c.v.GetInt("settings.logging.max_size_mb"),
			MaxAgeDays:  c.v.GetInt("settings.logging.max_age_days"),
			MaxBackups:  c.v.GetInt("settings.logging.max_backups"),
			Compress:    boolPtr(c.v.GetBool("settings.logging.compress")),
			Otel: OtelConfig{
				Enabled:               boolPtr(c.v.GetBool("settings.logging.otel.enabled")),
				TimeoutSeconds:        c.v.GetInt("settings.logging.otel.timeout_seconds"),
				MaxQueueSize:          c.v.GetInt("settings.logging.otel.max_queue_size"),
				ExportIntervalSeconds: c.v.GetInt("settings.logging.otel.export_interval_seconds"),
			},
		},
		Monitoring: MonitoringConfig{
			OtelCollectorEndpoint: c.v.GetString("settings.monitoring.otel_collector_endpoint"),
			OtelCollectorPort:     c.v.GetInt("settings.monitoring.otel_collector_port"),
			OtelCollectorHost:     c.v.GetString("settings.monitoring.otel_collector_host"),
			OtelCollectorInternal: c.v.GetString("settings.monitoring.otel_collector_internal"),
			OtelGRPCPort:          c.v.GetInt("settings.monitoring.otel_grpc_port"),
			LokiPort:              c.v.GetInt("settings.monitoring.loki_port"),
			PrometheusPort:        c.v.GetInt("settings.monitoring.prometheus_port"),
			JaegerPort:            c.v.GetInt("settings.monitoring.jaeger_port"),
			GrafanaPort:           c.v.GetInt("settings.monitoring.grafana_port"),
			PrometheusMetricsPort: c.v.GetInt("settings.monitoring.prometheus_metrics_port"),
			Telemetry: TelemetryConfig{
				MetricsPath:            c.v.GetString("settings.monitoring.telemetry.metrics_path"),
				LogsPath:               c.v.GetString("settings.monitoring.telemetry.logs_path"),
				MetricExportIntervalMs: c.v.GetInt("settings.monitoring.telemetry.metric_export_interval_ms"),
				LogsExportIntervalMs:   c.v.GetInt("settings.monitoring.telemetry.logs_export_interval_ms"),
				LogToolDetails:         boolPtr(c.v.GetBool("settings.monitoring.telemetry.log_tool_details")),
				LogUserPrompts:         boolPtr(c.v.GetBool("settings.monitoring.telemetry.log_user_prompts")),
				IncludeAccountUUID:     boolPtr(c.v.GetBool("settings.monitoring.telemetry.include_account_uuid")),
				IncludeSessionID:       boolPtr(c.v.GetBool("settings.monitoring.telemetry.include_session_id")),
			},
		},
		HostProxy: HostProxyConfig{
			Manager: HostProxyManagerConfig{
				Port: c.v.GetInt("settings.host_proxy.manager.port"),
			},
			Daemon: HostProxyDaemonConfig{
				Port:               c.v.GetInt("settings.host_proxy.daemon.port"),
				PollInterval:       c.v.GetDuration("settings.host_proxy.daemon.poll_interval"),
				GracePeriod:        c.v.GetDuration("settings.host_proxy.daemon.grace_period"),
				MaxConsecutiveErrs: c.v.GetInt("settings.host_proxy.daemon.max_consecutive_errs"),
			},
		},
		DefaultImage: c.v.GetString("settings.default_image"),
	}
}

func (c *configImpl) LoggingConfig() LoggingConfig {
	return c.Settings().Logging
}

func (c *configImpl) HostProxyConfig() HostProxyConfig {
	return c.Settings().HostProxy
}

func (c *configImpl) MonitoringConfig() MonitoringConfig {
	return c.Settings().Monitoring
}

// --- Get / Set / Watch ---

// Get returns the value for a namespaced config key (e.g. "project.build.image",
// "settings.logging.file_enabled").
//
// It returns KeyNotFoundError when the key is not set in the merged
// configuration state (including defaults and environment overrides).
// Access is protected by an RWMutex for safe concurrent reads.
func (c *configImpl) Get(key string) (any, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if _, err := scopeFromNamespacedKey(key); err != nil {
		return nil, err
	}

	if !c.v.IsSet(key) {
		return nil, &KeyNotFoundError{Key: key}
	}

	return c.v.Get(key), nil
}

// Set updates a namespaced config key in-memory and marks it dirty.
// Keys must be namespaced (e.g. "project.build.image", "settings.logging.file_enabled").
// The scope is derived from the first key segment, and dirty tracking uses the
// namespaced key so writes route to the correct file automatically.
// Access is protected by an RWMutex for safe concurrent writes.
func (c *configImpl) Set(key string, value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, err := scopeFromNamespacedKey(key); err != nil {
		return err
	}

	c.v.Set(key, value)
	c.markDirtyPath(key)
	return nil
}

// Watch enables file watching for the currently loaded config file.
//
// If onChange is non-nil, it is registered with Viper's OnConfigChange hook.
// The caller must ensure config paths/files were configured before watching;
// this method returns an error when no config file is currently in use.
// Access is protected by an RWMutex for safe watcher setup.
func (c *configImpl) Watch(onChange func(fsnotify.Event)) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.v.ConfigFileUsed() == "" {
		return fmt.Errorf("watch config requires a loaded config file")
	}

	if onChange != nil {
		c.v.OnConfigChange(onChange)
	}
	c.v.WatchConfig()
	return nil
}

// --- Key / scope helpers ---

func schemaForScope(scope ConfigScope) any {
	switch scope {
	case ScopeProject:
		return &Project{}
	case ScopeSettings:
		return &Settings{}
	case ScopeRegistry:
		return &ProjectRegistry{}
	default:
		panic(fmt.Sprintf("config: no schema for scope %q", scope))
	}
}

func scopeForKey(key string) (ConfigScope, error) {
	root := keyRoot(key)
	scope, ok := keyOwnership[root]
	if !ok {
		return "", fmt.Errorf("no ownership mapping for key: %s", key)
	}
	return scope, nil
}

func keyRoot(key string) string {
	parts := strings.SplitN(key, ".", 2)
	return parts[0]
}

// namespacedKey converts a flat (file-relative) config key to its namespaced
// equivalent by prepending the owning scope. Used internally by setDefaults and
// load to translate file keys into the namespaced Viper store.
// E.g. "build.image" → "project.build.image".
func namespacedKey(flat string) (string, error) {
	scope, err := scopeForKey(flat)
	if err != nil {
		return "", err
	}
	return string(scope) + "." + flat, nil
}

// namespaceMap wraps a flat config map (as read from a single YAML file)
// under a scope prefix key.
// E.g. namespaceMap({"build": {...}}, ScopeProject) → {"project": {"build": {...}}}.
func namespaceMap(flat map[string]any, scope ConfigScope) map[string]any {
	return map[string]any{string(scope): flat}
}

// scopeFromNamespacedKey extracts the ConfigScope from the first segment of a
// namespaced key. E.g. "project.build.image" → ScopeProject.
func scopeFromNamespacedKey(key string) (ConfigScope, error) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("namespaced key must have at least two segments: %s", key)
	}
	scope, ok := validScopes[parts[0]]
	if !ok {
		return "", fmt.Errorf("unknown scope prefix %q in key: %s", parts[0], key)
	}
	return scope, nil
}

// stripScopePrefix removes the first dotted segment from a namespaced key.
// E.g. "project.build.image" → "build.image", "settings.logging" → "logging".
func stripScopePrefix(key string) string {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) < 2 {
		return key
	}
	return parts[1]
}

func boolPtr(v bool) *bool {
	b := v
	return &b
}
