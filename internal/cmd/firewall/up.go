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
// This is the daemon entry point — it blocks until signal or auto-exit.
func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command {
	opts := &UpOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Logger:    f.Logger,
	}

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Run the firewall daemon",
		Long: `Start the firewall daemon process. This manages the Envoy+CoreDNS container
lifecycle, monitors their health, and auto-exits when no clawker containers are running.

Normally started automatically by container commands when firewall is enabled.
Can also be started manually for debugging.`,
		Example: `  # Start the firewall daemon (blocks)
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

func upRun(ctx context.Context, opts *UpOptions) error {
	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}

	daemon, err := fw.NewDaemon(cfg, log)
	if err != nil {
		return fmt.Errorf("initializing firewall daemon: %w", err)
	}

	return daemon.Run(ctx)
}
