package config

import (
	"fmt"
	"net"
	"strconv"
)

// Settings represents user-level configuration stored in ~/.local/clawker/settings.yaml.
// Settings are global and apply across all clawker projects.
type Settings struct {
	// Logging configures file-based logging.
	// File logging is ENABLED by default - users can disable via settings.yaml.
	Logging LoggingConfig `yaml:"logging,omitempty" mapstructure:"logging"`

	// Monitoring configures monitoring stack ports and OTEL endpoints.
	// These values are shared by the logger (host-side), monitor templates,
	// and Dockerfile templates (container-side).
	Monitoring MonitoringConfig `yaml:"monitoring,omitempty" mapstructure:"monitoring"`

	// DefaultImage is the user's preferred default container image.
	// Set by 'clawker init' after building the base image.
	DefaultImage string `yaml:"default_image,omitempty" mapstructure:"default_image"`
}

// LoggingConfig configures file-based logging.
// File logging is ENABLED by default - users can disable via settings.yaml.
type LoggingConfig struct {
	// FileEnabled enables logging to file (default: true)
	// Set to false in ~/.local/clawker/settings.yaml to disable
	FileEnabled *bool `yaml:"file_enabled,omitempty" mapstructure:"file_enabled"`
	// MaxSizeMB is the max size in MB before rotation (default: 50)
	MaxSizeMB int `yaml:"max_size_mb,omitempty" mapstructure:"max_size_mb"`
	// MaxAgeDays is max days to retain old logs (default: 7)
	MaxAgeDays int `yaml:"max_age_days,omitempty" mapstructure:"max_age_days"`
	// MaxBackups is max number of old log files to keep (default: 3)
	MaxBackups int `yaml:"max_backups,omitempty" mapstructure:"max_backups"`
	// Compress enables gzip compression of rotated log files (default: true)
	// Active clawker.log stays plain text; only rotated backups get gzipped.
	Compress *bool `yaml:"compress,omitempty" mapstructure:"compress"`
	// Otel configures the OTEL bridge for streaming logs to the monitoring stack.
	Otel OtelConfig `yaml:"otel,omitempty" mapstructure:"otel"`
}

// OtelConfig configures the OTEL zerolog bridge.
// The bridge streams diagnostic logs to the monitoring stack's OTEL collector.
// Endpoint is NOT configured here — it comes from MonitoringConfig.OtelCollectorEndpoint().
type OtelConfig struct {
	// Enabled enables the OTEL bridge (default: true)
	Enabled *bool `yaml:"enabled,omitempty" mapstructure:"enabled"`
	// TimeoutSeconds is the export timeout in seconds (default: 5)
	TimeoutSeconds int `yaml:"timeout_seconds,omitempty" mapstructure:"timeout_seconds"`
	// MaxQueueSize is the batch processor queue size (default: 2048)
	MaxQueueSize int `yaml:"max_queue_size,omitempty" mapstructure:"max_queue_size"`
	// ExportIntervalSeconds is the batch export interval in seconds (default: 5)
	ExportIntervalSeconds int `yaml:"export_interval_seconds,omitempty" mapstructure:"export_interval_seconds"`
}

