// Package storage provides a generic layered YAML store engine.
//
// Both internal/config and internal/project compose a Store[T] with their own
// schema types. The store handles file discovery (static paths or walk-up),
// per-file loading with migrations, N-way merge with provenance tracking,
// and scoped writes with atomic I/O.
package storage

import "errors"

// ErrAnchorNotAncestor reports a walk-up anchor that is neither the current
// working directory nor one of its ancestors. An upward walk from CWD can
// never reach such an anchor and would escape to the filesystem root, so
// discovery refuses it as a caller programming error. The supported
// "no walk-up" case is an empty anchor, which disables walk-up entirely.
var ErrAnchorNotAncestor = errors.New("walk-up anchor is not the current working directory or an ancestor of it")

// ErrSchemaDecode reports that grafting a value at a path produced a merged tree
// that no longer decodes into the schema type T. Set rejects the mutation and
// leaves the tree and snapshot untouched rather than persisting a tree that
// would poison the file on the next Write.
var ErrSchemaDecode = errors.New("value no longer decodes into schema")

// ErrInvalidWriteOption reports a WriteOption that selects neither a target path
// nor a target layer — an unconstructable state from the public ToPath/ToLayer
// constructors, so it signals a programming error.
var ErrInvalidWriteOption = errors.New("invalid WriteOption (no path or layer)")

// ErrMigrationType reports a migration whose store type does not match the
// Store[T] it was wired into (WithMigrations[T] not tied to New[T]'s T).
// Construction aborts rather than silently skipping the legacy-key cleanup.
var ErrMigrationType = errors.New("migration has wrong type for Store[T]")

// ErrNonMappingRoot reports a YAML document whose root node is not a mapping.
// Every layer must be a mapping so paths resolve against keyed fields.
var ErrNonMappingRoot = errors.New("expected a mapping at the document root")
