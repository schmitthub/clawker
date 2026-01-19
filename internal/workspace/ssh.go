package workspace

import (
	"os"
	"runtime"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/pkg/logger"
)

// ContainerSSHAgentPath is the path where the SSH agent socket is mounted in the container
const ContainerSSHAgentPath = "/tmp/ssh-agent.sock"

// IsSSHAgentAvailable checks if an SSH agent is available on the host.
// On Linux, it checks if SSH_AUTH_SOCK is set and the socket exists.
// On macOS, we use the host proxy for SSH agent forwarding (avoids permission issues).
func IsSSHAgentAvailable() bool {
	switch runtime.GOOS {
	case "darwin":
		// On macOS, SSH agent is available via the host proxy
		// We don't mount Docker Desktop's socket due to permission issues
		return os.Getenv("SSH_AUTH_SOCK") != ""
	case "linux":
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return false
		}
		// Check if the socket exists
		if _, err := os.Stat(sock); err != nil {
			return false
		}
		return true
	default:
		// Windows and other platforms not supported yet
		return false
	}
}

// UseSSHAgentProxy returns true if SSH agent should be forwarded via the host proxy
// instead of direct socket mounting. This is used on macOS where Docker Desktop
// mounts sockets with root ownership, causing permission issues.
func UseSSHAgentProxy() bool {
	return runtime.GOOS == "darwin" && IsSSHAgentAvailable()
}

// GetSSHAgentMounts returns mount configurations for SSH agent forwarding.
// Returns nil if SSH agent forwarding is not available on this platform
// or if the host proxy should be used instead (macOS).
//
// On Linux, the SSH_AUTH_SOCK socket is bind-mounted into the container.
// On macOS, we don't mount anything - the host proxy handles forwarding.
func GetSSHAgentMounts() []mount.Mount {
	switch runtime.GOOS {
	case "darwin":
		// On macOS, we use the host proxy for SSH agent forwarding
		// This avoids permission issues with Docker Desktop's socket mounting
		logger.Debug().Msg("macOS: using host proxy for SSH agent forwarding")
		return nil
	case "linux":
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			logger.Debug().Msg("SSH_AUTH_SOCK not set, skipping SSH agent mount")
			return nil
		}
		// Verify socket exists
		if _, err := os.Stat(sock); err != nil {
			logger.Debug().Str("socket", sock).Err(err).Msg("SSH agent socket not found")
			return nil
		}
		return []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   sock,
				Target:   ContainerSSHAgentPath,
				ReadOnly: false,
			},
		}
	default:
		logger.Debug().Str("os", runtime.GOOS).Msg("SSH agent forwarding not supported on this platform")
		return nil
	}
}

// GetSSHAgentEnvVar returns the SSH_AUTH_SOCK environment variable value
// to use inside the container. Returns empty string if SSH agent is not available
// or if the host proxy should be used (in which case the entrypoint sets it).
func GetSSHAgentEnvVar() string {
	if !IsSSHAgentAvailable() {
		return ""
	}
	// On macOS, the entrypoint will set SSH_AUTH_SOCK after starting the proxy
	if runtime.GOOS == "darwin" {
		return ""
	}
	return ContainerSSHAgentPath
}
