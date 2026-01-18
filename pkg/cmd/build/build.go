package build

import (
	imagebuild "github.com/schmitthub/clawker/pkg/cmd/image/build"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdBuild creates a new build command.
// This is an alias for 'clawker image build'.
func NewCmdBuild(f *cmdutil.Factory) *cobra.Command {
	cmd := imagebuild.NewCmd(f)

	// Update the examples to show the top-level command
	cmd.Example = `  # Build the project image
  clawker build

  # Build without Docker cache
  clawker build --no-cache

  # Build using a custom Dockerfile
  clawker build -f ./Dockerfile.dev

  # Build with multiple tags
  clawker build -t myapp:latest -t myapp:v1.0

  # Build with build arguments
  clawker build --build-arg NODE_VERSION=20

  # Build a specific target stage
  clawker build --target builder

  # Build quietly (suppress output)
  clawker build -q

  # Always pull base image
  clawker build --pull`

	return cmd
}
