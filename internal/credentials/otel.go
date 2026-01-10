package credentials

// OtelEnvVars returns environment variables to enable Claude Code telemetry
// when the monitoring stack is running. These variables configure Claude Code
// to export metrics and logs to the OpenTelemetry Collector.
func OtelEnvVars(containerName string) map[string]string {
	vars := map[string]string{
		// Enable telemetry export
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",

		// Enable metrics and logs export via OTLP
		"OTEL_METRICS_EXPORTER": "otlp",
		"OTEL_LOGS_EXPORTER":    "otlp",

		// OTLP exporter configuration - using HTTP/protobuf with explicit endpoints
		// Uses Docker network hostname since containers share claucker-net
		"OTEL_EXPORTER_OTLP_PROTOCOL":         "http/protobuf",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://otel-collector:4318/v1/metrics",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":    "http://otel-collector:4318/v1/logs",

		// Debug intervals for faster feedback during development
		"OTEL_METRIC_EXPORT_INTERVAL": "10000", // 10 seconds (default: 60000ms)
		"OTEL_LOGS_EXPORT_INTERVAL":   "5000",  // 5 seconds (default: 5000ms)

		// Metrics cardinality control - explicitly enable session tracking
		"OTEL_METRICS_INCLUDE_SESSION_ID":   "true",
		"OTEL_METRICS_INCLUDE_ACCOUNT_UUID": "true",
	}

	// Add container name as a resource attribute for tracking
	if containerName != "" {
		vars["OTEL_RESOURCE_ATTRIBUTES"] = "container.name=" + containerName
	}

	return vars
}

// OtelEnvVarsWithPrompts returns OTEL env vars including user prompt logging.
// WARNING: This includes user prompts in telemetry which may contain sensitive data.
func OtelEnvVarsWithPrompts(containerName string) map[string]string {
	vars := OtelEnvVars(containerName)
	vars["OTEL_LOG_USER_PROMPTS"] = "1"
	return vars
}
