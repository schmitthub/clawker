// Package storage provides a generic layered YAML store engine.
//
// Both internal/config and internal/project compose a Store[T] with their own
// schema types. The store handles file discovery (static paths or walk-up),
// per-file loading with migrations, N-way merge with provenance tracking,
// and scoped writes with atomic I/O.
//
// storage is low-level infrastructure: consumers compose a Store[T] behind a
// domain interface (see .claude/rules/store-backed-package.md) rather than
// exposing the store or constructing one at call sites.
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
// leaves the tree and the dirty set untouched rather than persisting a tree
// that would poison the file on the next Write.
var ErrSchemaDecode = errors.New("value no longer decodes into schema")

// ErrMigrationType reports a migration whose store type does not match the
// Store[T] it was wired into (WithMigrations[T] not tied to New[T]'s T).
// Construction aborts rather than silently skipping the legacy-key cleanup.
var ErrMigrationType = errors.New("migration has wrong type for Store[T]")

// ErrKeyNotFound reports a Get or Remove on a key that is absent from the
// merged tree — including a key that is unset (`key:` bare) in every layer,
// since unset keys are transparent and never enter the merge. Callers for
// whom absence is an expected case branch with [errors.Is]; Keys is the
// non-error existence check.
var ErrKeyNotFound = errors.New("key not found")

// ErrNilValue reports a nil (or typed-nil) value passed to Set. No caller
// legitimately sets nil — unsetting a key is Remove's job — so this is a
// caller infraction surfaced as a sentinel; callers translating user input
// skip the Set when the input carries no value.
var ErrNilValue = errors.New("nil value passed to Set (use Remove to unset)")

// ErrUnknownKey reports a Set on a key that is neither a declared schema
// field nor a dynamic entry under a declared map-like field. Set is the
// schema front-door: rejecting unknown keys here is what keeps a typo from
// ever reaching disk.
var ErrUnknownKey = errors.New("key is not declared in the schema")

// ErrNonMappingRoot reports a YAML document whose root node is not a mapping.
// Every layer must be a mapping so paths resolve against keyed fields.
var ErrNonMappingRoot = errors.New("expected a mapping at the document root")

// ErrMultiDocument reports a YAML file containing more than one document.
// Config files are single-document; silently using only the first document
// would drop the rest, so the file is rejected loudly instead.
var ErrMultiDocument = errors.New("expected a single YAML document")
