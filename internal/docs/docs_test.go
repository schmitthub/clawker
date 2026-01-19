package docs

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Test command tree for all format tests
// This simulates a clawker-like command hierarchy

func newTestRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "clawker",
		Short: "Claude Code in Docker containers",
		Long:  "Clawker wraps Claude Code in secure Docker containers with a familiar CLI.",
	}

	// Add container command with subcommands
	containerCmd := newTestContainerCmd()
	rootCmd.AddCommand(containerCmd)

	// Add volume command
	volumeCmd := newTestVolumeCmd()
	rootCmd.AddCommand(volumeCmd)

	// Add hidden command (should be skipped in docs)
	hiddenCmd := &cobra.Command{
		Use:    "hidden",
		Short:  "This command is hidden",
		Hidden: true,
	}
	rootCmd.AddCommand(hiddenCmd)

	// Add global flags
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")
	rootCmd.PersistentFlags().StringP("config", "c", "", "Path to config file")

	return rootCmd
}

func newTestContainerCmd() *cobra.Command {
	containerCmd := &cobra.Command{
		Use:     "container",
		Aliases: []string{"c"},
		Short:   "Manage containers",
		Long:    "Manage clawker containers including create, start, stop, and remove operations.",
	}

	// container list
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "ps"},
		Short:   "List containers",
		Long:    "List all clawker-managed containers with their status and metadata.",
		Example: `  # List all containers
  clawker container list

  # List all containers including stopped
  clawker container list --all`,
	}
	listCmd.Flags().BoolP("all", "a", false, "Show all containers (default shows just running)")
	listCmd.Flags().BoolP("quiet", "q", false, "Only display container IDs")
	containerCmd.AddCommand(listCmd)

	// container start
	startCmd := &cobra.Command{
		Use:   "start [CONTAINER]",
		Short: "Start a container",
		Long:  "Start one or more stopped containers.",
		Example: `  # Start by name
  clawker container start clawker.myproject.ralph

  # Start by agent name
  clawker container start --agent ralph`,
	}
	startCmd.Flags().String("agent", "", "Agent name shortcut")
	containerCmd.AddCommand(startCmd)

	// container stop
	stopCmd := &cobra.Command{
		Use:   "stop [CONTAINER]",
		Short: "Stop a container",
		Long:  "Stop one or more running containers.",
	}
	stopCmd.Flags().DurationP("time", "t", 0, "Seconds to wait before killing the container")
	containerCmd.AddCommand(stopCmd)

	return containerCmd
}

func newTestVolumeCmd() *cobra.Command {
	volumeCmd := &cobra.Command{
		Use:   "volume",
		Short: "Manage volumes",
		Long:  "Manage Docker volumes for clawker containers.",
	}

	// volume list
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List volumes",
		Long:    "List all clawker-managed volumes.",
	}
	listCmd.Flags().BoolP("quiet", "q", false, "Only display volume names")
	volumeCmd.AddCommand(listCmd)

	// volume prune
	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove unused volumes",
		Long:  "Remove all unused clawker-managed volumes.",
	}
	pruneCmd.Flags().BoolP("force", "f", false, "Do not prompt for confirmation")
	volumeCmd.AddCommand(pruneCmd)

	return volumeCmd
}

// checkStringContains verifies that got contains expected substring
func checkStringContains(t *testing.T, got, expected string) {
	t.Helper()
	if !strings.Contains(got, expected) {
		t.Errorf("expected output to contain %q, got:\n%s", expected, got)
	}
}

// checkStringOmits verifies that got does not contain unexpected substring
func checkStringOmits(t *testing.T, got, unexpected string) {
	t.Helper()
	if strings.Contains(got, unexpected) {
		t.Errorf("expected output to not contain %q, got:\n%s", unexpected, got)
	}
}
