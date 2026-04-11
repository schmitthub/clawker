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

	// Two paths converge on the same cleanup:
	//  1. daemon running → SIGTERM, wait, run cleanup, report "Firewall stopped"
	//  2. daemon not running (crashed/stale PID) → skip SIGTERM but still run
	//     cleanup so leftover envoy/coredns/ebpf-manager containers don't collide
	//     with the next `firewall up`.
	daemonWasRunning := fw.IsDaemonRunning(pidFile)
	if daemonWasRunning {
		if err := fw.StopDaemon(pidFile); err != nil {
			return fmt.Errorf("stopping firewall daemon: %w", err)
		}
		// Wait for the daemon to exit so its Stop() finishes before ours.
		fw.WaitForDaemonExit(pidFile, 10*time.Second)
	} else {
		fmt.Fprintf(ios.Out, "%s Firewall daemon is not running\n", cs.InfoIcon())
	}

	// Belt-and-suspenders: stop any remaining firewall containers.
	// The daemon's Stop() should have handled this on the running path, and
	// on the not-running path the daemon isn't there to clean up stale
	// containers at all. Either way, this is the last line of defense before
	// the next `firewall up` tries to bind the same ports.
	// Skip when Firewall isn't wired (unit tests with a partial Factory).
	cleanedOrphans := false
	if opts.Firewall != nil {
		if fwMgr, fwErr := opts.Firewall(ctx); fwErr == nil {
			// Detect stale state before Stop() so we can honestly report
			// whether there was anything to clean up on the not-running path.
			hadOrphans := !daemonWasRunning && fwMgr.IsRunning(ctx)
			if err := fwMgr.Stop(ctx); err != nil {
				fmt.Fprintf(ios.ErrOut, "%s firewall cleanup: %v\n", cs.WarningIcon(), err)
			} else if hadOrphans {
				cleanedOrphans = true
			}
		} else {
			fmt.Fprintf(ios.ErrOut, "%s firewall cleanup: %v\n", cs.WarningIcon(), fwErr)
		}
	}

	switch {
	case daemonWasRunning:
		fmt.Fprintf(ios.Out, "%s Firewall stopped\n", cs.SuccessIcon())
	case cleanedOrphans:
		fmt.Fprintf(ios.Out, "%s Removed leftover firewall containers\n", cs.SuccessIcon())
	}
	return nil
}
