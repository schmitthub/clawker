// Package settings provides the domain adapter for editing config.Settings via storeui.
package settings

import (
	"os"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/storeui"
)

// Overrides returns field overrides for config.Settings.
func Overrides() []storeui.Override {
	return []storeui.Override{
		// Logging
		{Path: "logging.file_enabled", Label: ptr("Enable File Logging"), Description: ptr("Write log output to a file")},
		{Path: "logging.max_size_mb", Label: ptr("Max Log Size (MB)"), Description: ptr("Maximum log file size before rotation")},
		{Path: "logging.max_age_days", Label: ptr("Max Log Age (days)"), Description: ptr("Days to retain old log files")},
		{Path: "logging.max_backups", Label: ptr("Max Backups"), Description: ptr("Maximum number of old log files to retain")},
		{Path: "logging.compress", Label: ptr("Compress Logs"), Description: ptr("Compress rotated log files")},

		// Logging OTEL
		{Path: "logging.otel.enabled", Label: ptr("OTEL Logging"), Description: ptr("Enable OpenTelemetry log bridge")},
		{Path: "logging.otel.timeout_seconds", Label: ptr("OTEL Timeout (sec)"), Description: ptr("OTEL exporter timeout")},
		{Path: "logging.otel.max_queue_size", Label: ptr("OTEL Queue Size"), Description: ptr("Maximum queued log records")},
		{Path: "logging.otel.export_interval_seconds", Label: ptr("OTEL Export Interval (sec)"), Description: ptr("Seconds between OTEL exports")},

		// Firewall
		{Path: "firewall.enable", Label: ptr("Enable Firewall"), Description: ptr("Global firewall on/off")},

		// Monitoring ports
		{Path: "monitoring.otel_collector_endpoint", Label: ptr("OTEL Collector Endpoint")},
		{Path: "monitoring.otel_collector_port", Label: ptr("OTEL Collector Port")},
		{Path: "monitoring.otel_collector_host", Label: ptr("OTEL Collector Host")},
		{Path: "monitoring.otel_collector_internal", Label: ptr("OTEL Collector Internal")},
		{Path: "monitoring.otel_grpc_port", Label: ptr("OTEL gRPC Port")},
		{Path: "monitoring.loki_port", Label: ptr("Loki Port")},
		{Path: "monitoring.prometheus_port", Label: ptr("Prometheus Port")},
		{Path: "monitoring.jaeger_port", Label: ptr("Jaeger Port")},
		{Path: "monitoring.grafana_port", Label: ptr("Grafana Port")},
		{Path: "monitoring.prometheus_metrics_port", Label: ptr("Prometheus Metrics Port")},

		// Telemetry
		{Path: "monitoring.telemetry.metrics_path", Label: ptr("Metrics Path")},
		{Path: "monitoring.telemetry.logs_path", Label: ptr("Logs Path")},
		{Path: "monitoring.telemetry.metric_export_interval_ms", Label: ptr("Metric Export Interval (ms)")},
		{Path: "monitoring.telemetry.logs_export_interval_ms", Label: ptr("Logs Export Interval (ms)")},
		{Path: "monitoring.telemetry.log_tool_details", Label: ptr("Log Tool Details")},
		{Path: "monitoring.telemetry.log_user_prompts", Label: ptr("Log User Prompts")},
		{Path: "monitoring.telemetry.include_account_uuid", Label: ptr("Include Account UUID")},
		{Path: "monitoring.telemetry.include_session_id", Label: ptr("Include Session ID")},

		// Host proxy — internals are read-only
		{Path: "host_proxy.manager.port", Label: ptr("Manager Port"), ReadOnly: ptr(true)},
		{Path: "host_proxy.daemon.port", Label: ptr("Daemon Port"), ReadOnly: ptr(true)},
		{Path: "host_proxy.daemon.poll_interval", Label: ptr("Poll Interval"), ReadOnly: ptr(true)},
		{Path: "host_proxy.daemon.grace_period", Label: ptr("Grace Period"), ReadOnly: ptr(true)},
		{Path: "host_proxy.daemon.max_consecutive_errs", Label: ptr("Max Consecutive Errors"), ReadOnly: ptr(true)},
	}
}

// SaveTargets builds human-readable save target options from store layers.
func SaveTargets(store *storage.Store[config.Settings]) []storeui.SaveTarget {
	layers := store.Layers()
	if len(layers) == 0 {
		return []storeui.SaveTarget{
			{Label: "User settings", Description: "Default settings location"},
		}
	}

	targets := make([]storeui.SaveTarget, len(layers))
	for i, l := range layers {
		targets[i] = storeui.SaveTarget{
			Label:       "User settings",
			Description: shortenPath(l.Path),
			Filename:    l.Filename,
		}
	}
	return targets
}

// shortenPath replaces $HOME with ~ for display.
func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// Edit runs an interactive settings editor.
func Edit(ios *iostreams.IOStreams, store *storage.Store[config.Settings]) (storeui.Result, error) {
	return storeui.Edit(ios, store,
		storeui.WithTitle("Settings Editor"),
		storeui.WithOverrides(Overrides()),
		storeui.WithSaveTargets(SaveTargets(store)),
	)
}

func ptr[T any](v T) *T {
	return &v
}
