// Package stats provides the container stats command.
package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options defines the options for the stats command.
type Options struct {
	NoStream bool
	NoTrunc  bool
}

// NewCmd creates a new stats command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "stats [OPTIONS] [CONTAINER...]",
		Short: "Display a live stream of container resource usage statistics",
		Long: `Display a live stream of container resource usage statistics.

When no containers are specified, shows stats for all running clawker containers.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Show live stats for all running containers
  clawker container stats

  # Show stats for specific containers
  clawker container stats clawker.myapp.ralph clawker.myapp.writer

  # Show stats once (no streaming)
  clawker container stats --no-stream

  # Show stats once for a specific container
  clawker container stats --no-stream clawker.myapp.ralph`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	cmd.Flags().BoolVar(&opts.NoStream, "no-stream", false, "Disable streaming stats and only pull the first result")
	cmd.Flags().BoolVar(&opts.NoTrunc, "no-trunc", false, "Do not truncate output")

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
			fmt.Fprintln(os.Stderr, "No running containers")
			return nil
		}
	}

	// For non-streaming mode, show stats once
	if opts.NoStream {
		return showStatsOnce(ctx, client, containers, opts)
	}

	// Streaming mode - continuously show stats
	return streamStats(ctx, client, containers, opts)
}

func showStatsOnce(ctx context.Context, client *docker.Client, containers []string, opts *Options) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "CONTAINER ID\tNAME\tCPU %\tMEM USAGE / LIMIT\tMEM %\tNET I/O\tBLOCK I/O\tPIDS")

	for _, name := range containers {
		// Find container by name
		c, err := client.FindContainerByName(ctx, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to find container %q: %v\n", name, err)
			continue
		}
		if c == nil {
			fmt.Fprintf(os.Stderr, "Error: container %q not found\n", name)
			continue
		}

		// Get one-shot stats
		statsReader, err := client.ContainerStatsOneShot(ctx, c.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to get stats for %q: %v\n", name, err)
			continue
		}

		var stats container.StatsResponse
		if err := json.NewDecoder(statsReader.Body).Decode(&stats); err != nil {
			statsReader.Body.Close()
			fmt.Fprintf(os.Stderr, "Error: failed to decode stats for %q: %v\n", name, err)
			continue
		}
		statsReader.Body.Close()

		// Format and print stats
		printStats(w, c.ID, name, &stats, opts)
	}

	return w.Flush()
}

func streamStats(ctx context.Context, client *docker.Client, containers []string, opts *Options) error {
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

	// Start goroutines for each container
	results := make(chan statResult)
	for _, name := range containers {
		go func(containerName string) {
			// Find container by name
			c, err := client.FindContainerByName(ctx, containerName)
			if err != nil {
				results <- statResult{Name: containerName, Err: fmt.Errorf("failed to find container: %w", err)}
				return
			}
			if c == nil {
				results <- statResult{Name: containerName, Err: fmt.Errorf("container not found")}
				return
			}

			// Start streaming stats
			reader, err := client.ContainerStats(ctx, c.ID, true)
			if err != nil {
				results <- statResult{ID: c.ID, Name: containerName, Err: err}
				return
			}
			defer reader.Close()

			decoder := json.NewDecoder(reader)
			for {
				var stats container.StatsResponse
				if err := decoder.Decode(&stats); err != nil {
					if err != io.EOF && ctx.Err() == nil {
						results <- statResult{ID: c.ID, Name: containerName, Err: err}
					}
					return
				}
				results <- statResult{ID: c.ID, Name: containerName, Stats: &stats}
			}
		}(name)
	}

	// Collect and print stats
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	// Track the last stats for each container
	lastStats := make(map[string]*container.StatsResponse)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Print header once
	fmt.Fprintln(w, "CONTAINER ID\tNAME\tCPU %\tMEM USAGE / LIMIT\tMEM %\tNET I/O\tBLOCK I/O\tPIDS")
	w.Flush()

	for {
		select {
		case <-ctx.Done():
			return nil
		case result := <-results:
			if result.Err != nil {
				fmt.Fprintf(os.Stderr, "Error: %s: %v\n", result.Name, result.Err)
				continue
			}
			lastStats[result.Name] = result.Stats
		case <-ticker.C:
			// Clear screen and reprint
			fmt.Print("\033[H\033[2J")
			fmt.Fprintln(w, "CONTAINER ID\tNAME\tCPU %\tMEM USAGE / LIMIT\tMEM %\tNET I/O\tBLOCK I/O\tPIDS")
			for name, stats := range lastStats {
				id := ""
				if c, _ := client.FindContainerByName(ctx, name); c != nil {
					id = c.ID
				}
				printStats(w, id, name, stats, opts)
			}
			w.Flush()
		}
	}
}

func printStats(w *tabwriter.Writer, id, name string, stats *container.StatsResponse, opts *Options) {
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
