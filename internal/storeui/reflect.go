package storeui

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// WalkFields uses reflection to discover editable fields from a struct value.
// It maps Go types to FieldKind: string→Text, bool→Bool, *bool→Bool, int→Int,
// []string→StringSlice, time.Duration→Duration, map[string]string→Map,
// []struct→StructSlice, nested struct→recurse, nil *struct→recurse zero value.
// Unrecognized types fall back to KindStructSlice (enrichWithSchema overwrites
// the kind from schema metadata afterward).
//
// Accepts both value and pointer types. Uses yaml struct tags for path building.
func WalkFields(v any) []Field {
	if v == nil {
		panic("storeui.WalkFields: v must not be nil")
	}
	val := derefValue(reflect.ValueOf(v))
	if val.Kind() != reflect.Struct {
		panic(fmt.Sprintf("storeui.WalkFields: expected struct or *struct, got %s", val.Kind()))
	}

	var fields []Field
	order := 0
	walkStruct(val, val.Type(), "", &fields, &order)
	return fields
}

// derefValue dereferences a pointer reflect.Value, allocating a zero value if nil.
func derefValue(val reflect.Value) reflect.Value {
	if val.Kind() != reflect.Ptr {
		return val
	}
	if val.IsNil() {
		return reflect.New(val.Type().Elem()).Elem()
	}
	return val.Elem()
}

func walkStruct(val reflect.Value, typ reflect.Type, prefix string, fields *[]Field, order *int) {
	for i := 0; i < typ.NumField(); i++ {
		sf := typ.Field(i)
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

		fv := val.Field(i)
		ft := sf.Type

		// Handle pointer types.
		if ft.Kind() == reflect.Ptr {
			// *bool → Bool (nil treated as false; system handles defaults via nil guards)
			if ft.Elem().Kind() == reflect.Bool {
				val := "false"
				if !fv.IsNil() {
					val = fmt.Sprintf("%v", fv.Elem().Bool())
				}
				*fields = append(*fields, Field{
					Path:  path,
					Label: name,
					Kind:  KindBool,
					Value: val,
					Order: *order,
				})
				*order++
				continue
			}

			// *struct → recurse into zero value if nil
			if ft.Elem().Kind() == reflect.Struct {
				inner := derefValue(fv)
				walkStruct(inner, ft.Elem(), path, fields, order)
				continue
			}

			// Other pointer types — classify as map or struct slice.
			kind, val, editVal := classifyAndFormat(ft, fv)
			*fields = append(*fields, Field{
				Path:      path,
				Label:     name,
				Kind:      kind,
				Value:     val,
				EditValue: editVal,
				Order:     *order,
			})
			*order++
			continue
		}

		// Non-pointer types.
		switch {
		case ft == reflect.TypeOf(time.Duration(0)):
			*fields = append(*fields, Field{
				Path:  path,
				Label: name,
				Kind:  KindDuration,
				Value: fv.Interface().(time.Duration).String(),
				Order: *order,
			})

		case ft.Kind() == reflect.String:
			*fields = append(*fields, Field{
				Path:  path,
				Label: name,
				Kind:  KindText,
				Value: fv.String(),
				Order: *order,
			})

		case ft.Kind() == reflect.Bool:
			*fields = append(*fields, Field{
				Path:  path,
				Label: name,
				Kind:  KindBool,
				Value: fmt.Sprintf("%v", fv.Bool()),
				Order: *order,
			})

		case ft.Kind() == reflect.Int, ft.Kind() == reflect.Int64:
			// time.Duration is int64 but handled above; this catches plain ints.
			*fields = append(*fields, Field{
				Path:  path,
				Label: name,
				Kind:  KindInt,
				Value: fmt.Sprintf("%d", fv.Int()),
				Order: *order,
			})

		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
			parts := make([]string, fv.Len())
			for j := 0; j < fv.Len(); j++ {
				parts[j] = fv.Index(j).String()
			}
			*fields = append(*fields, Field{
				Path:  path,
				Label: name,
				Kind:  KindStringSlice,
				Value: strings.Join(parts, ", "),
				Order: *order,
			})

		case ft.Kind() == reflect.Struct:
			walkStruct(fv, ft, path, fields, order)
			continue // Don't increment order — the struct itself is not a field.

		default:
			// Maps, struct slices, and other types — classify and format.
			kind, val, editVal := classifyAndFormat(ft, fv)
			*fields = append(*fields, Field{
				Path:      path,
				Label:     name,
				Kind:      kind,
				Value:     val,
				EditValue: editVal,
				Order:     *order,
			})
		}
		*order++
	}
}

