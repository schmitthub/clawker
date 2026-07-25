package storage

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Key addressing: the public API takes keys as explicit segment slices
// (`s.Set([]string{"aliases", "a.b"}, v)`), so a key containing a literal dot
// is addressed exactly and can never be reparsed as nesting — the bug class
// where alias "a.b" corrupted the tree as `aliases: {a: {b: ...}}` is
// structurally impossible.
//
// Internally (dirty tracking, provenance, the tag registry) a key is one flat
// string: the segments joined with pathSep, a control character that cannot
// appear in a YAML mapping key produced by real configs. Schema paths from
// Field.Path() are dotted (schema field names never contain dots), so they
// convert losslessly via strings.Split.

// pathSep joins key segments in internal maps (dirty set, provenance, tag
// registry). ASCII unit separator — never present in real keys, so a joined
// key round-trips to its exact segments.
const pathSep = "\x1f"

// joinKey flattens key segments into the internal representation.
func joinKey(key []string) string {
	return strings.Join(key, pathSep)
}

// splitKey recovers the segments of an internal joined key.
func splitKey(joined string) []string {
	return strings.Split(joined, pathSep)
}

// parentKey returns the joined key of the parent segment path, or "" at the
// root.
func parentKey(joined string) string {
	if idx := strings.LastIndex(joined, pathSep); idx >= 0 {
		return joined[:idx]
	}
	return ""
}

// displayKey renders key segments for error messages and display maps. Dotted
// for readability; display-only, never parsed back.
func displayKey(key []string) string {
	return strings.Join(key, ".")
}

// schemaKey converts a dotted schema path (Field.Path()) to the internal
// joined representation. Schema field names never contain literal dots, so
// the split is exact.
func schemaKey(dotted string) string {
	return joinKey(strings.Split(dotted, "."))
}

// validateKey rejects an empty key or a key with an empty segment, which would
// address or graft an empty-string mapping key and silently write a junk node.
func validateKey(key []string) error {
	if len(key) == 0 {
		return errors.New("storage: empty key")
	}
	if slices.Contains(key, "") {
		return fmt.Errorf("storage: key %q has an empty segment", displayKey(key))
	}
	return nil
}
