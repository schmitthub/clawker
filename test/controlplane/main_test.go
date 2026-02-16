package controlplane

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/test/harness"
)

func TestMain(m *testing.M) {
	// Initialize real logger with OTEL so CP logs flow to Loki.
	// Host-side collector at localhost:4318 (requires `clawker monitor up`).
	tmpDir, _ := os.MkdirTemp("", "clawker-cp-test-logs")
	logger.NewLogger(&logger.Options{
		LogsDir:     tmpDir,
		ServiceName: "clawker",
		FileConfig:  &logger.LoggingConfig{},
		OtelConfig: &logger.OtelLogConfig{
			Endpoint: "localhost:4318",
			Insecure: true,
		},
	})

	// Overlay console writer for test visibility — OTEL hook is preserved.
	logger.Log = logger.Log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"})

	code := harness.RunTestMain(m)
	logger.Close() // flush OTEL batches before exit
	os.Exit(code)
}
