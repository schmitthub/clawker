package storage

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// FieldKind classifies a configuration field's data type for schema consumers
// (TUI editors, doc generators, CLI help).
//
// storeui.FieldKind is a type alias for this type.
type FieldKind int

const (
	KindText        FieldKind = iota // string
	KindBool                         // bool or *bool
	KindSelect                       // constrained string (options set by consumer)
	KindInt                          // int, int64
	KindStringSlice                  // []string
	KindDuration                     // time.Duration
	KindComplex                      // map, non-string slice, non-bool/struct pointer, or other non-editable type
)

// String returns the human-readable name of the field kind.
func (k FieldKind) String() string {
	switch k {
	case KindText:
		return "Text"
	case KindBool:
		return "Bool"
	case KindSelect:
		return "Select"
	case KindInt:
		return "Int"
	case KindStringSlice:
		return "StringSlice"
	case KindDuration:
		return "Duration"
	case KindComplex:
		return "Complex"
	default:
		return fmt.Sprintf("FieldKind(%d)", int(k))
	}
}

// Field describes a single configuration field's schema metadata.
// Concrete implementations are created by [NewField] or [NormalizeFields].
type Field interface {
	Path() string        // Dotted YAML path (e.g. "build.image").
	Kind() FieldKind     // Data type classification.
	Label() string       // Short human-readable name (from `label` tag or derived from YAML key).
	Description() string // Help text (from `desc` tag).
	Default() string     // Default value hint (from `default` tag), may be empty.
}

// FieldSet is an ordered, indexed collection of [Field] values.
type FieldSet interface {
	All() []Field                // All fields in discovery order.
	Get(path string) Field       // Lookup by dotted path; returns nil if not found.
	Group(prefix string) []Field // Fields whose path starts with prefix + ".".
	Len() int                    // Number of fields.
}

// Schema is the contract that configuration types implement to expose their
// field metadata. [Store] is constrained to Schema, making field descriptions
// a compile-time requirement for all stored types.
type Schema interface {
	Fields() FieldSet
}

// ---------- concrete implementations ----------

// field is the unexported concrete implementation of [Field].
type field struct {
	path  string
	kind  FieldKind
	label string
	desc  string
	def   string
}

func (f *field) Path() string        { return f.path }
func (f *field) Kind() FieldKind     { return f.kind }
func (f *field) Label() string       { return f.label }
func (f *field) Description() string { return f.desc }
func (f *field) Default() string     { return f.def }

// fieldSet is the unexported concrete implementation of [FieldSet].
type fieldSet struct {
	fields []Field
	index  map[string]Field
}

func (fs *fieldSet) All() []Field {
	out := make([]Field, len(fs.fields))
	copy(out, fs.fields)
	return out
}

func (fs *fieldSet) Get(path string) Field {
	return fs.index[path]
}

func (fs *fieldSet) Group(prefix string) []Field {
	needle := prefix + "."
	var out []Field
	for _, f := range fs.fields {
		if strings.HasPrefix(f.Path(), needle) {
			out = append(out, f)
		}
	}
	return out
}

func (fs *fieldSet) Len() int { return len(fs.fields) }

// ---------- constructors ----------

// NewField creates a [Field] with explicit values. Use this when struct tags
// are not available (e.g. manually-assembled schemas).
func NewField(path string, kind FieldKind, label, desc, def string) Field {
	if path == "" {
		panic("storage.NewField: path must not be empty")
	}
	return &field{path: path, kind: kind, label: label, desc: desc, def: def}
}

// NewFieldSet creates a [FieldSet] from a slice of [Field] values.
// The slice order is preserved by [FieldSet.All].
func NewFieldSet(fields []Field) FieldSet {
	idx := make(map[string]Field, len(fields))
	for _, f := range fields {
		if _, dup := idx[f.Path()]; dup {
			panic("storage.NewFieldSet: duplicate field path " + f.Path())
		}
		idx[f.Path()] = f
	}
	return &fieldSet{fields: fields, index: idx}
}

