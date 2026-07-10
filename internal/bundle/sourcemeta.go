package bundle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// sourceMetaFile is the cache-internal metadata filename written beside a
// cached bundle's version content roots (<cacheRoot>/<ns>/<name>/source.yaml).
const sourceMetaFile = "source.yaml"

// sourceMeta is the cache-internal record of how a cached bundle was fetched. It
// is engine-owned derived metadata, NOT a lockfile: it links a cached identity
// back to its declared source (for update-compare and the C1 collision key) and
// records each fetched version's resolved commit. It is never read by the
// component resolver — the cached content is authoritative for resolution.
type sourceMeta struct {
	// URL/Ref/SHA/Subdir mirror the declared bundle source that produced this
	// cache entry: a ref source has Ref set, a sha-pinned source has SHA set.
	URL    string `yaml:"url,omitempty"`
	Ref    string `yaml:"ref,omitempty"`
	SHA    string `yaml:"sha,omitempty"`
	Subdir string `yaml:"subdir,omitempty"`
	// Versions maps each fetched version directory name to the commit it was
	// resolved from and when it was fetched.
	Versions map[string]versionMeta `yaml:"versions"`
}

// versionMeta records one fetched version's provenance.
type versionMeta struct {
	SHA       string    `yaml:"sha"`
	FetchedAt time.Time `yaml:"fetched_at"`
}

// newSourceMeta seeds a metadata record from a declared source.
func newSourceMeta(s Source) sourceMeta {
	return sourceMeta{
		URL:      s.URL,
		Ref:      s.Ref,
		SHA:      s.SHA,
		Subdir:   s.Path,
		Versions: map[string]versionMeta{},
	}
}

// source reconstructs the declared Source this cache entry was fetched from, so
// update-compare and the C1 collision key can re-derive its Canonical().
func (m sourceMeta) source() Source {
	return Source{URL: m.URL, Ref: m.Ref, SHA: m.SHA, Path: m.Subdir}
}

// pinned reports whether the source is sha-pinned (never moves on update).
func (m sourceMeta) pinned() bool {
	return m.SHA != "" && m.Ref == ""
}

// latestVersionSHA returns the resolved commit of the most recently fetched
// version, used to detect whether a ref has moved since the last fetch.
func (m sourceMeta) latestVersionSHA() string {
	var newest time.Time
	var sha string
	for _, v := range m.Versions {
		if v.FetchedAt.After(newest) {
			newest = v.FetchedAt
			sha = v.SHA
		}
	}
	return sha
}

// readSourceMeta loads the metadata for a cached bundle. It reports absence
// without error (an installed bundle with no metadata is possible only from a
// hand-placed cache; every engine fetch writes it).
func readSourceMeta(bundleDir string) (sourceMeta, bool, error) {
	raw, err := os.ReadFile(filepath.Join(bundleDir, sourceMetaFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return sourceMeta{}, false, nil
		}
		return sourceMeta{}, false, fmt.Errorf("read %s: %w", sourceMetaFile, err)
	}
	var m sourceMeta
	if unmarshalErr := yaml.Unmarshal(raw, &m); unmarshalErr != nil {
		return sourceMeta{}, false, fmt.Errorf("parse %s in %s: %w", sourceMetaFile, bundleDir, unmarshalErr)
	}
	if m.Versions == nil {
		m.Versions = map[string]versionMeta{}
	}
	return m, true, nil
}

// writeSourceMeta persists a cached bundle's metadata via a temp-file rename so
// a crashed write never leaves a truncated record.
func writeSourceMeta(bundleDir string, m sourceMeta) error {
	raw, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", sourceMetaFile, err)
	}
	final := filepath.Join(bundleDir, sourceMetaFile)
	tmp, err := os.CreateTemp(bundleDir, sourceMetaFile+".*")
	if err != nil {
		return fmt.Errorf("stage %s: %w", sourceMetaFile, err)
	}
	tmpName := tmp.Name()
	if _, writeErr := tmp.Write(raw); writeErr != nil {
		discardTemp(tmp, tmpName)
		return fmt.Errorf("write %s: %w", sourceMetaFile, writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		removeTemp(tmpName)
		return fmt.Errorf("close %s: %w", sourceMetaFile, closeErr)
	}
	if renameErr := os.Rename(tmpName, final); renameErr != nil {
		removeTemp(tmpName)
		return fmt.Errorf("commit %s: %w", sourceMetaFile, renameErr)
	}
	return nil
}

// discardTemp best-effort closes and removes a staged temp file on the error
// path; the cleanup errors are unactionable.
func discardTemp(tmp *os.File, name string) {
	if closeErr := tmp.Close(); closeErr != nil {
		_ = closeErr
	}
	removeTemp(name)
}

// removeTemp best-effort removes a staged temp file; the error is unactionable.
func removeTemp(name string) {
	if rmErr := os.Remove(name); rmErr != nil {
		_ = rmErr
	}
}
