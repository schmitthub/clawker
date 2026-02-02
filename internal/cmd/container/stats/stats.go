// Package stats provides the container stats command.
package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// StatsOptions defines the options for the stats command.
type StatsOptions struct {
	IOStreams  *iostreams.IOStreams
	Client     func(context.Context) (*docker.Client, error)
	Config func() *config.Config

	Agent      bool // if set to true, treat arguments as agent name
	NoStream   bool
	NoTrunc    bool
	Containers []string
}

// NewCmdStats creates a new stats command.
func NewCmdStats(f *cmdutil.Factory, runF func(context.Context, *StatsOptions) error) *cobra.Command {
	opts := &StatsOptions{
		IOStreams:  f.IOStreams,
		Client:     f.Client,
		Config: f.Config,
	}

	cmd := &cobra.Command{
		Use:   "stats [OPTIONS] [CONTAINER...]",
		Short: "Display a live stream of container resource usage statistics",
		Long: `Display a live stream of container resource usage statistics.

When no containers are specified, shows stats for all running clawker containers.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Show live stats for all running containers
  clawker container stats

  # Show stats using agent name
  clawker container stats --agent ralph

  # Show stats for specific containers
  clawker container stats clawker.myapp.ralph clawker.myapp.writer

  # Show stats once (no streaming)
  clawker container stats --no-stream

  # Show stats once for a specific container
  clawker container stats --no-stream --agent ralph`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Containers = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return statsRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVar(&opts.NoStream, "no-stream", false, "Disable streaming stats and only pull the first result")
	cmd.Flags().BoolVar(&opts.NoTrunc, "no-trunc", false, "Do not truncate output")

	return cmd
}

func statsRun(ctx context.Context, opts *StatsOptions) error {
	ios := opts.IOStreams

	// Resolve container names if --agent provided
	containers := opts.Containers
	if opts.Agent {
		containers = docker.ContainerNamesFromAgents(opts.Config().Resolution().ProjectKey, containers)
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// If no containers specified, get all running containers
	if len(containers) == 0 {
		running, err := client.ContainerListRunning(ctx)
		if err != nil {
			return fmt.Errorf("failed to list containers: %w", err)
		}
		for _, c := range running {
			if len(c.Names) > 0 {
				// Remove leading slash from container name
				name := c.Names[0]
				if len(name) > 0 && name[0] == '/' {
					name = name[1:]
				}
				containers = append(containers, name)
			}
		}
		if len(containers) == 0 {
			fmt.Fprintln(ios.ErrOut, "No running containers")
			return nil
		}
	}

	// For non-streaming mode, show stats once
	if opts.NoStream {
		return showStatsOnce(ctx, ios, client, containers, opts)
	}

	// Streaming mode - continuously show stats
	return streamStats(ctx, ios, client, containers, opts)
}

func showStatsOnce(ctx context.Context, ios *iostreams.IOStreams, client *docker.Client, containers []string, opts *StatsOptions) error {
	w := tabwriter.NewWriter(ios.Out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "CONTAINER ID\tNAME\tCPU %\tMEM USAGE / LIMIT\tMEM %\tNET I/O\tBLOCK I/O\tPIDS")

	var errs []error
	for _, name := range containers {
		// Find container by name
		c, err := client.FindContainerByName(ctx, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find container %q: %w", name, err))
			fmt.Fprintf(ios.ErrOut, "Error: failed to find container %q: %v\n", name, err)
			continue
		}
		if c == nil {
			errs = append(errs, fmt.Errorf("container %q not found", name))
			fmt.Fprintf(ios.ErrOut, "Error: container %q not found\n", name)
			continue
		}

		// Get one-shot stats
		statsReader, err := client.ContainerStatsOneShot(ctx, c.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get stats for %q: %w", name, err))
			fmt.Fprintf(ios.ErrOut, "Error: failed to get stats for %q: %v\n", name, err)
			continue
		}

		var stats container.StatsResponse
		if err := json.NewDecoder(statsReader.Body).Decode(&stats); err != nil {
			statsReader.Body.Close()
			errs = append(errs, fmt.Errorf("failed to decode stats for %q: %w", name, err))
			fmt.Fprintf(ios.ErrOut, "Error: failed to decode stats for %q: %v\n", name, err)
			continue
		}
		statsReader.Body.Close()

		// Format and print stats
		printStats(w, c.ID, name, &stats, opts)
	}

	if err := w.Flush(); err != nil {
		return fmt.Errorf("failed to flush output: %w", err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to get stats for %d container(s)", len(errs))
	}
	return nil
}

func streamStats(ctx context.Context, ios *iostreams.IOStreams, client *docker.Client, containers []string, opts *StatsOptions) error {
	// Create a cancellable context
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel to collect stats from all containers
	type statResult struct {
		ID    string
		Name  string
		Stats *container.StatsResponse
		Err   error
	}

	// Use buffered channel to prevent goroutine blocking when context is cancelled
	results := make(chan statResult, len(containers)*2)

	// Pre-resolve container IDs to avoid repeated lookups in the display loop
	containerIDs := make(map[string]string)
	for _, name := range containers {
		c, err := client.FindContainerByName(ctx, name)
		if err != nil {
			fmt.Fprintf(ios.ErrOut, "Warning: failed to resolve container %q: %v\n", name, err)
			continue
		}
		if c == nil {
			fmt.Fprintf(ios.ErrOut, "Warning: container %q not found\n", name)
			continue
		}
		containerIDs[name] = c.ID
	}

	if len(containerIDs) == 0 {
		return fmt.Errorf("no valid containers found")
	}

	// Start goroutines for each container
	for name, id := range containerIDs {
		go func(containerName, containerID string) {
			// Start streaming stats
			reader, err := client.ContainerStats(ctx, containerID, true)
			if err != nil {
				select {
				case results <- statResult{ID: containerID, Name: containerName, Err: err}:
				case <-ctx.Done():
				}
				return
			}
			defer reader.Body.Close()

			decoder := json.NewDecoder(reader.Body)
			for {
				var stats container.StatsResponse
				if err := decoder.Decode(&stats); err != nil {
					if err != io.EOF && ctx.Err() == nil {
						select {
						case results <- statResult{ID: containerID, Name: containerName, Err: err}:
						case <-ctx.Done():
						}
					}
					return
				}
				select {
				case results <- statResult{ID: containerID, Name: containerName, Stats: &stats}:
				case <-ctx.Done():
					return
				}
			}
		}(name, id)
	}

	// Collect and print stats
	w := tabwriter.NewWriter(ios.Out, 0, 0, 3, ' ', 0)

	// Track the last stats for each container
	lastStats := make(map[string]*container.StatsResponse)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Print header once
	fmt.Fprintln(w, "CONTAINER ID\tNAME\tCPU %\tMEM USAGE / LIMIT\tMEM %\tNET I/O\tBLOCK I/O\tPIDS")
	if err := w.Flush(); err != nil {
		fmt.Fprintf(ios.ErrOut, "Warning: output flush failed: %v\n", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case result := <-results:
			if result.Err != nil {
				fmt.Fprintf(ios.ErrOut, "Error: %s: %v\n", result.Name, result.Err)
				continue
			}
			lastStats[result.Name] = result.Stats
		case <-ticker.C:
			// Clear screen and reprint
			fmt.Fprint(ios.Out, "\033[H\033[2J")
			fmt.Fprintln(w, "CONTAINER ID\tNAME\tCPU %\tMEM USAGE / LIMIT\tMEM %\tNET I/O\tBLOCK I/O\tPIDS")
			for name, stats := range lastStats {
				id := containerIDs[name]
				printStats(w, id, name, stats, opts)
			}
			if err := w.Flush(); err != nil {
				fmt.Fprintf(ios.ErrOut, "Warning: output flush failed: %v\n", err)
			}
		}
	}
}

func printStats(w *tabwriter.Writer, id, name string, stats *container.StatsResponse, opts *StatsOptions) {
	// Format container ID
	containerID := id
	if !opts.NoTrunc && len(containerID) > 12 {
		containerID = containerID[:12]
	}

	// Calculate CPU percentage
	cpuPercent := calculateCPUPercent(stats)

	// Calculate memory percentage and format usage
	memUsage := stats.MemoryStats.Usage
	memLimit := stats.MemoryStats.Limit
	memPercent := 0.0
	if memLimit > 0 {
		memPercent = float64(memUsage) / float64(memLimit) * 100.0
	}
	memStr := fmt.Sprintf("%s / %s", formatBytes(memUsage), formatBytes(memLimit))

	// Calculate network I/O
	var rxBytes, txBytes uint64
	for _, netStats := range stats.Networks {
		rxBytes += netStats.RxBytes
		txBytes += netStats.TxBytes
	}
	netStr := fmt.Sprintf("%s / %s", formatBytes(rxBytes), formatBytes(txBytes))

	// Calculate block I/O
	var blkRead, blkWrite uint64
	for _, entry := range stats.BlkioStats.IoServiceBytesRecursive {
		switch entry.Op {
		case "read", "Read":
			blkRead += entry.Value
		case "write", "Write":
			blkWrite += entry.Value
		}
	}
	blkStr := fmt.Sprintf("%s / %s", formatBytes(blkRead), formatBytes(blkWrite))

	// Get PIDs
	pids := stats.PidsStats.Current

	fmt.Fprintf(w, "%s\t%s\t%.2f%%\t%s\t%.2f%%\t%s\t%s\t%d\n",
		containerID, name, cpuPercent, memStr, memPercent, netStr, blkStr, pids)
}

func calculateCPUPercent(stats *container.StatsResponse) float64 {
	// Calculate the change for the CPU usage of the container between readings
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)

	// Calculate the change for the entire system between readings
	systemDelta := float64(stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage)

	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent := (cpuDelta / systemDelta) * float64(stats.CPUStats.OnlineCPUs) * 100.0
		return cpuPercent
	}
	return 0.0
}

func formatBytes(bytes uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.2fTB", float64(bytes)/TB)
	case bytes >= GB:
		return fmt.Sprintf("%.2fGB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2fMB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2fKB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
