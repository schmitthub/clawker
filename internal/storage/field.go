package storage

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// FieldKind classifies a configuration field's data type for schema consumers
// (TUI editors, doc generators, CLI help).
type FieldKind int

const (
	KindText        FieldKind = iota // string
	KindBool                         // bool or *bool
	KindSelect                       // constrained string (options set by consumer)
	KindInt                          // int, int64
	KindStringSlice                  // []string
	KindDuration                     // time.Duration
	KindMap                          // map[string]string (only — other map types must register via KindFunc)
	KindStructSlice                  // []struct (non-string slice of structs)

	// KindLast is the boundary for storage-defined kinds. Consumer packages
	// define domain-specific kinds starting here:
	//
	//   const KindMyType storage.FieldKind = storage.KindLast + 1
	KindLast
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
	case KindMap:
		return "Map"
	case KindStructSlice:
		return "StructSlice"
	case KindLast:
		return "Last (extension boundary)"
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
	Required() bool      // Whether this field must have a value (from `required:"true"` tag).
	MergeTag() string    // Merge strategy hint (from `merge` tag): "union", "overwrite", or "".
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
	path     string
	kind     FieldKind
	label    string
	desc     string
	def      string
	required bool
	mergeTag string
}

func (f *field) Path() string        { return f.path }
func (f *field) Kind() FieldKind     { return f.kind }
func (f *field) Label() string       { return f.label }
func (f *field) Description() string { return f.desc }
func (f *field) Default() string     { return f.def }
func (f *field) Required() bool      { return f.required }
func (f *field) MergeTag() string    { return f.mergeTag }

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
func NewField(path string, kind FieldKind, label, desc, def string, required bool) Field {
	if path == "" {
		panic("storage.NewField: path must not be empty")
	}
	return &field{path: path, kind: kind, label: label, desc: desc, def: def, required: required, mergeTag: ""}
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

// KindFunc classifies a [reflect.Type] that the normalizer does not recognize.
// Return (kind, true) to claim the type. Return (0, false) to fall through to
// the default panic.
type KindFunc func(reflect.Type) (FieldKind, bool)

// NormalizeOption configures [NormalizeFields] behavior.
type NormalizeOption func(*normalizeOpts)

type normalizeOpts struct {
	kindFunc KindFunc
}

// WithKindFunc registers a classifier for domain-specific types.
// When [NormalizeFields] encounters a Go type it does not recognize, it calls fn
// before panicking. This lets consumer packages define custom [FieldKind] values
// (starting at [KindLast]) without modifying the storage package.
func WithKindFunc(fn KindFunc) NormalizeOption {
	return func(o *normalizeOpts) { o.kindFunc = fn }
}

// NormalizeFields reflects over v's exported struct fields and produces a
// [FieldSet] containing schema metadata derived from struct tags:
//
//   - `yaml:"name"` — dotted path key (falls back to lowercased field name)
//   - `label:"Display Name"` — human-readable label (falls back to YAML key)
//   - `desc:"Help text"` — field description
//   - `default:"value"` — default value hint
//   - `required:"true"` — marks load-bearing fields that must have a value
//
// Go type → [FieldKind] mapping:
//
//   - string → KindText
//   - bool, *bool → KindBool
//   - int, int64 → KindInt
//   - []string → KindStringSlice
//   - time.Duration → KindDuration
//   - struct, *struct → recursed (not a leaf field)
//   - map[string]string → KindMap
//   - []struct → KindStructSlice
//   - unrecognized types → KindFunc (if registered) → panic
//
// NormalizeFields does NOT extract runtime values — only schema metadata.
// Panics if v is not a struct or pointer-to-struct, or if any exported field
// has an unsupported type and no [KindFunc] claims it.
func NormalizeFields[T any](v T, opts ...NormalizeOption) FieldSet {
	var o normalizeOpts
	for _, opt := range opts {
		opt(&o)
	}

	rt := reflect.TypeOf(v)
	if rt == nil {
		panic("storage.NormalizeFields: expected struct or *struct, got nil")
	}

	// Dereference pointer-to-struct.
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}

	if rt.Kind() != reflect.Struct {
		panic("storage.NormalizeFields: expected struct or *struct, got " + rt.Kind().String())
	}

	var fields []Field
	normalizeStruct(rt, "", &fields, o.kindFunc)
	return NewFieldSet(fields)
}

// normalizeStruct walks a struct type's exported fields and appends schema
// metadata to fields. It recurses into nested structs and *structs.
// kindFunc is an optional classifier for domain-specific types (may be nil).
//
// SYNC: storeui.classifyAndFormat has a parallel type-classification switch.
// When adding a new type case here, update classifyAndFormat to match.
func normalizeStruct(rt reflect.Type, prefix string, fields *[]Field, kindFunc KindFunc) {
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
		req := sf.Tag.Get("required") == "true"
		merge := sf.Tag.Get("merge")

		ft := sf.Type

		// Handle pointer types.
		if ft.Kind() == reflect.Ptr {
			elem := ft.Elem()
			if elem.Kind() == reflect.Bool {
				*fields = append(*fields, &field{path: path, kind: KindBool, label: label, desc: desc, def: def, required: req, mergeTag: merge})
				continue
			}
			if elem.Kind() == reflect.Struct {
				normalizeStruct(elem, path, fields, kindFunc)
				continue
			}
			panic(fmt.Sprintf("storage.NormalizeFields: unsupported field type %s at path %q", ft, path))
		}

		// Non-pointer types.
		switch {
		case ft == reflect.TypeFor[time.Duration]():
			*fields = append(*fields, &field{path: path, kind: KindDuration, label: label, desc: desc, def: def, required: req, mergeTag: merge})

		case ft.Kind() == reflect.String:
			*fields = append(*fields, &field{path: path, kind: KindText, label: label, desc: desc, def: def, required: req, mergeTag: merge})

		case ft.Kind() == reflect.Bool:
			*fields = append(*fields, &field{path: path, kind: KindBool, label: label, desc: desc, def: def, required: req, mergeTag: merge})

		case ft.Kind() == reflect.Int, ft.Kind() == reflect.Int64:
			*fields = append(*fields, &field{path: path, kind: KindInt, label: label, desc: desc, def: def, required: req, mergeTag: merge})

		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
			*fields = append(*fields, &field{path: path, kind: KindStringSlice, label: label, desc: desc, def: def, required: req, mergeTag: merge})

		case ft.Kind() == reflect.Struct:
			normalizeStruct(ft, path, fields, kindFunc)
			continue // Struct itself is not a leaf field.

		case ft.Kind() == reflect.Map && ft.Key().Kind() == reflect.String && ft.Elem().Kind() == reflect.String:
			*fields = append(*fields, &field{path: path, kind: KindMap, label: label, desc: desc, def: def, required: req, mergeTag: merge})

		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			*fields = append(*fields, &field{path: path, kind: KindStructSlice, label: label, desc: desc, def: def, required: req, mergeTag: merge})

		default:
			// Try consumer-registered classifier before panicking.
			if kindFunc != nil {
				if kind, ok := kindFunc(ft); ok {
					if kind <= KindLast {
						panic(fmt.Sprintf("storage.NormalizeFields: KindFunc returned storage-defined kind %s for type %s at path %q; consumer kinds must be > KindLast", kind, ft, path))
					}
					*fields = append(*fields, &field{path: path, kind: kind, label: label, desc: desc, def: def, required: req, mergeTag: merge})
					break
				}
			}
			panic(fmt.Sprintf("storage.NormalizeFields: unsupported field type %s at path %q", ft, path))
		}
	}
}
