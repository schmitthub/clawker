package docs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"text/template"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/storage"
)

// ConfigSection is a top-level config section (build, agent, security, etc.)
// passed to the template.
type ConfigSection struct {
	Key    string        // YAML key (e.g. "build")
	Fields []ConfigField // Leaf fields in this section
	Groups []ConfigGroup // Sub-groups (e.g. "build.instructions", "build.inject")
}

// ConfigGroup is a nested group of fields under a section (e.g. instructions, inject).
type ConfigGroup struct {
	Key    string        // YAML key relative to section (e.g. "instructions")
	Path   string        // Full dotted path (e.g. "build.instructions")
	Fields []ConfigField // Leaf fields in this group
}

// ConfigField is a single leaf field for template rendering.
type ConfigField struct {
	Path        string // Dotted YAML path (e.g. "build.image")
	Key         string // Leaf key name (e.g. "image")
	Label       string
	Description string
	Type        string // Human-readable type (e.g. "string", "boolean", "string list")
	Default     string
	Required    bool
}

// ConfigDocData is the full template data for configuration.mdx.
type ConfigDocData struct {
	ProjectSections  []ConfigSection
	SettingsSections []ConfigSection
	ProjectSchema    string // Full YAML schema with descriptions as comments
	SettingsSchema   string // Full YAML schema with descriptions as comments
}

// GenConfigDoc renders the configuration documentation template to w using
// schema metadata from the Project and Settings types.
func GenConfigDoc(w io.Writer, tmplContent string) error {
	data := ConfigDocData{
		ProjectSections:  buildSections(config.Project{}),
		SettingsSections: buildSections(config.Settings{}),
		ProjectSchema:    renderYAMLSchema(reflect.TypeOf(config.Project{}), 0),
		SettingsSchema:   renderYAMLSchema(reflect.TypeOf(config.Settings{}), 0),
	}

	funcMap := template.FuncMap{
		"fieldTable": renderFieldTable,
	}

	tmpl, err := template.New("configuration.mdx").Funcs(funcMap).Parse(tmplContent)
	if err != nil {
		return fmt.Errorf("parsing config doc template: %w", err)
	}

	return tmpl.Execute(w, data)
}

// buildSections groups fields from a Schema type into sections by top-level key.
func buildSections(schema storage.Schema) []ConfigSection {
	fields := schema.Fields()
	all := fields.All()

	// Discover top-level keys in order.
	var topKeys []string
	seen := map[string]bool{}
	for _, f := range all {
		top := strings.SplitN(f.Path(), ".", 2)[0]
		if !seen[top] {
			seen[top] = true
			topKeys = append(topKeys, top)
		}
	}

	var sections []ConfigSection
	for _, key := range topKeys {
		group := fields.Group(key)
		section := ConfigSection{Key: key}

		// Separate direct children from nested groups.
		subGroups := map[string][]ConfigField{}
		var subGroupOrder []string

		for _, f := range group {
			rel := strings.TrimPrefix(f.Path(), key+".")
			parts := strings.SplitN(rel, ".", 2)

			if len(parts) == 1 {
				// Direct child of this section.
				section.Fields = append(section.Fields, toConfigField(f))
			} else {
				// Nested under a sub-group.
				groupKey := parts[0]
				if _, exists := subGroups[groupKey]; !exists {
					subGroupOrder = append(subGroupOrder, groupKey)
				}
				subGroups[groupKey] = append(subGroups[groupKey], toConfigField(f))
			}
		}

		for _, gk := range subGroupOrder {
			section.Groups = append(section.Groups, ConfigGroup{
				Key:    gk,
				Path:   key + "." + gk,
				Fields: subGroups[gk],
			})
		}

		sections = append(sections, section)
	}
	return sections
}

func toConfigField(f storage.Field) ConfigField {
	path := f.Path()
	parts := strings.Split(path, ".")
	key := parts[len(parts)-1]

	return ConfigField{
		Path:        path,
		Key:         key,
		Label:       f.Label(),
		Description: escapeMDX(f.Description()),
		Type:        kindToType(f.Kind()),
		Default:     f.Default(),
		Required:    f.Required(),
	}
}

