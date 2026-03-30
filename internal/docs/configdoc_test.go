package docs

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
)

func TestRenderYAMLSchema_ProjectContainsNestedStructTypes(t *testing.T) {
	schema := renderYAMLSchema(reflect.TypeOf(config.Project{}), 0)

	// Verify top-level sections exist.
	for _, section := range []string{"build:", "agent:", "workspace:", "security:", "loop:"} {
		if !strings.Contains(schema, section) {
			t.Errorf("schema missing top-level section %q", section)
		}
	}

	// Verify nested struct slice fields are expanded (not just "object list").
	// These are the types that NormalizeFields treats as opaque KindStructSlice.
	nestedFields := map[string][]string{
		"CopyInstruction": {"src:", "dest:", "chown:", "chmod:"},
		"ArgDefinition":   {"name:", "default:"},
		"EgressRule":      {"dst:", "proto:", "port:", "action:", "path_rules:", "path_default:"},
		"PathRule":        {"path:", "action:"},
	}

	for typeName, fields := range nestedFields {
		for _, field := range fields {
			if !strings.Contains(schema, field) {
				t.Errorf("schema missing %s field %q — nested struct types should be fully expanded", typeName, field)
			}
		}
	}
}

func TestRenderYAMLSchema_DescriptionsAsComments(t *testing.T) {
	schema := renderYAMLSchema(reflect.TypeOf(config.Project{}), 0)

	// Verify descriptions appear as YAML comments.
	wantComments := []string{
		"# Starting Docker image",
		"# System packages",
		"# Domain or IP the container needs to reach",
		"# URL path prefix to match",
		"# File or directory to copy from your project",
	}

	for _, comment := range wantComments {
		if !strings.Contains(schema, comment) {
			t.Errorf("schema missing description comment containing %q", comment)
		}
	}
}

func TestRenderYAMLSchema_DefaultsInComments(t *testing.T) {
	schema := renderYAMLSchema(reflect.TypeOf(config.Project{}), 0)

	// Fields with defaults should show them in comments.
	if !strings.Contains(schema, "(default: ripgrep)") {
		t.Error("schema should show default value for packages field")
	}
	if !strings.Contains(schema, "(default: bind)") {
		t.Error("schema should show default value for default_mode field")
	}
}

func TestGenConfigDoc_IncludesYAMLSchemas(t *testing.T) {
	tmpl := `Project: {{ .ProjectSchema }}
Settings: {{ .SettingsSchema }}`

	var buf bytes.Buffer
	if err := GenConfigDoc(&buf, tmpl); err != nil {
		t.Fatalf("GenConfigDoc: %v", err)
	}

	output := buf.String()

	// Project schema should have build config.
	if !strings.Contains(output, "build:") {
		t.Error("project schema should contain build section")
	}

	// Settings schema should have logging config.
	if !strings.Contains(output, "logging:") {
		t.Error("settings schema should contain logging section")
	}

	// Both should have expanded nested types.
	if !strings.Contains(output, "dst:") {
		t.Error("project schema should expand EgressRule fields")
	}
}
