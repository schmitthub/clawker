// Package bundler provides Docker image generation tooling for coding-agent harnesses.
package bundler

import (
	"errors"

	"github.com/schmitthub/clawker/internal/bundler/registry"
)

// ErrNoBaseImageRef is returned by GenerateHarness when the generator's
// BaseImageRef field was not set by the caller.
var ErrNoBaseImageRef = errors.New("harness image generation requires BaseImageRef (the shared base image tag)")

// ErrUnknownStack is returned when a declared stack name resolves to
// no definition in any source (shipped, settings registry, bundle-embedded).
var ErrUnknownStack = errors.New("unknown stack")

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

// ParseError is an alias for registry.ParseError.
type ParseError = registry.ParseError
