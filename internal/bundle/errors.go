package bundle

import (
	"errors"
	"fmt"
)

// ErrNotCached is the sentinel returned when a qualified component address names
// an installed bundle that is declared but not present in the host cache. It is
// wrapped with the remedy naming the install command; callers match it with
// [errors.Is].
var ErrNotCached = errors.New("bundle not cached — run `clawker bundle install`")

// CollisionError is the C1 identity collision: two declared bundle sources whose
// manifests resolve to the same (namespace, name) identity from different
// sources. It names both offending declarations — source coordinate and the
// exact clawker.yaml layer each lives in — so the user can drop one.
type CollisionError struct {
	Identity BundleID
	// AFile / BFile are the resolved clawker.yaml layer paths that declared the
	// two colliding sources.
	AFile string
	BFile string
	// ACanonical / BCanonical are the two differing source coordinates.
	ACanonical string
	BCanonical string
}

func (e *CollisionError) Error() string {
	return fmt.Sprintf(
		"bundle identity %s is declared by two different sources — %s (in %s) and %s (in %s); drop one",
		e.Identity, e.ACanonical, e.AFile, e.BCanonical, e.BFile,
	)
}

// SourceError reports a failure to reach or read a bundle source (an
// unreachable remote, a missing in-place path). It carries the source
// coordinate and the underlying cause. By contract a SourceError never mutates
// or purges the cache — a failed fetch leaves any previously cached version
// serving.
type SourceError struct {
	Source Source
	Err    error
}

func (e *SourceError) Error() string {
	return fmt.Sprintf("bundle source %s: %v", e.Source.Canonical(), e.Err)
}

func (e *SourceError) Unwrap() error { return e.Err }

// ManifestError reports a malformed, missing, or invalid bundle manifest
// (.clawker-bundle/bundle.yaml): unreadable/unparseable file, a missing
// required field, a reserved namespace, or a type mismatch. It is a hard-fail
// at the bundle load front door, distinct from the advisory warnings a
// structurally-valid bundle may still accumulate.
type ManifestError struct {
	// Dir is the bundle directory (or display path) whose manifest failed.
	Dir string
	Err error
}

func (e *ManifestError) Error() string {
	return fmt.Sprintf("bundle %s: %v", e.Dir, e.Err)
}

func (e *ManifestError) Unwrap() error { return e.Err }
