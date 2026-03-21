// Package settings provides the domain adapter for editing config.Settings via storeui.
package settings

import (
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/storeui"
)

// Overrides returns field overrides for config.Settings.
func Overrides() []storeui.Override {
	return []storeui.Override{
		// Logging
		{Path: "logging.file_enabled", Label: storeui.Ptr("Enable File Logging"), Description: storeui.Ptr("Write log output to a file")},
		{Path: "logging.max_size_mb", Label: storeui.Ptr("Max Log Size (MB)"), Description: storeui.Ptr("Maximum log file size before rotation")},
		{Path: "logging.max_age_days", Label: storeui.Ptr("Max Log Age (days)"), Description: storeui.Ptr("Days to retain old log files")},
		{Path: "logging.max_backups", Label: storeui.Ptr("Max Backups"), Description: storeui.Ptr("Maximum number of old log files to retain")},
		{Path: "logging.compress", Label: storeui.Ptr("Compress Logs"), Description: storeui.Ptr("Compress rotated log files")},

		// Logging OTEL
		{Path: "logging.otel.enabled", Label: storeui.Ptr("OTEL Logging"), Description: storeui.Ptr("Enable OpenTelemetry log bridge")},
		{Path: "logging.otel.timeout_seconds", Label: storeui.Ptr("OTEL Timeout (sec)"), Description: storeui.Ptr("OTEL exporter timeout")},
		{Path: "logging.otel.max_queue_size", Label: storeui.Ptr("OTEL Queue Size"), Description: storeui.Ptr("Maximum queued log records")},
		{Path: "logging.otel.export_interval_seconds", Label: storeui.Ptr("OTEL Export Interval (sec)"), Description: storeui.Ptr("Seconds between OTEL exports")},

		// Firewall
		{Path: "firewall.enable", Label: storeui.Ptr("Enable Firewall"), Description: storeui.Ptr("Global firewall on/off")},

		// Monitoring ports
		{Path: "monitoring.otel_collector_endpoint", Label: storeui.Ptr("OTEL Collector Endpoint")},
		{Path: "monitoring.otel_collector_port", Label: storeui.Ptr("OTEL Collector Port")},
		{Path: "monitoring.otel_collector_host", Label: storeui.Ptr("OTEL Collector Host")},
		{Path: "monitoring.otel_collector_internal", Label: storeui.Ptr("OTEL Collector Internal")},
		{Path: "monitoring.otel_grpc_port", Label: storeui.Ptr("OTEL gRPC Port")},
		{Path: "monitoring.loki_port", Label: storeui.Ptr("Loki Port")},
		{Path: "monitoring.prometheus_port", Label: storeui.Ptr("Prometheus Port")},
		{Path: "monitoring.jaeger_port", Label: storeui.Ptr("Jaeger Port")},
		{Path: "monitoring.grafana_port", Label: storeui.Ptr("Grafana Port")},
		{Path: "monitoring.prometheus_metrics_port", Label: storeui.Ptr("Prometheus Metrics Port")},

		// Telemetry
		{Path: "monitoring.telemetry.metrics_path", Label: storeui.Ptr("Metrics Path")},
		{Path: "monitoring.telemetry.logs_path", Label: storeui.Ptr("Logs Path")},
		{Path: "monitoring.telemetry.metric_export_interval_ms", Label: storeui.Ptr("Metric Export Interval (ms)")},
		{Path: "monitoring.telemetry.logs_export_interval_ms", Label: storeui.Ptr("Logs Export Interval (ms)")},
		{Path: "monitoring.telemetry.log_tool_details", Label: storeui.Ptr("Log Tool Details")},
		{Path: "monitoring.telemetry.log_user_prompts", Label: storeui.Ptr("Log User Prompts")},
		{Path: "monitoring.telemetry.include_account_uuid", Label: storeui.Ptr("Include Account UUID")},
		{Path: "monitoring.telemetry.include_session_id", Label: storeui.Ptr("Include Session ID")},

		// Host proxy — internals are read-only
		{Path: "host_proxy.manager.port", Label: storeui.Ptr("Manager Port"), ReadOnly: storeui.Ptr(true)},
		{Path: "host_proxy.daemon.port", Label: storeui.Ptr("Daemon Port"), ReadOnly: storeui.Ptr(true)},
		{Path: "host_proxy.daemon.poll_interval", Label: storeui.Ptr("Poll Interval"), ReadOnly: storeui.Ptr(true)},
		{Path: "host_proxy.daemon.grace_period", Label: storeui.Ptr("Grace Period"), ReadOnly: storeui.Ptr(true)},
		{Path: "host_proxy.daemon.max_consecutive_errs", Label: storeui.Ptr("Max Consecutive Errors"), ReadOnly: storeui.Ptr(true)},
	}
}

// LayerTargets builds the per-field save destinations for settings.
func LayerTargets(store *storage.Store[config.Settings], cfg config.Config) []storeui.LayerTarget {
	filename := cfg.SettingsFileName()

	var targets []storeui.LayerTarget
	seen := make(map[string]bool)

	// Local: CWD dot-file (skipped if CWD is unavailable).
	if cwd, err := os.Getwd(); err == nil {
		localPath := storeui.ResolveLocalPath(cwd, filename)
		targets = append(targets, storeui.LayerTarget{
			Label:       "Local",
			Description: storeui.ShortenHome(localPath),
			Path:        localPath,
		})
		seen[localPath] = true
	}

	// User: config dir file.
	userPath := filepath.Join(config.ConfigDir(), filename)
	if !seen[userPath] {
		targets = append(targets, storeui.LayerTarget{
			Label:       "User",
			Description: storeui.ShortenHome(userPath),
			Path:        userPath,
		})
		seen[userPath] = true
	}

	// Original: add any discovered layers not already in the list.
	for _, l := range store.Layers() {
		if !seen[l.Path] {
			targets = append(targets, storeui.LayerTarget{
				Label:       "Settings",
				Description: storeui.ShortenHome(l.Path),
				Path:        l.Path,
			})
			seen[l.Path] = true
		}
	}

	return targets
}

// Edit runs an interactive settings editor.
func Edit(ios *iostreams.IOStreams, store *storage.Store[config.Settings], cfg config.Config) (storeui.Result, error) {
	return storeui.Edit(ios, store,
		storeui.WithTitle("Settings Editor"),
		storeui.WithOverrides(Overrides()),
		storeui.WithLayerTargets(LayerTargets(store, cfg)),
	)
}
