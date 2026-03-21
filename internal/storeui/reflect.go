package storeui

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// WalkFields uses reflection to discover editable fields from a struct value.
// It maps Go types to FieldKind: string→Text, bool→Bool, *bool→Bool, int→Int,
// []string→StringSlice, time.Duration→Duration, nested struct→recurse,
// nil *struct→recurse zero value, everything else→Complex (read-only).
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

			// Other pointer types → Complex
			f := Field{
				Path:     path,
				Label:    name,
				Kind:     KindComplex,
				ReadOnly: true,
				Order:    *order,
			}
			if !fv.IsNil() {
				f.Value = fmt.Sprintf("%v", fv.Elem().Interface())
			}
			*fields = append(*fields, f)
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
			// Maps, non-string slices, interfaces, etc. → Complex (read-only).
			*fields = append(*fields, Field{
				Path:     path,
				Label:    name,
				Kind:     KindComplex,
				ReadOnly: true,
				Value:    fmt.Sprintf("%v", fv.Interface()),
				Order:    *order,
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
