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
