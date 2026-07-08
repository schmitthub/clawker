// Package stack provides the `clawker stack` command group: register, list,
// and remove stack definitions in the project's clawker.yaml registry.
package stack

import (
	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
)

// NewCmdStack creates the parent command for stack registry management.
func NewCmdStack(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stack <command>",
		Short: "Manage stack definitions",
		Long: `Manage stack definitions in the project's clawker.yaml registry.

A stack is a reusable collection of Dockerfile instruction injections that
provision a dev-stack (e.g. node = nvm + Node LTS, python = uv + CPython).
Registration points a name at a stack definition directory on disk; the name
can then be declared under build.stacks (or a build.harnesses.<name>.stacks
overlay) to render it into an image.

Clawker ships built-in stacks that resolve without registration; a project
registration under the same name shadows the shipped definition.`,
		Example: `  # Register a stack definition directory
  clawker stack register ./stacks/my-rust

  # Register under an explicit name
  clawker stack register ./vendor/rustup --name rust

  # List registered and built-in stacks
  clawker stack list

  # Remove a project registration
  clawker stack remove my-rust`,
	}

	cmd.AddCommand(
		NewCmdStackRegister(f, nil),
		NewCmdStackList(f, nil),
		NewCmdStackRemove(f, nil),
	)

	return cmd
}
