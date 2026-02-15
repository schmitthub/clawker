// Package hostproxy provides the hidden host-proxy command group for daemon management.
package hostproxy

import (
	"context"
	"fmt"
	"time"

	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/cobra"
)

// NewCmdServe creates the hidden daemon subcommand that runs the host proxy server.
// This is invoked by Manager.EnsureRunning() when spawning a daemon subprocess.
func NewCmdServe() *cobra.Command {
	opts := hostproxy.DefaultDaemonOptions()

	cmd := &cobra.Command{
		Use:    "serve",
		Short:  "Run the host proxy server as a daemon",
		Long:   "Internal command to run the host proxy server as a background daemon process.",
		Hidden: true,
		Example: `  # Start the host proxy daemon (internal use only)
  clawker host-proxy serve
  clawker host-proxy serve --port 18374
  clawker host-proxy serve --grace-period 2m`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Initialize daemon logger (debug mode disabled for background daemon)
			logger.Init()

			logger.Debug().
				Int("port", opts.Port).
				Str("pid_file", opts.PIDFile).
				Dur("poll_interval", opts.PollInterval).
				Dur("grace_period", opts.GracePeriod).
				Msg("starting host proxy daemon")

			daemon, err := hostproxy.NewDaemon(opts)
			if err != nil {
				logger.Error().Err(err).Msg("failed to create daemon")
				return err
			}

			ctx := context.Background()
			if err := daemon.Run(ctx); err != nil {
				logger.Error().Err(err).Msg("daemon error")
				return err
			}

			logger.Debug().Msg("host proxy daemon stopped")
			return nil
		},
	}

	cmd.Flags().IntVar(&opts.Port, "port", opts.Port, "Port to listen on")
	cmd.Flags().StringVar(&opts.PIDFile, "pid-file", opts.PIDFile, "Path to PID file")
	cmd.Flags().DurationVar(&opts.PollInterval, "poll-interval", opts.PollInterval, "Container poll interval")
	cmd.Flags().DurationVar(&opts.GracePeriod, "grace-period", opts.GracePeriod, "Initial grace period before container checking")

	return cmd
}

// NewCmdHostProxy creates the hidden host-proxy command group.
func NewCmdHostProxy() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "host-proxy",
		Short:  "Host proxy daemon management",
		Long:   "Internal commands for managing the host proxy daemon.",
		Hidden: true,
	}

	cmd.AddCommand(NewCmdServe())
	cmd.AddCommand(NewCmdStatus())
	cmd.AddCommand(NewCmdStop())

	return cmd
}

// NewCmdStatus creates a command to check daemon status.
func NewCmdStatus() *cobra.Command {
	return &cobra.Command{
		Use:    "status",
		Short:  "Check host proxy daemon status",
		Hidden: true,
		Example: `  # Check if the host proxy daemon is running
  clawker host-proxy status`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := hostproxy.DefaultDaemonOptions()
			pid := hostproxy.GetDaemonPID(opts.PIDFile)
			if pid == 0 {
				cmd.Println("Host proxy daemon is not running")
				return nil
			}
			cmd.Printf("Host proxy daemon is running (PID: %d)\n", pid)
			return nil
		},
	}
}

// NewCmdStop creates a command to stop the daemon.
func NewCmdStop() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:    "stop",
		Short:  "Stop the host proxy daemon",
		Hidden: true,
		Example: `  # Send stop signal to the daemon
  clawker host-proxy stop

  # Stop and wait up to 5 seconds for shutdown
  clawker host-proxy stop --wait 5s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := hostproxy.DefaultDaemonOptions()
			if err := hostproxy.StopDaemon(opts.PIDFile); err != nil {
				return err
			}
			cmd.Println("Stop signal sent to host proxy daemon")

			if wait > 0 {
				// Wait for daemon to stop
				deadline := time.Now().Add(wait)
				for time.Now().Before(deadline) {
					if !hostproxy.IsDaemonRunning(opts.PIDFile) {
						cmd.Println("Host proxy daemon stopped")
						return nil
					}
					time.Sleep(100 * time.Millisecond)
				}
				return fmt.Errorf("timeout waiting for daemon to stop after %v", wait)
			}
			return nil
		},
	}

	cmd.Flags().DurationVar(&wait, "wait", 0, "Wait for daemon to stop (0 = don't wait)")

	return cmd
}
