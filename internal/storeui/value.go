package storeui

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// durationKind is the reflected type of time.Duration, stored for comparison convenience.
var durationKind = reflect.TypeOf(time.Duration(0))

// SetFieldValue sets a field on a struct pointer by its dotted YAML path.
// It walks the struct via yaml tags, allocates nil pointer-to-struct parents,
// and performs type-aware conversion at the leaf.
//
// v must be a non-nil pointer to a struct. Panics otherwise.
func SetFieldValue(v any, path string, val string) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		panic("storeui.SetFieldValue: v must be a non-nil pointer to a struct")
	}
	if rv.Elem().Kind() != reflect.Struct {
		panic(fmt.Sprintf("storeui.SetFieldValue: v must point to a struct, got *%s", rv.Elem().Kind()))
	}
	if path == "" {
		return fmt.Errorf("storeui.SetFieldValue: path must not be empty")
	}

	segments := strings.Split(path, ".")
	current := rv.Elem()

	// Walk to the parent of the target field, allocating nil *struct parents.
	for _, seg := range segments[:len(segments)-1] {
		idx, ok := findFieldByYAMLTag(current, seg)
		if !ok {
			return fmt.Errorf("storeui.SetFieldValue: field %q not found in path %q", seg, path)
		}

		f := current.Field(idx)

		// Allocate nil pointer-to-struct parents.
		if f.Kind() == reflect.Ptr {
			if f.IsNil() {
				f.Set(reflect.New(f.Type().Elem()))
			}
			f = f.Elem()
		}

		if f.Kind() != reflect.Struct {
			return fmt.Errorf("storeui.SetFieldValue: intermediate path segment %q is not a struct in path %q", seg, path)
		}
		current = f
	}

	// Set the leaf field.
	leaf := segments[len(segments)-1]
	idx, ok := findFieldByYAMLTag(current, leaf)
	if !ok {
		return fmt.Errorf("storeui.SetFieldValue: field %q not found in path %q", leaf, path)
	}

	f := current.Field(idx)
	return setLeaf(f, val, path)
}

// findFieldByYAMLTag finds a struct field by its yaml tag name.
func findFieldByYAMLTag(v reflect.Value, name string) (int, bool) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		tag := sf.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		tagName := yamlTagName(tag)
		if tagName == "" {
			tagName = strings.ToLower(sf.Name)
		}
		if tagName == name {
			return i, true
		}
	}
	return 0, false
}

// setLeaf sets a reflect.Value from a string, handling all supported FieldKind types.
func setLeaf(f reflect.Value, val string, path string) error {
	if !f.CanSet() {
		return fmt.Errorf("storeui.SetFieldValue: field at %q is not settable", path)
	}
	ft := f.Type()

	// Handle pointer types.
	if ft.Kind() == reflect.Ptr {
		// *bool → Bool (always allocate a non-nil pointer)
		if ft.Elem().Kind() == reflect.Bool {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("storeui.SetFieldValue: invalid bool for %q: %w", path, err)
			}
			pv := reflect.New(ft.Elem())
			pv.Elem().SetBool(b)
			f.Set(pv)
			return nil
		}

		// Other pointer types — try YAML unmarshal for maps, struct slices, etc.
		return setLeafYAML(f, ft, val, path)
	}

	// Non-pointer types.
	switch {
	case ft == durationKind:
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("storeui.SetFieldValue: invalid duration for %q: %w", path, err)
		}
		f.Set(reflect.ValueOf(d))

	case ft.Kind() == reflect.String:
		f.SetString(val)

	case ft.Kind() == reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("storeui.SetFieldValue: invalid bool for %q: %w", path, err)
		}
		f.SetBool(b)

	case ft.Kind() == reflect.Int, ft.Kind() == reflect.Int64:
		n, err := strconv.ParseInt(val, 10, ft.Bits())
		if err != nil {
			return fmt.Errorf("storeui.SetFieldValue: invalid int for %q: %w", path, err)
		}
		f.SetInt(n)

	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
		if val == "" {
			f.Set(reflect.MakeSlice(ft, 0, 0))
			break
		}
		parts := strings.Split(val, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				result = append(result, s)
			}
		}
		f.Set(reflect.ValueOf(result))

	case ft.Kind() == reflect.Map:
		return setLeafYAML(f, ft, val, path)

	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
		return setLeafYAML(f, ft, val, path)

	default:
		return fmt.Errorf("storeui.SetFieldValue: unsupported type %s for %q", ft, path)
	}

	return nil
}

// setLeafYAML sets a field from a YAML string using reflection-based unmarshal.
// Handles map and struct-slice types.
//
// For struct slices: if the input isn't valid YAML for the target type, it falls
// back to wrapping a plain string as a single-element list using the first exported
// string field of the struct. This lets users type a raw command string instead of
// full YAML structure (e.g. typing "npm ci" instead of "- cmd: npm ci").
func setLeafYAML(f reflect.Value, ft reflect.Type, val string, path string) error {
	if val == "" {
		f.Set(reflect.Zero(ft))
		return nil
	}
	ptr := reflect.New(ft)
	if err := yaml.Unmarshal([]byte(val), ptr.Interface()); err != nil {
		// For struct slices, try wrapping a plain string as a single item.
		if ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct {
			if wrapped, ok := wrapStringAsStructSlice(ft, val); ok {
				f.Set(wrapped)
				return nil
			}
		}
		return fmt.Errorf("storeui.SetFieldValue: invalid YAML for %q: %w", path, err)
	}
	f.Set(ptr.Elem())
	return nil
}

// wrapStringAsStructSlice creates a single-element slice of structs from a plain
// string by setting the first exported string field of the struct element type.
// Returns (value, false) if the struct has no string fields.
func wrapStringAsStructSlice(sliceType reflect.Type, val string) (reflect.Value, bool) {
	elemType := sliceType.Elem()

	// Find the first exported string field.
	fieldIdx := -1
	for i := 0; i < elemType.NumField(); i++ {
		sf := elemType.Field(i)
		if sf.IsExported() && sf.Type.Kind() == reflect.String {
			fieldIdx = i
			break
		}
	}
	if fieldIdx < 0 {
		return reflect.Value{}, false
	}

	// Create a single-element slice with the string in the first field.
	elem := reflect.New(elemType).Elem()
	elem.Field(fieldIdx).SetString(val)
	slice := reflect.MakeSlice(sliceType, 1, 1)
	slice.Index(0).Set(elem)
	return slice, true
}