// ---------- normalizer ----------

// NormalizeFields reflects over v's exported struct fields and produces a
// [FieldSet] containing schema metadata derived from struct tags:
//
//   - `yaml:"name"` — dotted path key (falls back to lowercased field name)
//   - `label:"Display Name"` — human-readable label (falls back to YAML key)
//   - `desc:"Help text"` — field description
//   - `default:"value"` — default value hint
//
// Go type → [FieldKind] mapping:
//
//   - string → KindText
//   - bool, *bool → KindBool
//   - int, int64 → KindInt
//   - []string → KindStringSlice
//   - time.Duration → KindDuration
//   - *bool → KindBool
//   - struct, *struct → recursed (not a leaf field)
//   - *T (all other pointers), map, non-string slice → KindComplex
//
// NormalizeFields does NOT extract runtime values — only schema metadata.
// Panics if v is not a struct or pointer-to-struct.
func NormalizeFields[T any](v T) FieldSet {
	rt := reflect.TypeOf(v)
	if rt == nil {
		panic("storage.NormalizeFields: expected struct or *struct, got nil")
	}

	// Dereference pointer-to-struct.
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}

	if rt.Kind() != reflect.Struct {
		panic("storage.NormalizeFields: expected struct or *struct, got " + rt.Kind().String())
	}

	var fields []Field
	normalizeStruct(rt, "", &fields)
	return NewFieldSet(fields)
}

// normalizeStruct walks a struct type's exported fields and appends schema
// metadata to fields. It recurses into nested structs and *structs.
func normalizeStruct(rt reflect.Type, prefix string, fields *[]Field) {
	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		if !sf.IsExported() {
			continue
		}

		tag := sf.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		name := yamlTagName(tag)
		if name == "" {
			name = strings.ToLower(sf.Name)
		}

		path := name
		if prefix != "" {
			path = prefix + "." + name
		}

		label := sf.Tag.Get("label")
		if label == "" {
			label = name
		}
		desc := sf.Tag.Get("desc")
		def := sf.Tag.Get("default")

		ft := sf.Type

		// Handle pointer types.
		if ft.Kind() == reflect.Pointer {
			elem := ft.Elem()
			if elem.Kind() == reflect.Bool {
				*fields = append(*fields, &field{path: path, kind: KindBool, label: label, desc: desc, def: def})
				continue
			}
			if elem.Kind() == reflect.Struct {
				normalizeStruct(elem, path, fields)
				continue
			}
			// Other pointer types → Complex.
			*fields = append(*fields, &field{path: path, kind: KindComplex, label: label, desc: desc, def: def})
			continue
		}

		// Non-pointer types.
		switch {
		case ft == reflect.TypeFor[time.Duration]():
			*fields = append(*fields, &field{path: path, kind: KindDuration, label: label, desc: desc, def: def})

		case ft.Kind() == reflect.String:
			*fields = append(*fields, &field{path: path, kind: KindText, label: label, desc: desc, def: def})

		case ft.Kind() == reflect.Bool:
			*fields = append(*fields, &field{path: path, kind: KindBool, label: label, desc: desc, def: def})

		case ft.Kind() == reflect.Int, ft.Kind() == reflect.Int64:
			*fields = append(*fields, &field{path: path, kind: KindInt, label: label, desc: desc, def: def})

		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
			*fields = append(*fields, &field{path: path, kind: KindStringSlice, label: label, desc: desc, def: def})

		case ft.Kind() == reflect.Struct:
			normalizeStruct(ft, path, fields)
			continue // Struct itself is not a leaf field.

		default:
			// Maps, non-string slices, interfaces, etc. → Complex.
			*fields = append(*fields, &field{path: path, kind: KindComplex, label: label, desc: desc, def: def})
		}
	}
}
