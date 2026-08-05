package consts

import (
	"os"
)

// Host Docker daemon address resolution.
//
// config.Config.DockerHost composes the address, in docker CLI order with
// clawker's own setting slotted in above the ambient sources the CLI does not
// know about:
//
//  1. $DOCKER_HOST (DockerHostEnv)
//  2. settings.yaml docker.host
//  3. the active docker context (internal/docker/context)
//  4. DefaultDockerHost
//
// The value is an address, scheme and all — unix://, tcp://, https:// — and is
// returned raw: no normalization, no validation. That is the docker CLI's own
// contract for $DOCKER_HOST, and what its contexts store. A caller that needs
// something narrower (a bind-mount source is a path, not an address) takes it
// from the value itself. The mount TARGET is always the conventional location
// DefaultDockerHost names, regardless of where the host serves it.
const (
	// EnvDockerHost is the standard Docker daemon-address override honored
	// by the docker CLI and SDK (e.g. unix:///run/user/1003/docker.sock).
	EnvDockerHost = "DOCKER_HOST"

	// EnvDockerConfig relocates the docker CLI's config directory, which
	// holds both the current-context pointer and the context store.
	EnvDockerConfig = "DOCKER_CONFIG"

	// EnvDockerContext names the active docker context, outranking the one
	// stored in the docker CLI's config file.
	EnvDockerContext = "DOCKER_CONTEXT"

	// DefaultDockerHost is the address the docker CLI's built-in default
	// context resolves to, so falling back to it is parity rather than a
	// guess. It is also where in-container tools expect to find the socket,
	// which makes it the mount target too.
	DefaultDockerHost = "unix:///var/run/docker.sock"
)

// DockerHostEnv returns $DOCKER_HOST verbatim, or "" when unset. It is a dumb
// getter — no scheme validation: a malformed value passes through and fails at
// the point of use, which is the error surface.
func DockerHostEnv() string {
	return os.Getenv(EnvDockerHost)
}
