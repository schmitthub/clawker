package workspace

import (
	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// GitCredentialSetupResult holds the mounts and environment variables
// needed for git credential forwarding.
type GitCredentialSetupResult struct {
	Mounts []mount.Mount
	Env    []string
}

// SetupGitCredentials configures mounts and environment variables for git credential forwarding.
// It returns the mounts to add and environment variables to set based on the config and
// whether the host proxy is running.
func SetupGitCredentials(cfg *config.GitCredentialsConfig, hostProxyRunning bool) GitCredentialSetupResult {
	var result GitCredentialSetupResult

	// HTTPS credential forwarding (requires host proxy)
	if cfg.GitHTTPSEnabled(hostProxyRunning) {
		result.Env = append(result.Env, "CLAWKER_GIT_HTTPS=true")
		logger.Debug().Msg("git HTTPS credential forwarding enabled")
	}

	// SSH agent forwarding
	if cfg.GitSSHEnabled() {
		if IsSSHAgentAvailable() {
			// Add mounts (nil on macOS where we use proxy)
			result.Mounts = append(result.Mounts, GetSSHAgentMounts()...)
			if sshEnv := GetSSHAgentEnvVar(); sshEnv != "" {
				result.Env = append(result.Env, "SSH_AUTH_SOCK="+sshEnv)
			}
			// On macOS, tell entrypoint to use host proxy for SSH forwarding
			if UseSSHAgentProxy() && hostProxyRunning {
				result.Env = append(result.Env, "CLAWKER_SSH_VIA_PROXY=true")
				logger.Debug().Msg("SSH agent forwarding enabled via host proxy")
			} else {
				logger.Debug().Msg("SSH agent forwarding enabled via socket mount")
			}
		} else {
			logger.Debug().Msg("SSH agent not available, skipping SSH forwarding")
		}
	}

	// Git config forwarding
	if cfg.CopyGitConfigEnabled() {
		if GitConfigExists() {
			result.Mounts = append(result.Mounts, GetGitConfigMount()...)
			logger.Debug().Msg("host gitconfig mount enabled")
		}
	}

	return result
}
