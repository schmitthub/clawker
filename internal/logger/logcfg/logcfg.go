// Package logcfg assembles the settings-driven host-side logger shared by
// the CLI Factory and the e2e harness. It is a subpackage rather than part
// of internal/logger so the embedded daemon binaries that link the core
// logger never inherit internal/config.
package logcfg

import (
	"fmt"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// New builds the host CLI logger from resolved settings: it honors the
// file-logging switch, rotation knobs, and the OTEL export lane.
func New(cfg config.Config) (*logger.Logger, error) {
	loggingCfg := cfg.LoggingConfig()

	// File logging is on by default for user diagnostics.
	// Only skip if explicitly disabled via settings.yaml.
	if loggingCfg.FileEnabled != nil && !*loggingCfg.FileEnabled {
		return logger.Nop(), nil
	}

	logsDir, err := cfg.LogsSubdir()
	if err != nil {
		return nil, fmt.Errorf("failed to get logs subdir: %w", err)
	}
	monitoringCfg := cfg.MonitoringConfig()

	// Build OTEL config from settings if enabled. The CLI runs on the host
	// and reaches the collector via its host-published OTLP/gRPC port —
	// logger.New uses otlploggrpc (see internal/logger/logger.go::
	// newOtelProvider). Dialing the OtelCollectorPort (HTTP, 4318) with
	// a gRPC exporter returns 415 Unsupported Media Type and silently
	// drops every record; use OtelGRPCPort (4317) instead.
	var otelCfg *logger.OtelOptions
	if loggingCfg.Otel.Enabled != nil && *loggingCfg.Otel.Enabled {
		otelCfg = &logger.OtelOptions{
			Endpoint:       fmt.Sprintf("%s:%d", monitoringCfg.OtelCollectorHost, monitoringCfg.OtelGRPCPort),
			Insecure:       true,
			Timeout:        time.Duration(loggingCfg.Otel.TimeoutSeconds) * time.Second,
			MaxQueueSize:   loggingCfg.Otel.MaxQueueSize,
			ExportInterval: time.Duration(loggingCfg.Otel.ExportIntervalSeconds) * time.Second,
			ServiceName:    consts.TelemetryServiceCLI,

			// The CLI lane is plaintext OTLP on the untrusted receiver —
			// no mTLS material in either shape.
			CACertFile:     "",
			ClientCertFile: "",
			ClientKeyFile:  "",
			TLSConfig:      nil,
		}
	}

	compress := true
	if loggingCfg.Compress != nil {
		compress = *loggingCfg.Compress
	}
	l, err := logger.New(logger.Options{
		LogsDir:    logsDir,
		Filename:   "", // empty selects logger.DefaultLogFileName
		MaxSizeMB:  loggingCfg.MaxSizeMB,
		MaxAgeDays: loggingCfg.MaxAgeDays,
		MaxBackups: loggingCfg.MaxBackups,
		Compress:   compress,
		Otel:       otelCfg,
		EchoStdout: false, // host-side CLI logging never mirrors to stdout
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}
	return l, nil
}