func kindToType(k storage.FieldKind) string {
	switch k {
	case storage.KindText:
		return "string"
	case storage.KindBool:
		return "boolean"
	case storage.KindInt:
		return "integer"
	case storage.KindStringSlice:
		return "string list"
	case storage.KindDuration:
		return "duration"
	case storage.KindMap:
		return "key-value map"
	case storage.KindStructSlice:
		return "object list"
	default:
		return "string"
	}
}

// renderFieldTable is a template function that renders a []ConfigField as a markdown table.
func renderFieldTable(fields []ConfigField) string {
	if len(fields) == 0 {
		return ""
	}

	var buf bytes.Buffer
	buf.WriteString("| Field | Type | Default | Description |\n")
	buf.WriteString("|-------|------|---------|-------------|\n")

	for _, f := range fields {
		def := f.Default
		if def == "" {
			def = "—"
		} else {
			def = "`" + def + "`"
		}

		required := ""
		if f.Required {
			required = " **(required)**"
		}

		fmt.Fprintf(&buf, "| `%s` | %s | %s | %s%s |\n",
			f.Key, f.Type, def, f.Description, required)
	}
	return buf.String()
}

// escapeMDX wraps bare <word> angle brackets in backticks for MDX safety,
// reusing the same regex-based logic as EscapeMDXProse.
func escapeMDX(s string) string {
	return EscapeMDXProse(s)
}

// renderYAMLSchema generates a complete YAML schema from a struct type using
// reflection. Unlike NormalizeFields (which treats []struct as an opaque leaf),
// this recurses into struct slice element types to show their full field
// structure. Descriptions from `desc` tags become inline YAML comments.
func renderYAMLSchema(t reflect.Type, indent int) string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return ""
	}

	var buf bytes.Buffer
	prefix := strings.Repeat("  ", indent)

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		yamlKey := yamlFieldKey(f)
		if yamlKey == "-" {
			continue
		}

		desc := f.Tag.Get("desc")
		def := f.Tag.Get("default")
		req := f.Tag.Get("required") == "true"

		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		switch {
		case ft.Kind() == reflect.Struct && ft != reflect.TypeOf(time.Duration(0)) && ft != reflect.TypeOf(os.FileMode(0)):
			// Nested struct — recurse.
			if desc != "" {
				buf.WriteString(fmt.Sprintf("%s# %s\n", prefix, desc))
			}
			buf.WriteString(fmt.Sprintf("%s%s:\n", prefix, yamlKey))
			buf.WriteString(renderYAMLSchema(ft, indent+1))

		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			// Struct slice — show element fields as list item.
			if desc != "" {
				buf.WriteString(fmt.Sprintf("%s# %s\n", prefix, desc))
			}
			buf.WriteString(fmt.Sprintf("%s%s:\n", prefix, yamlKey))
			buf.WriteString(renderStructSliceElement(ft.Elem(), indent+1))

		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
			writeDescComment(&buf, prefix, desc)
			buf.WriteString(fmt.Sprintf("%s%s:  %s\n%s  - <string>\n", prefix, yamlKey, fieldMeta(def, req), prefix))

		case ft.Kind() == reflect.Map && ft.Key().Kind() == reflect.String && ft.Elem().Kind() == reflect.String:
			writeDescComment(&buf, prefix, desc)
			buf.WriteString(fmt.Sprintf("%s%s:  %s\n%s  <key>: <value>\n", prefix, yamlKey, fieldMeta(def, req), prefix))

		case ft == reflect.TypeOf(time.Duration(0)):
			writeDescComment(&buf, prefix, desc)
			buf.WriteString(fmt.Sprintf("%s%s: <duration>  %s\n", prefix, yamlKey, fieldMeta(def, req)))

		case ft.Kind() == reflect.Bool:
			writeDescComment(&buf, prefix, desc)
			buf.WriteString(fmt.Sprintf("%s%s: <boolean>  %s\n", prefix, yamlKey, fieldMeta(def, req)))

		case ft.Kind() == reflect.Int, ft.Kind() == reflect.Int64:
			writeDescComment(&buf, prefix, desc)
			buf.WriteString(fmt.Sprintf("%s%s: <integer>  %s\n", prefix, yamlKey, fieldMeta(def, req)))

		case ft.Kind() == reflect.String:
			writeDescComment(&buf, prefix, desc)
			buf.WriteString(fmt.Sprintf("%s%s: <string>  %s\n", prefix, yamlKey, fieldMeta(def, req)))

		default:
			writeDescComment(&buf, prefix, desc)
			buf.WriteString(fmt.Sprintf("%s%s: <value>  %s\n", prefix, yamlKey, fieldMeta(def, req)))
		}
	}
	return buf.String()
}

