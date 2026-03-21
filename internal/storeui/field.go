// Package storeui provides a generic TUI for browsing and editing storage.Store[T] instances.
//
// It bridges the gap between typed stores (internal/storage) and terminal presentation
// (internal/tui), enabling interactive field browsing, editing, and layer-targeted saving
// for any store instance.
package storeui

import (
	"fmt"
	"sort"
	"strings"
)

// FieldKind identifies the type of a configuration field for widget selection.
type FieldKind int

const (
	KindText        FieldKind = iota // Single-line string
	KindBool                         // true/false
	KindTriState                     // Deprecated: mapped to KindBool. Retained for iota stability.
	KindSelect                       // Bounded enum with Options
	KindInt                          // Integer
	KindStringSlice                  // Comma-separated string list
	KindDuration                     // time.Duration
	KindComplex                      // Unsupported type — always read-only
)

// Field represents a single editable configuration field discovered via reflection.
type Field struct {
	Path        string             // Dotted YAML path (e.g. "build.image")
	Label       string             // Human-readable label
	Description string             // Help text
	Kind        FieldKind          // Widget type
	Value       string             // Formatted current value
	Default     string             // Effective default shown when Value is "<unset>" or empty
	Options     []string           // For Select fields
	Validator   func(string) error // Optional input validation
	Required    bool               // Whether the field must have a value
	ReadOnly    bool               // Whether the field is not editable
	Order       int                // Sort order (lower = first)
}

// Override allows domain adapters to customize reflected fields by path.
// Pointer fields use nil to mean "don't override" — only non-nil values replace the original.
type Override struct {
	Path        string
	Label       *string
	Description *string
	Default     *string // Effective default shown when value is "<unset>"
	Kind        *FieldKind
	Options     []string           // Replaces options when non-nil
	Validator   func(string) error // Replaces validator when non-nil
	Required    *bool
	ReadOnly    *bool
	Order       *int
	Hidden      bool // When true, removes the field from the list entirely
}

// ApplyOverrides merges overrides into a copy of fields, returning the result sorted by Order.
// Fields matched by a Hidden override are removed. Nil override pointer fields do not clobber
// existing values. Overrides with no matching field path are silently ignored.
func ApplyOverrides(fields []Field, overrides []Override) []Field {
	if len(fields) == 0 {
		return nil
	}

	// Index overrides by path for O(1) lookup. Duplicate paths are a programming error.
	idx := make(map[string]*Override, len(overrides))
	for i := range overrides {
		if _, exists := idx[overrides[i].Path]; exists {
			panic(fmt.Sprintf("storeui.ApplyOverrides: duplicate override path %q", overrides[i].Path))
		}
		idx[overrides[i].Path] = &overrides[i]
	}

	// Collect hidden prefixes for parent-path hiding.
	// A Hidden override for "build.instructions" hides all fields with paths
	// starting with "build.instructions" (exact match or "build.instructions.").
	var hiddenPrefixes []string
	for _, ov := range overrides {
		if ov.Hidden {
			hiddenPrefixes = append(hiddenPrefixes, ov.Path)
		}
	}

	// Build result by copying fields and applying matching overrides.
	result := make([]Field, 0, len(fields))
	for _, f := range fields {
		ov, ok := idx[f.Path]
		if ok && ov.Hidden {
			continue
		}

		// Check if field path is under a hidden parent prefix.
		hidden := false
		for _, prefix := range hiddenPrefixes {
			if f.Path == prefix || strings.HasPrefix(f.Path, prefix+".") {
				hidden = true
				break
			}
		}
		if hidden {
			continue
		}

		// Copy the field (value semantics — don't mutate the original).
		out := f

		if ok {
			if ov.Label != nil {
				out.Label = *ov.Label
			}
			if ov.Description != nil {
				out.Description = *ov.Description
			}
			if ov.Default != nil {
				out.Default = *ov.Default
			}
			if ov.Kind != nil {
				out.Kind = *ov.Kind
			}
			if ov.Options != nil {
				out.Options = ov.Options
			}
			if ov.Validator != nil {
				out.Validator = ov.Validator
			}
			if ov.Required != nil {
				out.Required = *ov.Required
			}
			if ov.ReadOnly != nil {
				out.ReadOnly = *ov.ReadOnly
			}
			if ov.Order != nil {
				out.Order = *ov.Order
			}
		}

		// KindComplex fields are always read-only — enforce the invariant.
		if out.Kind == KindComplex {
			out.ReadOnly = true
		}

		result = append(result, out)
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Order < result[j].Order
	})

	return result
}
