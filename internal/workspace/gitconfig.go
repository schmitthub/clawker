package workspace

import (
	"os"
	"path/filepath"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/pkg/logger"
)

// HostGitConfigStagingPath is where the host's gitconfig is mounted for processing by entrypoint
const HostGitConfigStagingPath = "/tmp/host-gitconfig"

// GetGitConfigMount returns a mount configuration for the host's ~/.gitconfig file.
// The file is mounted read-only to a staging location where the entrypoint script
// can process it (filtering credential.helper lines) before copying to ~/.gitconfig.
//
// Returns nil if ~/.gitconfig doesn't exist on the host.
func GetGitConfigMount() []mount.Mount {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to get user home directory for gitconfig")
		return nil
	}

	gitconfigPath := filepath.Join(homeDir, ".gitconfig")

	// Check if file exists
	info, err := os.Stat(gitconfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Debug().Str("path", gitconfigPath).Msg("host gitconfig not found, skipping mount")
		} else {
			logger.Debug().Str("path", gitconfigPath).Err(err).Msg("failed to stat host gitconfig")
		}
		return nil
	}

	// Ensure it's a regular file, not a directory
	if info.IsDir() {
		logger.Debug().Str("path", gitconfigPath).Msg("host gitconfig is a directory, skipping")
		return nil
	}

	logger.Debug().Str("path", gitconfigPath).Msg("mounting host gitconfig to staging location")

	return []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   gitconfigPath,
			Target:   HostGitConfigStagingPath,
			ReadOnly: true,
		},
	}
}

// GitConfigExists checks if the host has a ~/.gitconfig file
func GitConfigExists() bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	gitconfigPath := filepath.Join(homeDir, ".gitconfig")
	info, err := os.Stat(gitconfigPath)
	if err != nil {
		return false
	}

	return !info.IsDir()
}
