// Package config provides types for interacting with clawker configuration files.
// It loads clawker.yaml (project) and settings.yaml (user) into one merged
// in-memory Config backed by viper, with key-path traversal via Get/Set/Keys/Remove.
// Most of this code is based on [github.com/cli/cli/blob/trunk/pkg/config/config.go](github.com/cli/cli/blob/trunk/pkg/config/config.go).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/spf13/viper"
)

var invalidKeysRe = regexp.MustCompile(`'([^']+)' has invalid keys: (.+)$`)

const (
	appData          = "AppData"
	xdgConfigHome    = "XDG_CONFIG_HOME"
)

// Config is the public configuration contract.
// Add methods here as the config contract grows.
type Config interface {
	Logging() map[string]any
	Project() *Project
	Settings() Settings
	LoggingConfig() LoggingConfig
	MonitoringConfig() MonitoringConfig
	Domain() string
	LabelDomain() string
	ConfigDirEnvVar() string
	MonitorSubdir() string
	BuildSubdir() string
	DockerfilesSubdir() string
	ClawkerNetwork() string
	LogsSubdir() string
	BridgesSubdir() string
	ShareSubdir() string
	RequiredFirewallDomains() []string
	GetProjectRoot() (string, error)
}

var ErrNotInProject = errors.New("current directory is not within a configured project root")

type configImpl struct {
	v *viper.Viper
}

func newViperConfig() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("CLAWKER")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	setDefaults(v)
	return v
}

func newConfig(v *viper.Viper) *configImpl {
	return &configImpl{
		v: v,
	}
}

// NewConfig loads all clawker configuration files into a single Config.
// Precedence (highest to lowest): project config > project registry > user config > settings
func NewConfig() (Config, error) {
	c := newConfig(newViperConfig())
	if err := c.load(loadOptions{
		settingsFile:          settingsConfigFile(),
		userProjectConfigFile: userProjectConfigFile(),
		projectRegistryPath:   projectRegistryPath(),
	}); err != nil {
		return nil, err
	}
	return c, nil
}

// ReadFromString takes a YAML string and returns a Config.
// Useful for testing or constructing configs programmatically.
func ReadFromString(str string) (Config, error) {
	if err := validateProjectYAMLString(str); err != nil {
		return nil, err
	}

	v := newViperConfig()
	v.SetConfigType("yaml")
	if str != "" {
		err := v.ReadConfig(strings.NewReader(str))
		if err != nil {
			return nil, fmt.Errorf("parsing config from string: %w", err)
		}
	}
	return newConfig(v), nil
}

func (c *configImpl) RequiredFirewallDomains() []string {
	return append([]string(nil), requiredFirewallDomains...)
}

func (c *configImpl) Logging() map[string]any {
	return c.v.GetStringMap("logging")
}

func (c *configImpl) Project() *Project {
	p := &Project{}
	_ = c.v.Unmarshal(p)
	return p
}

func (c *configImpl) Settings() Settings {
	return Settings{
		Logging:      c.LoggingConfig(),
		Monitoring:   c.MonitoringConfig(),
		DefaultImage: c.v.GetString("default_image"),
	}
}

func (c *configImpl) LoggingConfig() LoggingConfig {
	return LoggingConfig{
		FileEnabled: boolPtr(c.v.GetBool("logging.file_enabled")),
		MaxSizeMB:   c.v.GetInt("logging.max_size_mb"),
		MaxAgeDays:  c.v.GetInt("logging.max_age_days"),
		MaxBackups:  c.v.GetInt("logging.max_backups"),
		Compress:    boolPtr(c.v.GetBool("logging.compress")),
		Otel: OtelConfig{
			Enabled:               boolPtr(c.v.GetBool("logging.otel.enabled")),
			TimeoutSeconds:        c.v.GetInt("logging.otel.timeout_seconds"),
			MaxQueueSize:          c.v.GetInt("logging.otel.max_queue_size"),
			ExportIntervalSeconds: c.v.GetInt("logging.otel.export_interval_seconds"),
		},
	}
}

func (c *configImpl) MonitoringConfig() MonitoringConfig {
	return MonitoringConfig{
		OtelCollectorEndpoint: c.v.GetString("monitoring.otel_collector_endpoint"),
		OtelCollectorPort:     c.v.GetInt("monitoring.otel_collector_port"),
		OtelCollectorHost:     c.v.GetString("monitoring.otel_collector_host"),
		OtelCollectorInternal: c.v.GetString("monitoring.otel_collector_internal"),
		OtelGRPCPort:          c.v.GetInt("monitoring.otel_grpc_port"),
		LokiPort:              c.v.GetInt("monitoring.loki_port"),
		PrometheusPort:        c.v.GetInt("monitoring.prometheus_port"),
		JaegerPort:            c.v.GetInt("monitoring.jaeger_port"),
		GrafanaPort:           c.v.GetInt("monitoring.grafana_port"),
		PrometheusMetricsPort: c.v.GetInt("monitoring.prometheus_metrics_port"),
		Telemetry: TelemetryConfig{
			MetricsPath:            c.v.GetString("monitoring.telemetry.metrics_path"),
			LogsPath:               c.v.GetString("monitoring.telemetry.logs_path"),
			MetricExportIntervalMs: c.v.GetInt("monitoring.telemetry.metric_export_interval_ms"),
			LogsExportIntervalMs:   c.v.GetInt("monitoring.telemetry.logs_export_interval_ms"),
			LogToolDetails:         boolPtr(c.v.GetBool("monitoring.telemetry.log_tool_details")),
			LogUserPrompts:         boolPtr(c.v.GetBool("monitoring.telemetry.log_user_prompts")),
			IncludeAccountUUID:     boolPtr(c.v.GetBool("monitoring.telemetry.include_account_uuid")),
			IncludeSessionID:       boolPtr(c.v.GetBool("monitoring.telemetry.include_session_id")),
		},
	}
}

