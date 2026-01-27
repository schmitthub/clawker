package cmdutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompts"
	"github.com/spf13/cobra"
)

// AnnotationRequiresProject is the annotation key for commands that require project context.
const AnnotationRequiresProject = "clawker.requiresProject"

// ErrAborted is returned when user cancels an operation.
var ErrAborted = errors.New("operation aborted by user")

// CommandRequiresProject checks if a command has the requiresProject annotation.
func CommandRequiresProject(cmd *cobra.Command) bool {
	return cmd.Annotations[AnnotationRequiresProject] == "true"
}

// CheckProjectContext verifies we're in a project directory or prompts for confirmation.
// Returns nil to proceed, or ErrAborted if user declines.
func CheckProjectContext(cmd *cobra.Command, f *Factory) error {
	settings, err := f.Settings()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to load settings for project context check")
	}
	projectRoot := FindProjectRoot(f.WorkDir, settings)

	if projectRoot == "" {
		if !ConfirmExternalProjectOperation(f.IOStreams, cmd.InOrStdin(), f.WorkDir, cmd.Name()) {
			return ErrAborted
		}
	}
	return nil
}

// IsProjectDir checks if dir contains clawker.yaml or is a registered project.
// If settings is nil, only checks for clawker.yaml file presence.
func IsProjectDir(dir string, settings *config.Settings) bool {
	// Check for clawker.yaml
	configPath := filepath.Join(dir, config.ConfigFileName)
	if _, err := os.Stat(configPath); err == nil {
		return true
	}

	// Check if registered in settings
	if settings != nil {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return false
		}
		for _, p := range settings.Projects {
			if p == absDir {
				return true
			}
		}
	}

	return false
}

// FindProjectRoot walks up from dir to find the project root.
// Returns the project root path or empty string if not found.
// Project root is determined by:
// 1. Presence of clawker.yaml
// 2. Being a registered project directory in settings
func FindProjectRoot(dir string, settings *config.Settings) string {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}

	// Walk up the directory tree
	current := absDir
	for {
		// Check for clawker.yaml
		configPath := filepath.Join(current, config.ConfigFileName)
		if _, err := os.Stat(configPath); err == nil {
			return current
		}

		// Check if this is a registered project root
		if settings != nil {
			for _, p := range settings.Projects {
				if p == current {
					return current
				}
			}
		}

		// Move up one directory
		parent := filepath.Dir(current)
		if parent == current {
			// Reached filesystem root
			return ""
		}
		current = parent
	}
}

// IsChildOfProject checks if dir is within a registered project directory.
// Returns the project root path if found, empty string otherwise.
func IsChildOfProject(dir string, settings *config.Settings) string {
	if settings == nil {
		return ""
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}

	for _, p := range settings.Projects {
		// Check if absDir is equal to or a child of p
		if absDir == p {
			return p
		}
		// Check if absDir starts with p + separator
		if strings.HasPrefix(absDir, p+string(filepath.Separator)) {
			return p
		}
	}

	return ""
}

// ConfirmExternalProjectOperation prompts user to confirm operation outside project.
// Returns true if user confirms, false otherwise.
// On decline, prints "Aborted." and guidance to stderr.
func ConfirmExternalProjectOperation(ios *iostreams.IOStreams, in io.Reader, projectPath, operation string) bool {
	message := fmt.Sprintf("You are running %s in '%s', which is outside of a project directory.\nDo you want to continue?", operation, projectPath)
	if !prompts.PromptForConfirmation(in, message) {
		fmt.Fprintln(ios.ErrOut, "Aborted.")
		PrintNextSteps(ios, "Run 'clawker init' in the project root to initialize a new project")
		return false
	}
	return true
}
