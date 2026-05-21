package registry

import (
	"errors"
	"fmt"
)

// Sentinel errors for common failure conditions.
var (
	// ErrVersionNotFound indicates a requested version does not exist.
	ErrVersionNotFound = errors.New("version not found")

	// ErrInvalidVersion indicates a malformed version string.
	ErrInvalidVersion = errors.New("invalid semver version")

	// ErrNoVersions indicates no versions matched the criteria.
	ErrNoVersions = errors.New("no versions matched")
)

// NetworkError represents a failure during network operations.
type NetworkError struct {
	URL     string
	Message string
	Err     error
}

func (e *NetworkError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("network request to %s failed: %s: %v", e.URL, e.Message, e.Err)
	}
	return fmt.Sprintf("network request to %s failed: %s", e.URL, e.Message)
}

func (e *NetworkError) Unwrap() error {
	return e.Err
}

// RegistryError represents a failure from the npm registry.
type RegistryError struct {
	Package    string
	StatusCode int
	Message    string
}

func (e *RegistryError) Error() string {
	return fmt.Sprintf("registry error for package %q (status %d): %s", e.Package, e.StatusCode, e.Message)
}

// IsNotFound returns true if the error indicates a package was not found.
func (e *RegistryError) IsNotFound() bool {
	return e.StatusCode == 404
}

// ParseError represents a failure to decode a registry response body that the
// transport returned successfully (HTTP 200). Distinct from NetworkError so
// operators can tell "registry returned non-JSON" — typical of corp proxy
// MITM injecting HTML, captive portals, or DNS hijack — from "couldn't reach
// the registry". Snippet captures the first ~256 bytes of the body as the
// actionable diagnostic.
type ParseError struct {
	URL     string
	Snippet string
	Err     error
}

func (e *ParseError) Error() string {
	if e.Snippet == "" {
		return fmt.Sprintf("parse response from %s: %v", e.URL, e.Err)
	}
	return fmt.Sprintf("parse response from %s: %v (body starts with: %q)", e.URL, e.Err, e.Snippet)
}

func (e *ParseError) Unwrap() error {
	return e.Err
}
