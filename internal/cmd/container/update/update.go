// Package update provides the container update command.
package update

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// TODO might be able to replace with container opts
// UpdateOptions defines the options for the update command.
type UpdateOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() *config.Config

	Agent              bool
	blkioWeight        uint16
	cpuPeriod          int64
	cpuQuota           int64
	cpuRealtimePeriod  int64
	cpuRealtimeRuntime int64
	cpusetCpus         string
	cpusetMems         string
	cpuShares          int64
	memory             docker.MemBytes
	memoryReservation  docker.MemBytes
	memorySwap         docker.MemSwapBytes
	restartPolicy      string
	pidsLimit          int64
	cpus               docker.NanoCPUs

	nFlag int

	Containers []string
}

// NewCmdUpdate creates a new update command.
func NewCmdUpdate(f *cmdutil.Factory, runF func(context.Context, *UpdateOptions) error) *cobra.Command {
	opts := &UpdateOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "update [OPTIONS] [CONTAINER...]",
		Short: "Update configuration of one or more containers",
		Long: `Update configuration of one or more containers.

This command updates the resource limits of containers that are already running
or have been created but not yet started.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Update memory limit using agent name
  clawker container update --memory 512m --agent ralph

  # Update memory limit by full name
  clawker container update --memory 512m clawker.myapp.ralph

  # Update CPU limit
  clawker container update --cpus 2 --agent ralph

  # Update multiple resources
  clawker container update --cpus 1.5 --memory 1g --agent ralph

  # Update multiple containers
  clawker container update --memory 256m container1 container2`,
		Args: cmdutil.AgentArgsValidator(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Containers = args
			opts.nFlag = cmd.Flags().NFlag()
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return updateRun(cmd.Context(), opts)
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&opts.Agent, "agent", false, "Use agent name (resolves to clawker.<project>.<agent>)")

	flags.Uint16Var(&opts.blkioWeight, "blkio-weight", 0, `Block IO (relative weight), between 10 and 1000, or 0 to disable (default 0)`)
	flags.Int64Var(&opts.cpuPeriod, "cpu-period", 0, "Limit CPU CFS (Completely Fair Scheduler) period")
	flags.Int64Var(&opts.cpuQuota, "cpu-quota", 0, "Limit CPU CFS (Completely Fair Scheduler) quota")
	flags.Int64Var(&opts.cpuRealtimePeriod, "cpu-rt-period", 0, "Limit the CPU real-time period in microseconds")
	_ = flags.SetAnnotation("cpu-rt-period", "version", []string{"1.25"})
	flags.Int64Var(&opts.cpuRealtimeRuntime, "cpu-rt-runtime", 0, "Limit the CPU real-time runtime in microseconds")
	_ = flags.SetAnnotation("cpu-rt-runtime", "version", []string{"1.25"})
	flags.StringVar(&opts.cpusetCpus, "cpuset-cpus", "", "CPUs in which to allow execution (0-3, 0,1)")
	flags.StringVar(&opts.cpusetMems, "cpuset-mems", "", "MEMs in which to allow execution (0-3, 0,1)")
	flags.Int64VarP(&opts.cpuShares, "cpu-shares", "c", 0, "CPU shares (relative weight)")
	flags.VarP(&opts.memory, "memory", "m", "Memory limit")
	flags.Var(&opts.memoryReservation, "memory-reservation", "Memory soft limit")
	flags.Var(&opts.memorySwap, "memory-swap", `Swap limit equal to memory plus swap: -1 to enable unlimited swap`)

	flags.StringVar(&opts.restartPolicy, "restart", "", "Restart policy to apply when a container exits")
	flags.Int64Var(&opts.pidsLimit, "pids-limit", 0, `Tune container pids limit (set -1 for unlimited)`)
	_ = flags.SetAnnotation("pids-limit", "version", []string{"1.40"})

	flags.Var(&opts.cpus, "cpus", "Number of CPUs")
	_ = flags.SetAnnotation("cpus", "version", []string{"1.29"})

	return cmd
}

func updateRun(ctx context.Context, opts *UpdateOptions) error {
	ios := opts.IOStreams

	// Resolve container names
	// When opts.Agent is true, all items in opts.Containers are agent names
	containers := opts.Containers
	if opts.Agent {
		containers = docker.ContainerNamesFromAgents(opts.Config().Resolution.ProjectKey, opts.Containers)
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	// Build update resources
	resources, restartPolicy := buildUpdateResources(opts)

	cs := ios.ColorScheme()
	var errs []error
	for _, name := range containers {
		if err := updateContainer(ctx, ios, client, name, resources, restartPolicy); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), name, err)
		} else {
			fmt.Fprintln(ios.Out, name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to update %d container(s)", len(errs))
	}
	return nil
}

func updateContainer(ctx context.Context, ios *iostreams.IOStreams, client *docker.Client, name string, resources *docker.Resources, restartPolicy *docker.RestartPolicy) error {
	// Find container by name
	c, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Update the container
	resp, err := client.ContainerUpdate(ctx, c.ID, resources, restartPolicy)
	if err != nil {
		return err
	}

	// Check for warnings
	for _, warning := range resp.Warnings {
		fmt.Fprintf(ios.ErrOut, "Warning: %s\n", warning)
	}

	return nil
}

func buildUpdateResources(opts *UpdateOptions) (*docker.Resources, *docker.RestartPolicy) {
	resources := &docker.Resources{}

	// CPU settings - cpus is a NanoCPUs type which is already in nanoseconds
	if opts.cpus.Value() > 0 {
		resources.NanoCPUs = opts.cpus.Value()
	}
	if opts.cpuShares > 0 {
		resources.CPUShares = opts.cpuShares
	}
	if opts.cpusetCpus != "" {
		resources.CpusetCpus = opts.cpusetCpus
	}
	if opts.cpusetMems != "" {
		resources.CpusetMems = opts.cpusetMems
	}

	// Memory settings - memory types are already int64 bytes
	if opts.memory.Value() > 0 {
		resources.Memory = opts.memory.Value()
	}
	if opts.memoryReservation.Value() > 0 {
		resources.MemoryReservation = opts.memoryReservation.Value()
	}
	if opts.memorySwap.Value() != 0 {
		resources.MemorySwap = opts.memorySwap.Value()
	}

	// PIDs limit
	if opts.pidsLimit != 0 {
		resources.PidsLimit = &opts.pidsLimit
	}

	// Block IO weight
	if opts.blkioWeight > 0 {
		resources.BlkioWeight = opts.blkioWeight
	}

	// No restart policy changes in this command
	return resources, nil
}
