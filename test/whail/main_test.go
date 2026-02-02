package whail_test

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestMain(m *testing.M) {
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cli, err := client.New(client.FromEnv)
		if err != nil {
			return
		}
		defer cli.Close()
		_ = cleanupTestImages(ctx, cli)
	}

	// Catch SIGINT/SIGTERM so Ctrl+C still cleans up.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cleanup()
		os.Exit(1)
	}()

	// Clean stale resources from previous runs
	cleanup()

	code := m.Run()

	signal.Stop(sig)
	cleanup()

	os.Exit(code)
}