// MonitoringConfig configures monitoring stack ports and OTEL endpoints.
// These are the single source of truth — consumed by the logger, monitor templates,
// and Dockerfile templates. Override via settings.yaml or CLAWKER_ env vars.
type MonitoringConfig struct {
	// OtelCollectorPort is the OTLP HTTP port (default: 4318)
	OtelCollectorPort int `yaml:"otel_collector_port,omitempty" mapstructure:"otel_collector_port"`
	// OtelCollectorHost is the host-side collector address (default: "localhost")
	OtelCollectorHost string `yaml:"otel_collector_host,omitempty" mapstructure:"otel_collector_host"`
	// OtelCollectorInternal is the docker-network-side collector hostname (default: "otel-collector")
	OtelCollectorInternal string `yaml:"otel_collector_internal,omitempty" mapstructure:"otel_collector_internal"`
	// OtelGRPCPort is the OTLP gRPC port (default: 4317).
	// Independent of OtelCollectorPort (HTTP) — not derived.
	OtelGRPCPort int `yaml:"otel_grpc_port,omitempty" mapstructure:"otel_grpc_port"`
	// LokiPort is the Loki HTTP port (default: 3100)
	LokiPort int `yaml:"loki_port,omitempty" mapstructure:"loki_port"`
	// PrometheusPort is the Prometheus HTTP port (default: 9090)
	PrometheusPort int `yaml:"prometheus_port,omitempty" mapstructure:"prometheus_port"`
	// JaegerPort is the Jaeger UI port (default: 16686)
	JaegerPort int `yaml:"jaeger_port,omitempty" mapstructure:"jaeger_port"`
	// GrafanaPort is the Grafana HTTP port (default: 3000)
	GrafanaPort int `yaml:"grafana_port,omitempty" mapstructure:"grafana_port"`
	// PrometheusMetricsPort is the otel-collector Prometheus exporter port (default: 8889)
	PrometheusMetricsPort int `yaml:"prometheus_metrics_port,omitempty" mapstructure:"prometheus_metrics_port"`

	// Telemetry configures Claude Code OTEL env vars baked into container images.
	// These control what telemetry data Claude Code exports and at what frequency.
	// Reference: https://docs.claude.ai/en/docs/monitoring-usage
	Telemetry TelemetryConfig `yaml:"telemetry,omitempty" mapstructure:"telemetry"`
}

// TelemetryConfig configures Claude Code OTEL env vars for container images.
// Values flow through to the Dockerfile template as ENV directives.
type TelemetryConfig struct {
	// MetricsPath is the URL path appended to the collector URL for per-signal metrics.
	// Default: "/v1/metrics" (http/protobuf convention). Set to "" for gRPC.
	// Constructed endpoint: http://<otel_collector_internal>:<port><metrics_path>
	// Maps to: OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (constructed)
	MetricsPath string `yaml:"metrics_path,omitempty" mapstructure:"metrics_path"`
	// LogsPath is the URL path appended to the collector URL for per-signal logs.
	// Default: "/v1/logs" (http/protobuf convention). Set to "" for gRPC.
	// Constructed endpoint: http://<otel_collector_internal>:<port><logs_path>
	// Maps to: OTEL_EXPORTER_OTLP_LOGS_ENDPOINT (constructed)
	LogsPath string `yaml:"logs_path,omitempty" mapstructure:"logs_path"`
	// MetricExportIntervalMs is metrics export interval in milliseconds (default: 10000)
	// Maps to: OTEL_METRIC_EXPORT_INTERVAL
	MetricExportIntervalMs int `yaml:"metric_export_interval_ms,omitempty" mapstructure:"metric_export_interval_ms"`
	// LogsExportIntervalMs is logs export interval in milliseconds (default: 5000)
	// Maps to: OTEL_LOGS_EXPORT_INTERVAL
	LogsExportIntervalMs int `yaml:"logs_export_interval_ms,omitempty" mapstructure:"logs_export_interval_ms"`
	// LogToolDetails enables logging MCP server/tool names in tool events (default: true)
	// Maps to: OTEL_LOG_TOOL_DETAILS
	LogToolDetails *bool `yaml:"log_tool_details,omitempty" mapstructure:"log_tool_details"`
	// LogUserPrompts enables logging user prompt content (default: true)
	// Maps to: OTEL_LOG_USER_PROMPTS
	LogUserPrompts *bool `yaml:"log_user_prompts,omitempty" mapstructure:"log_user_prompts"`
	// IncludeAccountUUID includes user.account_uuid attribute in metrics (default: true)
	// Maps to: OTEL_METRICS_INCLUDE_ACCOUNT_UUID
	IncludeAccountUUID *bool `yaml:"include_account_uuid,omitempty" mapstructure:"include_account_uuid"`
	// IncludeSessionID includes session.id attribute in metrics (default: true)
	// Maps to: OTEL_METRICS_INCLUDE_SESSION_ID
	IncludeSessionID *bool `yaml:"include_session_id,omitempty" mapstructure:"include_session_id"`
}