// renderStructSliceElement renders the fields of a struct as a YAML list item
// (first field prefixed with "- ", subsequent fields indented to align).
func renderStructSliceElement(t reflect.Type, indent int) string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return ""
	}

	var buf bytes.Buffer
	prefix := strings.Repeat("  ", indent)
	first := true

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		yamlKey := yamlFieldKey(f)
		if yamlKey == "-" {
			continue
		}

		desc := f.Tag.Get("desc")
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		// List items: "- key: val" for first field, "  key: val" for rest.
		// Comments align with their key line.
		itemPrefix := prefix + "  " // continuation indent for 2nd+ fields
		if first {
			first = false
			// First field: comment + "- key: val" both at list indent.
			if desc != "" {
				buf.WriteString(fmt.Sprintf("%s# %s\n", prefix, desc))
			}
			switch {
			case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
				buf.WriteString(fmt.Sprintf("%s- %s:\n", prefix, yamlKey))
				buf.WriteString(renderStructSliceElement(ft.Elem(), indent+2))
			case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
				buf.WriteString(fmt.Sprintf("%s- %s:\n%s  - <string>\n", prefix, yamlKey, itemPrefix))
			default:
				buf.WriteString(fmt.Sprintf("%s- %s: <%s>\n", prefix, yamlKey, yamlTypeName(ft)))
			}
			continue
		}

		if desc != "" {
			buf.WriteString(fmt.Sprintf("%s# %s\n", itemPrefix, desc))
		}
		switch {
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			buf.WriteString(fmt.Sprintf("%s%s:\n", itemPrefix, yamlKey))
			buf.WriteString(renderStructSliceElement(ft.Elem(), indent+2))
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
			buf.WriteString(fmt.Sprintf("%s%s:\n%s  - <string>\n", itemPrefix, yamlKey, itemPrefix))
		default:
			buf.WriteString(fmt.Sprintf("%s%s: <%s>\n", itemPrefix, yamlKey, yamlTypeName(ft)))
		}
	}
	return buf.String()
}

// writeDescComment writes the description as a YAML comment line.
func writeDescComment(buf *bytes.Buffer, prefix, desc string) {
	if desc != "" {
		buf.WriteString(fmt.Sprintf("%s# %s\n", prefix, desc))
	}
}

// fieldMeta returns an inline YAML comment with default and required metadata.
// Every field gets both values so agents can parse the schema unambiguously.
func fieldMeta(def string, required bool) string {
	d := "n/a"
	if def != "" {
		d = def
	}
	r := "false"
	if required {
		r = "true"
	}
	return fmt.Sprintf("# default: %s | required: %s", d, r)
}

// yamlFieldKey extracts the YAML key from a struct field's yaml tag.
func yamlFieldKey(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	if tag == "" {
		return strings.ToLower(f.Name)
	}
	key, _, _ := strings.Cut(tag, ",")
	if key == "" {
		return strings.ToLower(f.Name)
	}
	return key
}

// yamlTypeName returns a human-readable type placeholder for YAML schema output.
func yamlTypeName(t reflect.Type) string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int64:
		return "integer"
	default:
		return "value"
	}
}
