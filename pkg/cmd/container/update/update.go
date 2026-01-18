// Package update provides the container update command.
package update

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options defines the options for the update command.
type Options struct {
	CPUs              float64
	CPUShares         int64
	CPUsetCPUs        string
	CPUsetMems        string
	Memory            string
	MemoryReservation string
	MemorySwap        string
	PidsLimit         int64
	BlkioWeight       uint16
}

// NewCmd creates a new update command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "update [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Update configuration of one or more containers",
		Long: `Update configuration of one or more containers.

This command updates the resource limits of containers that are already running
or have been created but not yet started.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Update memory limit
  clawker container update --memory 512m clawker.myapp.ralph

  # Update CPU limit
  clawker container update --cpus 2 clawker.myapp.ralph

  # Update multiple resources
  clawker container update --cpus 1.5 --memory 1g clawker.myapp.ralph

  # Update multiple containers
  clawker container update --memory 256m container1 container2`,
		Annotations: map[string]string{
			cmdutil.AnnotationRequiresProject: "true",
		},
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	cmd.Flags().Float64Var(&opts.CPUs, "cpus", 0, "Number of CPUs")
	cmd.Flags().Int64Var(&opts.CPUShares, "cpu-shares", 0, "CPU shares (relative weight)")
	cmd.Flags().StringVar(&opts.CPUsetCPUs, "cpuset-cpus", "", "CPUs in which to allow execution (0-3, 0,1)")
	cmd.Flags().StringVar(&opts.CPUsetMems, "cpuset-mems", "", "MEMs in which to allow execution (0-3, 0,1)")
	cmd.Flags().StringVarP(&opts.Memory, "memory", "m", "", "Memory limit (e.g., 512m, 1g)")
	cmd.Flags().StringVar(&opts.MemoryReservation, "memory-reservation", "", "Memory soft limit (e.g., 256m)")
	cmd.Flags().StringVar(&opts.MemorySwap, "memory-swap", "", "Swap limit equal to memory plus swap: -1 to enable unlimited swap")
	cmd.Flags().Int64Var(&opts.PidsLimit, "pids-limit", 0, "Tune container pids limit (set -1 for unlimited)")
	cmd.Flags().Uint16Var(&opts.BlkioWeight, "blkio-weight", 0, "Block IO (relative weight), between 10 and 1000, or 0 to disable")

	return cmd
}

func run(_ *cmdutil.Factory, opts *Options, containers []string) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	// Build update resources
	resources, restartPolicy, err := buildUpdateResources(opts)
	if err != nil {
		return err
	}

	var errs []error
	for _, name := range containers {
		if err := updateContainer(ctx, client, name, resources, restartPolicy); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			fmt.Println(name)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to update %d container(s)", len(errs))
	}
	return nil
}

func updateContainer(ctx context.Context, client *docker.Client, name string, resources *container.Resources, restartPolicy *container.RestartPolicy) error {
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
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
	}

	return nil
}

func buildUpdateResources(opts *Options) (*container.Resources, *container.RestartPolicy, error) {
	resources := &container.Resources{}

	// CPU settings
	if opts.CPUs > 0 {
		// Convert CPUs to NanoCPUs (1 CPU = 1e9 NanoCPUs)
		resources.NanoCPUs = int64(opts.CPUs * 1e9)
	}
	if opts.CPUShares > 0 {
		resources.CPUShares = opts.CPUShares
	}
	if opts.CPUsetCPUs != "" {
		resources.CpusetCpus = opts.CPUsetCPUs
	}
	if opts.CPUsetMems != "" {
		resources.CpusetMems = opts.CPUsetMems
	}

	// Memory settings
	if opts.Memory != "" {
		mem, err := parseMemorySize(opts.Memory)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid memory value %q: %w", opts.Memory, err)
		}
		resources.Memory = mem
	}
	if opts.MemoryReservation != "" {
		mem, err := parseMemorySize(opts.MemoryReservation)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid memory-reservation value %q: %w", opts.MemoryReservation, err)
		}
		resources.MemoryReservation = mem
	}
	if opts.MemorySwap != "" {
		if opts.MemorySwap == "-1" {
			resources.MemorySwap = -1
		} else {
			mem, err := parseMemorySize(opts.MemorySwap)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid memory-swap value %q: %w", opts.MemorySwap, err)
			}
			resources.MemorySwap = mem
		}
	}

	// PIDs limit
	if opts.PidsLimit != 0 {
		resources.PidsLimit = &opts.PidsLimit
	}

	// Block IO weight
	if opts.BlkioWeight > 0 {
		resources.BlkioWeight = opts.BlkioWeight
	}

	// No restart policy changes in this command
	return resources, nil, nil
}

// parseMemorySize parses a human-readable memory size (e.g., "512m", "1g", "1024k")
// and returns the size in bytes.
func parseMemorySize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}

	// Check for suffix
	var multiplier int64 = 1
	suffix := s[len(s)-1]
	switch suffix {
	case 'b':
		multiplier = 1
		s = s[:len(s)-1]
	case 'k':
		multiplier = 1024
		s = s[:len(s)-1]
	case 'm':
		multiplier = 1024 * 1024
		s = s[:len(s)-1]
	case 'g':
		multiplier = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	case 't':
		multiplier = 1024 * 1024 * 1024 * 1024
		s = s[:len(s)-1]
	default:
		// No suffix, assume bytes
		if suffix < '0' || suffix > '9' {
			return 0, fmt.Errorf("unknown suffix %q", string(suffix))
		}
	}

	value, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}

	return value * multiplier, nil
}