// yamlTagName extracts the field name from a yaml struct tag.
// E.g. "image,omitempty" → "image", "" → "", "-" → "".
func yamlTagName(tag string) string {
	if tag == "" || tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}

// classifyAndFormat determines the FieldKind and formats browse/edit values
// for map[string]string and []struct types. Unrecognized types fall back to
// KindStructSlice with YAML-marshaled values — enrichWithSchema overwrites
// the kind from the authoritative schema metadata afterward.
//
// SYNC: storage.normalizeStruct has a parallel type-classification switch.
// When adding a new type case here, update normalizeStruct to match.
func classifyAndFormat(ft reflect.Type, fv reflect.Value) (kind FieldKind, browseVal, editVal string) {
	// Dereference pointer for classification.
	elem := ft
	if ft.Kind() == reflect.Ptr {
		elem = ft.Elem()
		if fv.IsNil() {
			fv = reflect.New(elem).Elem()
		} else {
			fv = fv.Elem()
		}
	}

	switch {
	case elem.Kind() == reflect.Map && elem.Key().Kind() == reflect.String && elem.Elem().Kind() == reflect.String:
		kind = KindMap
		browseVal = formatMapSummary(fv)
		editVal = marshalYAMLValue(fv)

	case elem.Kind() == reflect.Slice && elem.Elem().Kind() == reflect.Struct:
		kind = KindStructSlice
		n := fv.Len()
		if n == 0 {
			browseVal = ""
		} else if n == 1 {
			browseVal = "1 item"
		} else {
			browseVal = fmt.Sprintf("%d items", n)
		}
		editVal = marshalYAMLValue(fv)

	default:
		// Unrecognized type — return KindStructSlice as a safe fallback.
		// This is expected when consumers register custom kinds via KindFunc:
		// WalkFields runs before enrichWithSchema, which overwrites the kind
		// from the authoritative schema metadata. The fallback here is temporary.
		kind = KindStructSlice
		editVal = marshalYAMLValue(fv)
	}
	return
}

// formatMapSummary produces a compact "key1=val1, key2=val2" browse summary.
func formatMapSummary(fv reflect.Value) string {
	if fv.IsNil() || fv.Len() == 0 {
		return ""
	}
	n := fv.Len()
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}

// marshalYAMLValue marshals a reflect.Value to a YAML string for editor pre-population.
// Returns empty string for nil/empty values.
// TODO: silently returns "" on yaml.Marshal errors — should propagate or log so
// the user doesn't see an empty editor and unknowingly overwrite existing data.
func marshalYAMLValue(fv reflect.Value) string {
	if !fv.IsValid() {
		return ""
	}
	iface := fv.Interface()
	// Check for nil/empty.
	switch fv.Kind() {
	case reflect.Map:
		if fv.IsNil() || fv.Len() == 0 {
			return ""
		}
		// Sort map keys for stable output.
		if fv.Type().Key().Kind() == reflect.String {
			keys := make([]string, 0, fv.Len())
			for _, k := range fv.MapKeys() {
				keys = append(keys, k.String())
			}
			sort.Strings(keys)
			ordered := make(map[string]any, len(keys))
			for _, k := range keys {
				ordered[k] = fv.MapIndex(reflect.ValueOf(k)).Interface()
			}
			iface = ordered
		}
	case reflect.Slice:
		if fv.IsNil() || fv.Len() == 0 {
			return ""
		}
	}
	data, err := yaml.Marshal(iface)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
