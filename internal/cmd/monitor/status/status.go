package status

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
)

type StatusOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)
	Logger    func() (*logger.Logger, error)
}

func NewCmdStatus(f *cmdutil.Factory, runF func(context.Context, *StatusOptions) error) *cobra.Command {
	opts := &StatusOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Logger:    f.Logger,
	}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show monitoring stack status",
		Long: `Shows the current status of the monitoring stack containers.

Displays running/stopped state and service URLs when the stack is running.`,
		Example: `  # Check monitoring stack status
  clawker monitor status`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return statusRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func statusRun(ctx context.Context, opts *StatusOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	networkName := cfg.ClawkerNetwork()

	// Resolve monitor directory
	monitorDir, err := cfg.MonitorSubdir()
	if err != nil {
		return fmt.Errorf("failed to determine monitor directory: %w", err)
	}

	log.Debug().Str("monitor_dir", monitorDir).Msg("checking monitor stack status")

	// Check if compose.yaml exists
	composePath := monitorDir + "/" + internalmonitor.ComposeFileName
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		fmt.Fprintf(ios.ErrOut, "Monitoring stack: %s\n", cs.Yellow("NOT INITIALIZED"))
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "Run 'clawker monitor init' to scaffold configuration files.")
		return nil
	}

	// Run docker compose ps — bound to ctx so Ctrl+C doesn't leave an
	// orphaned subprocess.
	cmd := exec.CommandContext(
		ctx,
		"docker",
		"compose",
		"-f",
		composePath,
		"ps",
		"--format",
		"table {{.Name}}\t{{.Status}}\t{{.Ports}}",
	)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get container status: %w", err)
	}

	outputStr := strings.TrimSpace(string(output))

	if outputStr == "" || !strings.Contains(outputStr, "Up") {
		fmt.Fprintf(ios.ErrOut, "Monitoring stack: %s\n", cs.Red("STOPPED"))
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "Run 'clawker monitor up' to start the stack.")
		return nil
	}

	fmt.Fprintf(ios.ErrOut, "Monitoring stack: %s\n", cs.Green("RUNNING"))
	fmt.Fprintln(ios.ErrOut)
	fmt.Fprintln(ios.ErrOut, "Containers:")
	fmt.Fprintln(ios.ErrOut, outputStr)
	fmt.Fprintln(ios.ErrOut)

	// Check which services are actually running and print relevant URLs
	mc := cfg.SettingsStore().Read().Monitoring
	fmt.Fprintln(ios.ErrOut, "Service URLs:")
	if strings.Contains(outputStr, consts.MonitoringServiceOpenSearchDashboards) {
		fmt.Fprintf(
			ios.ErrOut,
			"  OpenSearch Dashboards: %s\n",
			cs.Cyan(fmt.Sprintf("http://localhost:%d", mc.OpenSearchDashboardsPort)),
		)
	}
	if strings.Contains(outputStr, consts.MonitoringServiceOpenSearchNode) {
		fmt.Fprintf(
			ios.ErrOut,
			"  OpenSearch API:        %s\n",
			cs.Cyan(fmt.Sprintf("http://localhost:%d", mc.OpenSearchPort)),
		)
	}
	if strings.Contains(outputStr, consts.MonitoringServicePrometheus) {
		fmt.Fprintf(
			ios.ErrOut,
			"  Prometheus:            %s\n",
			cs.Cyan(fmt.Sprintf("http://localhost:%d", mc.PrometheusPort)),
		)
	}

	// Check network status. Any non-success collapses to "(not found)"
	// in the user-visible output — log the underlying err at Debug so a
	// daemon-down / permission-denied case is recoverable from the CP log
	// rather than indistinguishable from "no such network".
	fmt.Fprintln(ios.ErrOut)
	networkCmd := exec.CommandContext(ctx, "docker", "network", "inspect", networkName, "--format", "{{.Name}}")
	if networkOutput, err := networkCmd.Output(); err == nil {
		fmt.Fprintf(ios.ErrOut, "Network: %s %s\n", strings.TrimSpace(string(networkOutput)), cs.Green("(active)"))
	} else {
		log.Debug().Err(err).Str("network", networkName).Msg("docker network inspect failed; reporting as not found")
		fmt.Fprintf(ios.ErrOut, "Network: %s %s\n", networkName, cs.Red("(not found)"))
	}

	return nil
}
