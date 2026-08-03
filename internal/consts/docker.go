package consts

import (
	"os"
	"strings"
)

// Host Docker daemon socket resolution.
//
// The bind-mount SOURCE for Docker socket mounts must name the socket where
// it actually lives on the host, which is not always the conventional path
// (rootless Docker serves it from $XDG_RUNTIME_DIR/docker.sock; custom
// daemons put it anywhere). Resolution order (docker CLI parity: environment
// beats stored configuration):
//
//  1. $DOCKER_HOST with a unix:// address → its path (DockerHostSocketPath)
//  2. settings.yaml docker.socket (config.Config.DockerSocketPath composes this)
//  3. DefaultDockerSocketPath
//
// The mount TARGET is always DefaultDockerSocketPath — in-container tools
// expect the conventional location regardless of where the host serves it.
const (
	// EnvDockerHost is the standard Docker daemon-address override honored
	// by the docker CLI and SDK (e.g. unix:///run/user/1003/docker.sock).
	EnvDockerHost = "DOCKER_HOST"

	// DefaultDockerSocketPath is the conventional Docker socket path: the
	// host-side fallback when nothing overrides it, and always the
	// in-container mount target.
	DefaultDockerSocketPath = "/var/run/docker.sock"

	// unixSocketScheme prefixes a DOCKER_HOST value that names a host
	// filesystem socket path.
	unixSocketScheme = "unix://"
)

// DockerHostSocketPath returns the host socket path named by $DOCKER_HOST
// and true when the variable holds a unix:// address. Non-unix schemes
// (tcp://, ssh://) return false — they name no host path a bind mount could
// use, so callers fall through to configured/default resolution.
func DockerHostSocketPath() (string, bool) {
	host := os.Getenv(EnvDockerHost)
	path, found := strings.CutPrefix(host, unixSocketScheme)
	if !found || path == "" {
		return "", false
	}
	return path, true
}
