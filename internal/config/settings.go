package config

import "fmt"

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
	return fmt.Sprintf("http://%s:%d", internal, port)
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
