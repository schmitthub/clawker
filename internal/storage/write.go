package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// layerPathForKey finds the layer path that owns a field via provenance.
//
// Resolution:
//  1. Scan provenance for exact or descendant matches in a single pass
//     (e.g. key="build" matches "build" or "build.image"). When multiple
//     entries match, the highest-priority (lowest index) layer wins.
//  2. If no match, walk up ancestor paths stopping only at opaque value
//     fields (e.g. key="env.FOO" walks up to "env" if it's a KindMap).
//     This handles new entries in map[string]string fields whose parent
//     has provenance but the entry itself does not.
func (s *Store[T]) layerPathForKey(key string) string {
	bestIdx := -1
	prefix := key + "."

	// Check exact match and descendant matches.
	for provKey, idx := range s.prov {
		if provKey == key || strings.HasPrefix(provKey, prefix) {
			if bestIdx == -1 || idx < bestIdx {
				bestIdx = idx
			}
		}
	}

	// If no match, walk up ancestor paths. Only stop at ancestors that
	// are opaque value fields (maps, struct slices) — a new entry in an
	// opaque field should route to the layer that owns that field.
	// Struct nesting ancestors are skipped since they don't represent
	// a meaningful write target for leaf entries.
	if bestIdx == -1 {
		for parent := key; ; {
			dot := strings.LastIndex(parent, ".")
			if dot < 0 {
				break
			}
			parent = parent[:dot]
			if idx, ok := s.prov[parent]; ok && isOpaqueField(s.tags, parent) {
				bestIdx = idx
				break // closest opaque ancestor is the most specific match
			}
		}
	}

	if bestIdx >= 0 && bestIdx < len(s.layers) {
		return s.layers[bestIdx].path
	}
	return ""
}

// defaultWritePath returns the fallback write target for fields without
// provenance. Prefers the highest-priority discovered layer, then the
// first explicit path + first filename.
func (s *Store[T]) defaultWritePath() (string, error) {
	// Prefer the highest-priority file-backed layer.
	for _, l := range s.layers {
		if l.path != "" {
			return l.path, nil
		}
	}
	// No file layers — use explicit paths if configured, otherwise CWD.
	if len(s.opts.Filenames) == 0 {
		return "", errors.New("storage: no write path available (no layers or filenames)")
	}
	fname := s.opts.writeFilename()
	if len(s.opts.Paths) > 0 {
		// Explicit dir (e.g. config dir) — no dot prefix.
		dir := s.opts.Paths[0]
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("storage: creating directory %s: %w", dir, err)
		}
		return filepath.Join(dir, fname), nil
	}
	// CWD fallback — apply dual-placement dot prefix if configured.
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("storage: resolving CWD for default write path: %w", err)
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil { //nolint:gosec // config dirs are conventionally world-readable
		return "", fmt.Errorf("storage: creating directory %s: %w", dir, mkErr)
	}
	if s.opts.DotDefault {
		return dualPlacementPath(dir, fname), nil
	}
	return filepath.Join(dir, fname), nil
}

// encodeNode stamps the header comment (when header is non-empty), applies
// block (literal) style to multiline scalars, and encodes the node with 2-space
// indentation. It is the single YAML emitter for the write path — both the
// node-merge writer and the migration re-save route through it so the header is
// emitted consistently. The caller's tree is never mutated — header stamping and
// style changes land on an internal clone.
func encodeNode(node *yaml.Node, header string) ([]byte, error) {
	node = cloneNode(node)
	// Replace any previously-stamped header lines before re-stamping so a
	// re-write never duplicates them. On re-parse yaml.v3 attaches a leading
	// file comment to the first key node, so clean both the mapping node and
	// its first key.
	stripHeaderLines(node, header)
	if len(node.Content) > 0 {
		stripHeaderLines(node.Content[0], header)
	}
	if header != "" {
		node.HeadComment = header
	}
	setLiteralStyle(node)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// stripHeaderLines removes comment lines superseded by the configured header
// from a node's head comment, leaving unrelated comment lines intact. A header
// line containing a colon claims every existing line sharing its `key:`
// directive prefix — so a changed value (e.g. a re-pinned $schema URL written
// by a different binary) is replaced rather than stacked; a header line
// without a colon claims only exact matches. Idempotent — safe on nodes with
// no comment.
func stripHeaderLines(n *yaml.Node, header string) {
	if n == nil || n.HeadComment == "" || header == "" {
		return
	}
	var prefixes, exacts []string
	for hl := range strings.SplitSeq(header, "\n") {
		hl = strings.TrimSpace(hl)
		if hl == "" {
			continue
		}
		if idx := strings.Index(hl, ":"); idx >= 0 {
			prefixes = append(prefixes, hl[:idx+1])
		} else {
			exacts = append(exacts, hl)
		}
	}
	lines := strings.Split(n.HeadComment, "\n")
	kept := lines[:0]
	for _, ln := range lines {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ln), "#"))
		if headerClaimsLine(trimmed, prefixes, exacts) {
			continue
		}
		kept = append(kept, ln)
	}
	n.HeadComment = strings.Join(kept, "\n")
}

// headerClaimsLine reports whether a normalized existing comment line is
// superseded by one of the configured header's directive prefixes or exact
// colon-less lines.
func headerClaimsLine(line string, prefixes, exacts []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return slices.Contains(exacts, line)
}

// setLiteralStyle walks a yaml.Node tree and sets LiteralStyle on string
// scalar nodes that contain newlines. This produces "cmd: |" block scalars
// instead of "cmd: \"line1\\nline2\"" escaped quotes.
func setLiteralStyle(node *yaml.Node) {
	if node == nil {
		return
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && strings.Contains(node.Value, "\n") {
		node.Style = yaml.LiteralStyle
	}
	for _, child := range node.Content {
		setLiteralStyle(child)
	}
}

// configFileMode is the permission for persisted config/settings files —
// world-readable so non-root tooling can read them.
const configFileMode os.FileMode = 0o644

// atomicWrite writes data to path using a temp-file + fsync + rename
// strategy. The temp file is created in the target's parent directory
// to guarantee same-filesystem rename semantics.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("storage: creating directory for %s: %w", path, err)
	}

	tmp, err := os.CreateTemp(dir, ".clawker-*.tmp")
	if err != nil {
		return fmt.Errorf("storage: creating temp file for %s: %w", path, err)
	}

	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("storage: writing temp file for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("storage: syncing temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("storage: closing temp file for %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return fmt.Errorf("storage: setting permissions on %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("storage: renaming temp file to %s: %w", path, err)
	}

	success = true
	return nil
}

// withLock acquires an advisory file lock on path+".lock" before running fn.
// Provides cross-process mutual exclusion for file writes.
func withLock(path string, fn func() error) error {
	fl := flock.New(path + ".lock")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	locked, err := fl.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("storage: acquiring file lock for %s: %w", path, err)
	}
	if !locked {
		return fmt.Errorf("storage: timed out acquiring file lock for %s", path)
	}
	// Unlock error is unactionable in deferred cleanup: the flock is released by
	// the OS on process exit regardless, and the write outcome is already decided.
	defer func() { _ = fl.Unlock() }()

	return fn()
}
