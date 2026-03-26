package docs

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/template"

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
}

// GenConfigDoc renders the configuration documentation template to w using
// schema metadata from the Project and Settings types.
func GenConfigDoc(w io.Writer, tmplContent string) error {
	data := ConfigDocData{
		ProjectSections:  buildSections(config.Project{}),
		SettingsSections: buildSections(config.Settings{}),
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
