package controlplane

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/test/harness"
)

func TestMain(m *testing.M) {
	logger.Log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"}).
		With().Timestamp().Logger()

	os.Exit(harness.RunTestMain(m))
}
