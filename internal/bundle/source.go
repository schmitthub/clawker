package bundle

import (
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
)

// Source is a git-generic bundle source: a remote clone spec (URL set, with an
// optional subdir Path and a ref/sha pin) or a local in-place spec (Path alone,
// no URL — loaded from disk with no cache copy, the dev loop). It is the
// resolution-side projection of a config.BundleSource; identity is never drawn
// from it (a bundle's identity comes only from its manifest).
type Source struct {
	URL  string
	Ref  string
	SHA  string
	Path string
}

// SourceFromConfig projects a persisted config.BundleSource into a Source.
func SourceFromConfig(bs config.BundleSource) Source {
	return Source{
		URL:  bs.URL,
		Ref:  bs.Ref,
		SHA:  bs.SHA,
		Path: bs.Path,
	}
}

// IsLocal reports whether the source is a local in-place directory (path alone,
// no URL) — loaded directly from disk, never fetched into the cache.
func (s Source) IsLocal() bool {
	return s.URL == "" && s.Path != ""
}

// Canonical returns the identity-collision key for a REMOTE source: the string
// two declared sources must share to be considered the same source (idempotent
// re-declaration) rather than a C1 collision. It is purely syntactic over the
// declared fields — no fetch, no ref→sha resolution — so drift between a ref and
// the commit it points at is accepted, exactly as the no-lockfile model
// requires. A sha pin supersedes a ref (sha beats ref); a subdir Path
// distinguishes two bundles carved from one repository.
//
// For a LOCAL source the resolver keys claims by the resolved absolute
// directory instead (two spellings of the same directory are the same source);
// the local branch here is a display form for errors only.
func (s Source) Canonical() string {
	if s.IsLocal() {
		return "path:" + filepath.Clean(s.Path)
	}
	return "git:" + s.URL + "//" + s.Path + "@" + s.pin()
}

// pin returns the declared pin segment: "sha:<sha>" (sha beats ref),
// "ref:<ref>", or "" for an unpinned source tracking the default branch. It is
// the per-version fetch key recorded in source.yaml, so different projects
// declaring the same repository at different pins each resolve the versions
// fetched under THEIR pin.
func (s Source) pin() string {
	switch {
	case s.SHA != "":
		return "sha:" + s.SHA
	case s.Ref != "":
		return "ref:" + s.Ref
	}
	return ""
}

// Repository returns the pin-stripped source identity: the clone URL plus the
// subdir Path, without ref/sha. Two remote sources sharing it are the SAME
// source at install time — re-declaring a different pin (ref edit, sha bump)
// updates the cache entry in place rather than colliding, mirroring Claude
// Code's "adding a second marketplace with the same name replaces the first".
// Distinct subdirs of one repository remain distinct sources.
func (s Source) Repository() string {
	if s.IsLocal() {
		return "path:" + filepath.Clean(s.Path)
	}
	return "git:" + s.URL + "//" + s.Path
}
