// Package build provides the image build command.
package build

import (
	"fmt"
	"os"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmd creates the image build command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build an image from a clawker project",
		Long: `Builds a container image from a clawker project configuration.

This is an alias for the top-level 'clawker build' command.

The image is built from the project's clawker.yaml configuration,
generating a Dockerfile and building the image.`,
		Example: `  # Build the project image
  clawker image build

  # Build without Docker cache
  clawker image build --no-cache

  # Build using a custom Dockerfile
  clawker image build --dockerfile ./Dockerfile.dev`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f)
		},
	}

	// Add the same flags as the top-level build command
	cmd.Flags().Bool("no-cache", false, "Build image without using Docker cache")
	cmd.Flags().String("dockerfile", "", "Path to custom Dockerfile (overrides build.dockerfile in config)")

	return cmd
}

func run(_ *cmdutil.Factory) error {
	// Point users to the top-level build command for now
	// TODO: Share implementation with top-level build command
	fmt.Fprintln(os.Stderr, "Please use 'clawker build' for image building.")
	fmt.Fprintln(os.Stderr, "")
	cmdutil.PrintNextSteps(
		"Run 'clawker build' to build the project image",
		"Run 'clawker build --no-cache' to build without cache",
		"Run 'clawker build --help' for more options",
	)
	return nil
}
