// Package bundler provides Docker image generation tooling for Claude Code.
package bundler

import (
	"errors"

	"github.com/schmitthub/clawker/internal/bundler/registry"
)

// ErrNoBuildImage is returned when no build image is configured and no custom Dockerfile is specified.
var ErrNoBuildImage = errors.New("no build image configured: run 'clawker project init' or 'clawker init' to set up")

// Re-export error types from registry for convenience.
var (
	ErrVersionNotFound = registry.ErrVersionNotFound
	ErrInvalidVersion  = registry.ErrInvalidVersion
	ErrNoVersions      = registry.ErrNoVersions
)

// NetworkError is an alias for registry.NetworkError.
type NetworkError = registry.NetworkError

// RegistryError is an alias for registry.RegistryError.
type RegistryError = registry.RegistryError
