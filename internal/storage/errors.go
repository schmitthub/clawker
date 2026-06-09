// Package storage provides a generic layered YAML store engine.
//
// Both internal/config and internal/project compose a Store[T] with their own
// schema types. The store handles file discovery (static paths or walk-up),
// per-file loading with migrations, N-way merge with provenance tracking,
// and scoped writes with atomic I/O.
package storage
