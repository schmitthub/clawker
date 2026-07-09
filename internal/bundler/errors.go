// Package bundler owns the composition of clawker container images from
// file-backed harness bundles and stack definitions. It loads and validates
// those formats (harness.yaml bundles and stack.yaml definitions, whose schema
// types live in [github.com/schmitthub/clawker/internal/config]), resolves each
// through its per-layer lookup chain (project registry, harness-embedded,
// shipped-embedded), composes the master Dockerfile template with a bundle's
// block-slot fragment, and renders the two-image base/harness Dockerfile split
// plus its build context. It also resolves harness install versions from
// upstream registries. Leaf package: no Docker client import — build
// orchestration lives in [github.com/schmitthub/clawker/internal/docker].
package bundler

import (
	"errors"

	"github.com/schmitthub/clawker/internal/bundler/registry"
)

// ErrNoBaseImageRef is returned by GenerateHarness when the generator's
// BaseImageRef field was not set by the caller.
var ErrNoBaseImageRef = errors.New("harness image generation requires BaseImageRef (the shared base image tag)")

// ErrUnknownStack is returned when a declared stack name resolves to no
// definition in any layer (project stacks: registry, bundle-embedded, shipped).
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
