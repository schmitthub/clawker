package project

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
)

// RegisterProject registers a project in the user's project registry.
// It ensures the settings file exists and calls registryLoader.Register().
// Returns the slug on success. On errors, prints warnings to stderr and returns the error.
func RegisterProject(ios *iostreams.IOStreams, registryLoader config.Registry, workDir string, projectName string) (string, error) {
	cs := ios.ColorScheme()

	// Check if registry loader is available
	if registryLoader == nil {
		logger.Debug().Msg("registry loader is nil, cannot register project")
		fmt.Fprintf(ios.ErrOut, "%s Could not register project in registry: registry not initialized\n", cs.WarningIcon())
		return "", fmt.Errorf("registry not initialized")
	}

	// Ensure settings file exists
	settingsLoader, err := config.NewSettingsLoader()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to create settings loader")
		fmt.Fprintf(ios.ErrOut, "%s Could not access user settings: %v\n", cs.WarningIcon(), err)
		return "", fmt.Errorf("could not access user settings: %w", err)
	}
	if _, err := settingsLoader.EnsureExists(); err != nil {
		logger.Warn().Err(err).Msg("failed to ensure settings file exists")
	}

	// Register the project in the registry
	slug, err := registryLoader.Register(projectName, workDir)
	if err != nil {
		logger.Debug().Err(err).Msg("failed to register project in registry")
		fmt.Fprintf(ios.ErrOut, "%s Could not register project in registry: %v\n", cs.WarningIcon(), err)
		return "", fmt.Errorf("could not register project: %w", err)
	}

	logger.Debug().Str("dir", workDir).Str("slug", slug).Str("name", projectName).Msg("registered project in registry")
	return slug, nil
}
