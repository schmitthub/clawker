package cmdutil

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/project"
)

// ErrAborted is returned when user cancels an operation.
var ErrAborted = errors.New("operation aborted by user")

// AnnotationRunsAnywhere marks a command that runs outside a registered
// project. Every other command needs the current directory inside a
// registered project root; the root command enforces this in its
// PersistentPreRunE through RequireProject.
const AnnotationRunsAnywhere = "runsAnywhere"

// MarkRunsAnywhere sets AnnotationRunsAnywhere on each command. The mark
// covers the command's subcommands through RunsAnywhere's ancestor walk.
func MarkRunsAnywhere(cmds ...*cobra.Command) {
	for _, cmd := range cmds {
		if cmd.Annotations == nil {
			cmd.Annotations = map[string]string{}
		}
		cmd.Annotations[AnnotationRunsAnywhere] = "true"
	}
}

// RunsAnywhere reports whether cmd or one of its ancestors carries
// AnnotationRunsAnywhere. Cobra's own help and completion commands run
// anywhere.
func RunsAnywhere(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	}
	for c := cmd; c != nil; c = c.Parent() {
		if _, ok := c.Annotations[AnnotationRunsAnywhere]; ok {
			return true
		}
	}
	return false
}

// RequireProject returns an error when the current directory is not inside
// a registered project root. project.ErrNotInProject becomes a user-facing
// error that names the commands that create a registration; other registry
// errors are wrapped.
func RequireProject(registry func() (project.Registry, error)) error {
	if registry == nil {
		return errors.New("project registry not available")
	}
	reg, err := registry()
	if err != nil {
		return fmt.Errorf("loading project registry: %w", err)
	}
	if _, rootErr := reg.CurrentRoot(); rootErr != nil {
		if errors.Is(rootErr, project.ErrNotInProject) {
			return fmt.Errorf(
				"%w: run `clawker init` to create a project here, or `clawker project register` to register this directory",
				rootErr,
			)
		}
		return fmt.Errorf("resolving project root: %w", rootErr)
	}
	return nil
}
