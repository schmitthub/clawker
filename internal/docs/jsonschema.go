package docs

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// jsonSchemaDialect is the JSON Schema dialect the generated schemas declare.
const jsonSchemaDialect = "https://json-schema.org/draft/2020-12/schema"

// JSON Schema keyword + type-value literals (deduplicated for goconst).
const (
	keyType    = "type"
	typeString = "string"
	typeObject = "object"
	typeArray  = "array"
	typeBool   = "boolean"
	typeInt    = "integer"
	tagTrue    = "true"
)

// GenJSONSchema generates a JSON Schema (draft 2020-12) for a config struct type
// from its `yaml`/`label`/`desc`/`default`/`required` struct tags — the same
// single source of truth the YAML reference doc and the storage layer read.
//
// Unlike storage.NormalizeFields (which treats a []struct as an opaque leaf),
// this recurses into struct-slice element types so array items carry full
// property schemas. Objects are strict (additionalProperties:false) so editors
// flag unknown/misspelled keys.
//
// id is the canonical `$id` URL (the same URL the file's yaml-language-server
// header points at); title is the human-readable schema title.
func GenJSONSchema(t reflect.Type, id, title string) ([]byte, error) {
	root := jsonSchemaForStruct(t)
	root["$schema"] = jsonSchemaDialect
	root["$id"] = id
	root["title"] = title

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("docs: marshaling json schema: %w", err)
	}
	return append(out, '\n'), nil
}

// jsonSchemaForStruct builds an object schema for a struct type: a property per
// exported field, a sorted required array from `required:"true"` tags, and
// additionalProperties:false.
func jsonSchemaForStruct(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	properties := map[string]any{}
	var required []string

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		key := yamlFieldKey(f)
		if key == "-" {
			continue
		}

		properties[key] = jsonSchemaForField(f)
		if f.Tag.Get("required") == tagTrue {
			required = append(required, key)
		}
	}

	schema := map[string]any{
		keyType:                typeObject,
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		sort.Strings(required)
		schema["required"] = required
	}
	return schema
}

// jsonSchemaForField builds the schema node for a single struct field, attaching
// title/description/default metadata from its tags.
func jsonSchemaForField(f reflect.StructField) map[string]any {
	ft := f.Type
	for ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}

	schema := jsonSchemaForType(ft)

	if label := f.Tag.Get("label"); label != "" {
		schema["title"] = label
	}
	if desc := f.Tag.Get("desc"); desc != "" {
		schema["description"] = desc
	}
	if def := f.Tag.Get("default"); def != "" {
		if v := defaultValue(ft, def); v != nil {
			schema["default"] = v
		}
	}
	return schema
}

// jsonSchemaForType maps a (pointer-dereferenced) Go type to its JSON Schema
// node, mirroring the type switch in renderYAMLSchema.
func jsonSchemaForType(ft reflect.Type) map[string]any {
	switch {
	case ft == reflect.TypeFor[time.Duration]():
		// Go duration string, e.g. "30s".
		return map[string]any{keyType: typeString}

	case ft == reflect.TypeFor[time.Time]():
		return map[string]any{keyType: typeString, "format": "date-time"}

	case ft.Kind() == reflect.Struct:
		return jsonSchemaForStruct(ft)

	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
		return map[string]any{keyType: typeArray, "items": jsonSchemaForStruct(ft.Elem())}

	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
		return map[string]any{keyType: typeArray, "items": map[string]any{keyType: typeString}}

	case ft.Kind() == reflect.Map && ft.Key().Kind() == reflect.String && ft.Elem().Kind() == reflect.String:
		return map[string]any{keyType: typeObject, "additionalProperties": map[string]any{keyType: typeString}}

	case ft.Kind() == reflect.Bool:
		return map[string]any{keyType: typeBool}

	case ft.Kind() == reflect.Int, ft.Kind() == reflect.Int64:
		return map[string]any{keyType: typeInt}

	case ft.Kind() == reflect.String:
		return map[string]any{keyType: typeString}

	default:
		// Fall back to a permissive string for any opaque named type (e.g.
		// config.Port wraps an int but unmarshals from a scalar).
		return map[string]any{keyType: typeString}
	}
}

// defaultValue coerces a `default` struct tag into a typed JSON value matching
// the field's type, mirroring storage's default formats (storage-schema.md).
// Returns nil when the tag cannot be coerced (the default is then omitted).
func defaultValue(ft reflect.Type, def string) any {
	switch {
	case ft.Kind() == reflect.Bool:
		if b, err := strconv.ParseBool(def); err == nil {
			return b
		}
		return nil

	case ft.Kind() == reflect.Int, ft.Kind() == reflect.Int64:
		return intDefault(ft, def)

	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
		return stringSliceDefault(def)

	case ft.Kind() == reflect.Map && ft.Key().Kind() == reflect.String && ft.Elem().Kind() == reflect.String:
		return stringMapDefault(def)

	default:
		return def
	}
}

// intDefault coerces an int/int64 default; a [time.Duration] keeps its string form
// (e.g. "30s"), and an unparseable value yields nil.
func intDefault(ft reflect.Type, def string) any {
	if ft == reflect.TypeFor[time.Duration]() {
		return def
	}
	if n, err := strconv.Atoi(def); err == nil {
		return n
	}
	return nil
}

// stringSliceDefault splits a comma-separated default into a []any of strings.
func stringSliceDefault(def string) any {
	parts := strings.Split(def, ",")
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		out = append(out, p)
	}
	return out
}

// stringMapDefault parses a comma-separated key=value default into a string map.
func stringMapDefault(def string) any {
	out := map[string]any{}
	for pair := range strings.SplitSeq(def, ",") {
		if k, v, found := strings.Cut(pair, "="); found {
			out[k] = v
		}
	}
	return out
}
