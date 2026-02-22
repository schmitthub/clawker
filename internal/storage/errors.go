// Package storage provides a generic layered YAML store engine.
//
// Both internal/config and internal/project compose a Store[T] with their own
// schema types. The store handles file discovery (static paths or walk-up),
// per-file loading with migrations, N-way merge with provenance tracking,
// and scoped writes with atomic I/O.
package storage

import "errors"

// ErrNotInProject is returned when CWD is not within a registered project's
// directory tree. Walk-up discovery falls back to home-level configs only.
var ErrNotInProject = errors.New("storage: CWD is not within a registered project")

// ErrRegistryNotFound is returned when the project registry file cannot be
// located during walk-up discovery. Discovery continues with explicit paths.
var ErrRegistryNotFound = errors.New("storage: project registry not found")
