// Package build provides Docker image generation tooling for Claude Code.
package build

import (
	"github.com/schmitthub/clawker/pkg/build/registry"
)

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