// IsFileEnabled returns whether file logging is enabled.
// Defaults to true if not explicitly set.
func (c *LoggingConfig) IsFileEnabled() bool {
	if c.FileEnabled == nil {
		return true
	}
	return *c.FileEnabled
}

// IsCompressEnabled returns whether rotated log compression is enabled.
// Defaults to true if not explicitly set.
func (c *LoggingConfig) IsCompressEnabled() bool {
	if c.Compress == nil {
		return true
	}
	return *c.Compress
}

// GetMaxSizeMB returns the max size in MB, defaulting to 50 if not set.
func (c *LoggingConfig) GetMaxSizeMB() int {
	if c.MaxSizeMB <= 0 {
		return 50
	}
	return c.MaxSizeMB
}

// GetMaxAgeDays returns the max age in days, defaulting to 7 if not set.
func (c *LoggingConfig) GetMaxAgeDays() int {
	if c.MaxAgeDays <= 0 {
		return 7
	}
	return c.MaxAgeDays
}

// GetMaxBackups returns the max backups, defaulting to 3 if not set.
func (c *LoggingConfig) GetMaxBackups() int {
	if c.MaxBackups <= 0 {
		return 3
	}
	return c.MaxBackups
}

// IsEnabled returns whether the OTEL bridge is enabled.
// Defaults to true if not explicitly set.
func (c *OtelConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// GetTimeoutSeconds returns the export timeout, defaulting to 5 if not set.
func (c *OtelConfig) GetTimeoutSeconds() int {
	if c.TimeoutSeconds <= 0 {
		return 5
	}
	return c.TimeoutSeconds
}

// GetMaxQueueSize returns the batch queue size, defaulting to 2048 if not set.
func (c *OtelConfig) GetMaxQueueSize() int {
	if c.MaxQueueSize <= 0 {
		return 2048
	}
	return c.MaxQueueSize
}

// GetExportIntervalSeconds returns the export interval, defaulting to 5 if not set.
func (c *OtelConfig) GetExportIntervalSeconds() int {
	if c.ExportIntervalSeconds <= 0 {
		return 5
	}
	return c.ExportIntervalSeconds
}

// GetOtelGRPCPort returns the OTEL gRPC port, defaulting to 4317 if not set.
func (c *MonitoringConfig) GetOtelGRPCPort() int {
	if c.OtelGRPCPort == 0 {
		return 4317
	}
	return c.OtelGRPCPort
}

// GetMetricsEndpoint constructs the OTLP metrics endpoint for containers.
// Combines: OtelCollectorInternalURL() + Telemetry.GetMetricsPath()
// e.g. "http://otel-collector:4318/v1/metrics"
func (c *MonitoringConfig) GetMetricsEndpoint() string {
	return c.OtelCollectorInternalURL() + c.Telemetry.GetMetricsPath()
}

// GetLogsEndpoint constructs the OTLP logs endpoint for containers.
// Combines: OtelCollectorInternalURL() + Telemetry.GetLogsPath()
// e.g. "http://otel-collector:4318/v1/logs"
func (c *MonitoringConfig) GetLogsEndpoint() string {
	return c.OtelCollectorInternalURL() + c.Telemetry.GetLogsPath()
}

// OtelCollectorEndpoint returns the host-side OTLP HTTP endpoint (e.g. "localhost:4318").
func (c *MonitoringConfig) OtelCollectorEndpoint() string {
	host := c.OtelCollectorHost
	if host == "" {
		host = "localhost"
	}
	port := c.OtelCollectorPort
	if port == 0 {
		port = 4318
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// OtelCollectorInternalEndpoint returns the docker-network-side OTLP HTTP endpoint
// as host:port without scheme (e.g. "otel-collector:4318").
// Use this for otlploghttp.WithEndpoint() which expects host:port, not a full URL.
// Compare: OtelCollectorInternalURL() returns "http://otel-collector:4318" (full URL with scheme).
func (c *MonitoringConfig) OtelCollectorInternalEndpoint() string {
	internal := c.OtelCollectorInternal
	if internal == "" {
		internal = "otel-collector"
	}
	port := c.OtelCollectorPort
	if port == 0 {
		port = 4318
	}
	return fmt.Sprintf("%s:%d", internal, port)
}

// OtelCollectorInternalURL returns the docker-network-side OTLP HTTP URL.
func (c *MonitoringConfig) OtelCollectorInternalURL() string {
	internal := c.OtelCollectorInternal
	if internal == "" {
		internal = "otel-collector"
	}
	port := c.OtelCollectorPort
	if port == 0 {
		port = 4318
	}
	return "http://" + net.JoinHostPort(internal, strconv.Itoa(port))
}

// LokiInternalURL returns the Loki OTLP push URL on the docker network.
func (c *MonitoringConfig) LokiInternalURL() string {
	port := c.LokiPort
	if port == 0 {
		port = 3100
	}
	return fmt.Sprintf("http://loki:%d/otlp", port)
}

// GrafanaURL returns the Grafana dashboard URL.
func (c *MonitoringConfig) GrafanaURL() string {
	port := c.GrafanaPort
	if port == 0 {
		port = 3000
	}
	return fmt.Sprintf("http://localhost:%d", port)
}

// JaegerURL returns the Jaeger UI URL.
func (c *MonitoringConfig) JaegerURL() string {
	port := c.JaegerPort
	if port == 0 {
		port = 16686
	}
	return fmt.Sprintf("http://localhost:%d", port)
}

// PrometheusURL returns the Prometheus UI URL.
func (c *MonitoringConfig) PrometheusURL() string {
	port := c.PrometheusPort
	if port == 0 {
		port = 9090
	}
	return fmt.Sprintf("http://localhost:%d", port)
}

// GetMetricsPath returns the metrics URL path, defaulting to "/v1/metrics" if not set.
func (c *TelemetryConfig) GetMetricsPath() string {
	if c.MetricsPath == "" {
		return "/v1/metrics"
	}
	return c.MetricsPath
}

// GetLogsPath returns the logs URL path, defaulting to "/v1/logs" if not set.
func (c *TelemetryConfig) GetLogsPath() string {
	if c.LogsPath == "" {
		return "/v1/logs"
	}
	return c.LogsPath
}

// GetMetricExportIntervalMs returns the metrics export interval, defaulting to 10000 if not set.
func (c *TelemetryConfig) GetMetricExportIntervalMs() int {
	if c.MetricExportIntervalMs <= 0 {
		return 10000
	}
	return c.MetricExportIntervalMs
}

// GetLogsExportIntervalMs returns the logs export interval, defaulting to 5000 if not set.
func (c *TelemetryConfig) GetLogsExportIntervalMs() int {
	if c.LogsExportIntervalMs <= 0 {
		return 5000
	}
	return c.LogsExportIntervalMs
}

// IsLogToolDetailsEnabled returns whether tool detail logging is enabled.
// Defaults to true if not explicitly set.
func (c *TelemetryConfig) IsLogToolDetailsEnabled() bool {
	if c.LogToolDetails == nil {
		return true
	}
	return *c.LogToolDetails
}

// IsLogUserPromptsEnabled returns whether user prompt logging is enabled.
// Defaults to true if not explicitly set.
func (c *TelemetryConfig) IsLogUserPromptsEnabled() bool {
	if c.LogUserPrompts == nil {
		return true
	}
	return *c.LogUserPrompts
}

// IsIncludeAccountUUIDEnabled returns whether account UUID is included in metrics.
// Defaults to true if not explicitly set.
func (c *TelemetryConfig) IsIncludeAccountUUIDEnabled() bool {
	if c.IncludeAccountUUID == nil {
		return true
	}
	return *c.IncludeAccountUUID
}

// IsIncludeSessionIDEnabled returns whether session ID is included in metrics.
// Defaults to true if not explicitly set.
func (c *TelemetryConfig) IsIncludeSessionIDEnabled() bool {
	if c.IncludeSessionID == nil {
		return true
	}
	return *c.IncludeSessionID
}
