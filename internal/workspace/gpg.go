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
// Returns true if the socket exists and can be mounted into containers.
func IsGPGAgentAvailable() bool {
	if runtime.GOOS == "windows" {
		// Windows not supported yet
		return false
	}

	socketPath := GetGPGExtraSocketPath()
	if socketPath == "" {
		return false
	}

	// Verify socket exists
	if _, err := os.Stat(socketPath); err != nil {
		return false
	}
	return true
}

// GetGPGExtraSocketPath returns the path to the GPG agent's extra socket on the host.
// The extra socket is designed for restricted remote access and is what should be
// forwarded to containers. Returns empty string if gpgconf is not available or fails.
//
// NOTE: A similar function exists in internal/hostproxy/gpg_agent.go (getGPGExtraSocket).
// The duplication is intentional: hostproxy is a server-side package that should not
// import workspace, and workspace provides container configuration logic. Both need
// the socket path but for different purposes (hostproxy for forwarding requests,
// workspace for mount configuration).
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
// instead of direct socket mounting.
//
// Returns false - direct socket mounting now works on Docker Desktop with VirtioFS.
// The proxy code is kept as a fallback for older Docker Desktop versions if needed,
// but is disabled by default.
func UseGPGAgentProxy() bool {
	return false
}

// GetGPGAgentMounts returns mount configurations for GPG agent forwarding.
// Returns nil if GPG agent forwarding is not available on this platform.
//
// The GPG extra socket is bind-mounted into the container. This works on both
// Linux and macOS (Docker Desktop 4.x+ with VirtioFS handles Unix sockets correctly).
func GetGPGAgentMounts() []mount.Mount {
	if runtime.GOOS == "windows" {
		logger.Debug().Msg("GPG agent forwarding not supported on Windows")
		return nil
	}

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
}
