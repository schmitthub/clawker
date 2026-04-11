package firewall

import (
	"context"
	"fmt"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	fw "github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// DownOptions holds the options for the firewall down command.
type DownOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)
	Firewall  func(context.Context) (fw.FirewallManager, error)
}

// NewCmdDown creates the firewall down command.
func NewCmdDown(f *cmdutil.Factory, runF func(context.Context, *DownOptions) error) *cobra.Command {
	opts := &DownOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Firewall:  f.Firewall,
	}

	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the firewall daemon",
		Long: `Send SIGTERM to the firewall daemon process. The daemon will gracefully
shut down the Envoy and CoreDNS containers before exiting.

No-op if the daemon is not running.`,
		Example: `  # Stop the firewall daemon
  clawker firewall down`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return downRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func downRun(ctx context.Context, opts *DownOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	pidFile, err := cfg.FirewallPIDFilePath()
	if err != nil {
		return fmt.Errorf("resolving PID file path: %w", err)
	}

	if !fw.IsDaemonRunning(pidFile) {
		fmt.Fprintf(ios.Out, "%s Firewall daemon is not running\n", cs.InfoIcon())
	} else {
		if err := fw.StopDaemon(pidFile); err != nil {
			return fmt.Errorf("stopping firewall daemon: %w", err)
		}
		// Wait for the daemon to exit so its Stop() finishes before ours.
		fw.WaitForDaemonExit(pidFile, 10*time.Second)
	}

	// Belt-and-suspenders: stop any remaining firewall containers.
	// The daemon's Stop() should handle this, but if the daemon was started
	// by an older binary (e.g. before eBPF support), it may leave containers behind.
	// Skip when Firewall isn't wired (unit tests with a partial Factory).
	if opts.Firewall != nil {
		if fwMgr, err := opts.Firewall(ctx); err == nil {
			_ = fwMgr.Stop(ctx) // best-effort, ignore "already removed" errors
		}
	}

	fmt.Fprintf(ios.Out, "%s Firewall stopped\n", cs.SuccessIcon())
	return nil
}
