// Package bridge provides the hidden bridge command group for socket bridge management.
// The bridge serve subcommand is invoked by socketbridge.Manager when spawning
// daemon subprocesses to forward GPG/SSH sockets into containers.
package bridge

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/spf13/cobra"
)

// NewCmdBridge creates the hidden bridge command group.
func NewCmdBridge() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "bridge",
		Short:  "Socket bridge management",
		Long:   "Internal commands for managing socket bridge daemons.",
		Hidden: true,
	}

	cmd.AddCommand(NewCmdBridgeServe())

	return cmd
}

// NewCmdBridgeServe creates the hidden daemon subcommand that runs a socket bridge.
// This is invoked by Manager.EnsureBridge() when spawning a daemon subprocess.
func NewCmdBridgeServe() *cobra.Command {
	var (
		containerID string
		gpgEnabled  bool
		pidFile     string
	)

	cmd := &cobra.Command{
		Use:    "serve",
		Short:  "Run a socket bridge daemon for a container",
		Long:   "Internal command to run a socket bridge daemon that forwards GPG/SSH sockets into a container via docker exec.",
		Hidden: true,
		Example: `  # Start bridge for a container (internal use only)
  clawker bridge serve --container abc123 --gpg
  clawker bridge serve --container abc123 --pid-file /path/to/pid`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if containerID == "" {
				return fmt.Errorf("--container flag is required")
			}

			// Initialize daemon logger
			logger.Init(false)

			logger.Info().
				Str("container", containerID).
				Bool("gpg", gpgEnabled).
				Str("pid_file", pidFile).
				Msg("starting socket bridge daemon")

			// Write PID file
			if pidFile != "" {
				if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
					logger.Error().Err(err).Msg("failed to write PID file")
					return err
				}
				defer os.Remove(pidFile)
			}

			// Create bridge
			bridge := socketbridge.NewBridge(containerID, gpgEnabled)

			// Set up signal handling for graceful shutdown
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
			go func() {
				sig := <-sigCh
				logger.Info().Str("signal", sig.String()).Msg("received shutdown signal")
				bridge.Stop()
				cancel()
			}()

			// Start bridge (blocks until READY message from container)
			if err := bridge.Start(ctx); err != nil {
				logger.Error().Err(err).Msg("bridge start failed")
				return err
			}

			logger.Info().Msg("bridge started, waiting for container exit")

			// Wait for bridge to finish (docker exec EOF / container exit)
			if err := bridge.Wait(); err != nil {
				logger.Error().Err(err).Msg("bridge wait error")
				return err
			}

			logger.Info().Msg("socket bridge daemon stopped")
			return nil
		},
	}

	cmd.Flags().StringVar(&containerID, "container", "", "Container ID to bridge into")
	cmd.Flags().BoolVar(&gpgEnabled, "gpg", false, "Enable GPG agent forwarding")
	cmd.Flags().StringVar(&pidFile, "pid-file", "", "Path to PID file")

	return cmd
}
