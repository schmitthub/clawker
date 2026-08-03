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
//  1. $DOCKER_HOST, unix:// prefix stripped (DockerHostSocketPath)
//  2. settings.yaml docker.socket (config.Config.DockerSocketPath composes this)
//  3. DefaultDockerSocketPath
//
// No value is validated — a non-path $DOCKER_HOST flows through and the
// Docker daemon rejects the mount naming it, exactly like the CLI's own
// pass-through contract. The mount TARGET is always DefaultDockerSocketPath —
// in-container tools expect the conventional location regardless of where
// the host serves it.
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

// DockerHostSocketPath returns $DOCKER_HOST with a unix:// prefix stripped,
// or "" when unset. It is a dumb getter — no scheme validation: a non-path
// value passes through verbatim and the Docker daemon rejects the mount
// naming that exact value, which is the error surface.
func DockerHostSocketPath() string {
	return strings.TrimPrefix(os.Getenv(EnvDockerHost), unixSocketScheme)
}
