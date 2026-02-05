package workspace

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/logger"
)

// ContainerGPGAgentPath is the path where the GPG agent socket is expected in the container.
// GPG by default looks for the socket in ~/.gnupg/S.gpg-agent.
const ContainerGPGAgentPath = "/home/claude/.gnupg/S.gpg-agent"

// IsGPGAgentAvailable checks if a GPG agent with an extra socket is available on the host.
// The extra socket is specifically designed for restricted remote/forwarded access.
// On macOS, we use the host proxy for GPG agent forwarding.
// On Linux, we check if the extra socket exists.
func IsGPGAgentAvailable() bool {
	socketPath := GetGPGExtraSocketPath()
	if socketPath == "" {
		return false
	}

	switch runtime.GOOS {
	case "darwin":
		// On macOS, GPG agent is available via the host proxy
		// We don't mount Docker Desktop's socket due to permission issues
		return true
	case "linux":
		// Check if the socket exists
		if _, err := os.Stat(socketPath); err != nil {
			return false
		}
		return true
	default:
		// Windows and other platforms not supported yet
		return false
	}
}

// GetGPGExtraSocketPath returns the path to the GPG agent's extra socket on the host.
// The extra socket is designed for restricted remote access and is what should be
// forwarded to containers. Returns empty string if gpgconf is not available or fails.
func GetGPGExtraSocketPath() string {
	// Run gpgconf to get the extra socket path
	cmd := exec.Command("gpgconf", "--list-dir", "agent-extra-socket")
	output, err := cmd.Output()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to get GPG extra socket path from gpgconf")
		return ""
	}

	socketPath := strings.TrimSpace(string(output))
	if socketPath == "" {
		logger.Debug().Msg("gpgconf returned empty extra socket path")
		return ""
	}

	return socketPath
}

// UseGPGAgentProxy returns true if GPG agent should be forwarded via the host proxy
// instead of direct socket mounting. This is used on macOS where Docker Desktop
// mounts sockets with root ownership, causing permission issues.
func UseGPGAgentProxy() bool {
	return runtime.GOOS == "darwin" && IsGPGAgentAvailable()
}

// GetGPGAgentMounts returns mount configurations for GPG agent forwarding.
// Returns nil if GPG agent forwarding is not available on this platform
// or if the host proxy should be used instead (macOS).
//
// On Linux, the GPG extra socket is bind-mounted into the container.
// On macOS, we don't mount anything - the host proxy handles forwarding.
func GetGPGAgentMounts() []mount.Mount {
	switch runtime.GOOS {
	case "darwin":
		// On macOS, we use the host proxy for GPG agent forwarding
		// This avoids permission issues with Docker Desktop's socket mounting
		logger.Debug().Msg("macOS: using host proxy for GPG agent forwarding")
		return nil
	case "linux":
		socketPath := GetGPGExtraSocketPath()
		if socketPath == "" {
			logger.Debug().Msg("GPG extra socket not found, skipping GPG agent mount")
			return nil
		}
		// Verify socket exists
		if _, err := os.Stat(socketPath); err != nil {
			logger.Debug().Str("socket", socketPath).Err(err).Msg("GPG agent socket not found")
			return nil
		}
		return []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   socketPath,
				Target:   ContainerGPGAgentPath,
				ReadOnly: false,
			},
		}
	default:
		logger.Debug().Str("os", runtime.GOOS).Msg("GPG agent forwarding not supported on this platform")
		return nil
	}
}