func (c *configImpl) GetProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting cwd: %w", err)
	}
	cwd = filepath.Clean(cwd)

	projects := c.v.GetStringMap("projects")
	bestMatch := ""
	for key := range projects {
		root := filepath.Clean(c.v.GetString(fmt.Sprintf("projects.%s.root", key)))
		rel, relErr := filepath.Rel(root, cwd)
		if relErr != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			if len(root) > len(bestMatch) {
				bestMatch = root
			}
		}
	}

	if bestMatch == "" {
		return "", fmt.Errorf("%w: %s", ErrNotInProject, cwd)
	}

	return bestMatch, nil
}

type loadOptions struct {
	settingsFile          string
	userProjectConfigFile string
	projectRegistryPath   string
}

func (c *configImpl) load(opts loadOptions) error {
	files := []struct {
		path   string
		schema any
	}{
		{path: opts.settingsFile, schema: &Settings{}},
		{path: opts.userProjectConfigFile, schema: &Project{}},
		{path: opts.projectRegistryPath, schema: &projectRegistryValidation{}},
	}

	for i, f := range files {
		if err := validateConfigFileExact(f.path, f.schema); err != nil {
			return err
		}

		c.v.SetConfigFile(f.path)
		var err error
		if i == 0 {
			err = c.v.ReadInConfig()
		} else {
			err = c.v.MergeInConfig()
		}
		if err != nil {
			return fmt.Errorf("loading config %s: %w", f.path, err)
		}
	}

	return c.mergeProjectConfig()
}

func (c *configImpl) mergeProjectConfig() error {
	root, err := c.GetProjectRoot()
	if err != nil {
		if errors.Is(err, ErrNotInProject) {
			return nil
		}
		return err
	}

	c.v.SetConfigFile(filepath.Join(root, "clawker.yaml"))
	if err := validateConfigFileExact(filepath.Join(root, "clawker.yaml"), &Project{}); err != nil {
		return err
	}
	if err := c.v.MergeInConfig(); err != nil {
		return fmt.Errorf("loading project config for root %s: %w", root, err)
	}

	return nil
}

// ConfigDir returns the clawker config directory.
func ConfigDir() string {
	if a := os.Getenv(clawkerConfigDirEnv); a != "" {
		return a
	}
	if b := os.Getenv(xdgConfigHome); b != "" {
		return filepath.Join(b, "clawker")
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv(appData); c != "" {
			return filepath.Join(c, "clawker")
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".config", "clawker")
}

func settingsConfigFile() string {
	return filepath.Join(ConfigDir(), "settings.yaml")
}

func userProjectConfigFile() string {
	return filepath.Join(ConfigDir(), "clawker.yaml")
}

func projectRegistryPath() string {
	return filepath.Join(ConfigDir(), "projects.yaml")
}

func boolPtr(v bool) *bool {
	b := v
	return &b
}

func validateProjectYAMLString(str string) error {
	if str == "" {
		return nil
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(str)); err != nil {
		return fmt.Errorf("parsing config from string: %w", err)
	}

	if err := v.UnmarshalExact(&readFromStringValidation{}); err != nil {
		return fmt.Errorf("invalid project config: %s", formatDecodeError(err))
	}

	return nil
}

func validateConfigFileExact(path string, schema any) error {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("loading config %s: %w", path, err)
	}

	if err := v.UnmarshalExact(schema); err != nil {
		return fmt.Errorf("invalid config %s: %s", path, formatDecodeError(err))
	}

	return nil
}

func formatDecodeError(err error) string {
	msg := err.Error()
	match := invalidKeysRe.FindStringSubmatch(msg)
	if len(match) != 3 {
		return msg
	}

	parent := strings.TrimSpace(match[1])
	keys := strings.Split(match[2], ",")
	paths := make([]string, 0, len(keys))
	for _, key := range keys {
		k := strings.TrimSpace(key)
		if parent == "" || parent == "<root>" {
			paths = append(paths, k)
			continue
		}
		paths = append(paths, parent+"."+k)
	}

	return "unknown keys: " + strings.Join(paths, ", ")
}

// readFromStringValidation is a permissive root schema for ReadFromString.
// It validates unknown keys for known config roots while allowing ad-hoc
// projects maps used in tests.
type readFromStringValidation struct {
	Version      string           `mapstructure:"version"`
	Project      string           `mapstructure:"project"`
	DefaultImage string           `mapstructure:"default_image"`
	Build        BuildConfig      `mapstructure:"build"`
	Agent        AgentConfig      `mapstructure:"agent"`
	Workspace    WorkspaceConfig  `mapstructure:"workspace"`
	Security     SecurityConfig   `mapstructure:"security"`
	Loop         *LoopConfig      `mapstructure:"loop"`
	Logging      LoggingConfig    `mapstructure:"logging"`
	Monitoring   MonitoringConfig `mapstructure:"monitoring"`
	Projects     map[string]any   `mapstructure:"projects"`
}

// projectRegistryValidation allows legacy worktree values while still
// rejecting unknown keys on registry/project entries.
type projectRegistryValidation struct {
	Projects map[string]projectEntryValidation `mapstructure:"projects"`
}

type projectEntryValidation struct {
	Name      string         `mapstructure:"name"`
	Root      string         `mapstructure:"root"`
	Worktrees map[string]any `mapstructure:"worktrees"`
}
