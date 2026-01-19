package workspace

import (
	"os"
	"runtime"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/pkg/logger"
)

// ContainerSSHAgentPath is the path where the SSH agent socket is mounted in the container
const ContainerSSHAgentPath = "/tmp/ssh-agent.sock"

// dockerDesktopSSHPath is the magic path Docker Desktop provides for SSH agent forwarding on macOS
const dockerDesktopSSHPath = "/run/host-services/ssh-auth.sock"

// IsSSHAgentAvailable checks if an SSH agent is available on the host.
// On Linux, it checks if SSH_AUTH_SOCK is set and the socket exists.
// On macOS with Docker Desktop, the magic socket is always available if Docker is running.
func IsSSHAgentAvailable() bool {
	switch runtime.GOOS {
	case "darwin":
		// Docker Desktop always provides the SSH agent socket path when running.
		// We can't verify the socket from the host since it only exists inside containers.
		// If Docker Desktop is not running, container creation will fail anyway.
		return true
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

// GetSSHAgentMounts returns mount configurations for SSH agent forwarding.
// Returns nil if SSH agent forwarding is not available on this platform.
//
// On Linux, the SSH_AUTH_SOCK socket is bind-mounted into the container.
// On macOS, Docker Desktop provides a magic socket at /run/host-services/ssh-auth.sock.
func GetSSHAgentMounts() []mount.Mount {
	switch runtime.GOOS {
	case "darwin":
		// Docker Desktop magic socket for SSH agent forwarding
		return []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   dockerDesktopSSHPath,
				Target:   ContainerSSHAgentPath,
				ReadOnly: false,
			},
		}
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
// to use inside the container. Returns empty string if SSH agent is not available.
func GetSSHAgentEnvVar() string {
	if !IsSSHAgentAvailable() {
		return ""
	}
	return ContainerSSHAgentPath
}
