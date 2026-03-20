package firewall

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	fw "github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/cobra"
)

// UpOptions holds the options for the firewall up command.
type UpOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)
	Logger    func() (*logger.Logger, error)
}

// NewCmdUp creates the firewall up command.
// This ensures the firewall daemon is running, then returns immediately.
func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Logger:    firewallLogger(f.Config),
	}

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the firewall daemon",
		Long: `Start the firewall daemon process in the background. This manages the Envoy+CoreDNS container
lifecycle, monitors their health, and auto-exits when no clawker containers are running.

Normally started automatically by container commands when firewall is enabled.
Can also be started manually for debugging or pre-warming.`,
		Example: `  # Start the firewall daemon in the background
  clawker firewall up`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return upRun(cmd.Context(), opts)
		},
	}

	return cmd
}

// NewCmdServe creates the hidden blocking daemon entrypoint.
// This is invoked by the detached firewall startup path.
func NewCmdServe(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Logger:    firewallLogger(f.Config),
	}

	cmd := &cobra.Command{
		Use:    "serve",
		Short:  "Run the firewall daemon",
		Long:   "Internal command that runs the firewall daemon in the foreground.",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return serveRun(cmd.Context(), opts)
		},
	}

	return cmd
}

// firewallLogger returns a logger closure that writes to firewall.log
// instead of the shared clawker.log. The daemon is a long-lived subprocess
// whose logs must be isolated for debugging.
func firewallLogger(cfgFn func() (config.Config, error)) func() (*logger.Logger, error) {
	return func() (*logger.Logger, error) {
		cfg, err := cfgFn()
		if err != nil {
			return nil, fmt.Errorf("loading config for firewall logger: %w", err)
		}
		logsDir, err := cfg.LogsSubdir()
		if err != nil {
			return nil, fmt.Errorf("resolving logs directory: %w", err)
		}
		return logger.New(logger.Options{LogsDir: logsDir, Filename: "firewall.log"})
	}
}

func upRun(_ context.Context, opts *UpOptions) error {
	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}
	defer func() {
		_ = log.Close()
	}()

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := fw.EnsureDaemon(cfg, log); err != nil {
		return fmt.Errorf("starting firewall daemon: %w", err)
	}

	return nil
}

func serveRun(ctx context.Context, opts *UpOptions) error {
	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}
	defer func() {
		_ = log.Close()
	}()

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	daemon, err := fw.NewDaemon(cfg, log)
	if err != nil {
		return fmt.Errorf("initializing firewall daemon: %w", err)
	}

	return daemon.Run(ctx)
}
