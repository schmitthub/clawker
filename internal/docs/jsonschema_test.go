package docs_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docs"
)

// obj is a tiny helper to descend into a decoded JSON Schema map.
func obj(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key]
	require.Truef(t, ok, "missing key %q", key)
	child, ok := v.(map[string]any)
	require.Truef(t, ok, "key %q is not an object: %T", key, v)
	return child
}

func TestGenJSONSchema_Project(t *testing.T) {
	raw, err := docs.GenJSONSchema(
		reflect.TypeFor[config.Project](),
		"https://example.test/clawker.schema.json",
		"clawker project configuration",
	)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	// Root envelope.
	assert.Equal(t, "https://json-schema.org/draft/2020-12/schema", doc["$schema"])
	assert.Equal(t, "https://example.test/clawker.schema.json", doc["$id"])
	assert.Equal(t, "clawker project configuration", doc["title"])
	assert.Equal(t, "object", doc["type"])
	assert.Equal(t, false, doc["additionalProperties"], "strict mode: unknown keys rejected")

	props := obj(t, doc, "properties")

	build := obj(t, props, "build")
	buildProps := obj(t, build, "properties")

	// String slice leaf with metadata.
	pkgs := obj(t, buildProps, "packages")
	assert.NotEmpty(t, pkgs["description"], "desc tag should populate description")
	assert.NotEmpty(t, pkgs["title"], "label tag should populate title")
	assert.Equal(t, "array", pkgs["type"])
	assert.Equal(t, "string", obj(t, pkgs, "items")["type"])

	// map[string]string → object with string additionalProperties.
	aliases := obj(t, props, "aliases")
	assert.Equal(t, "object", aliases["type"])
	assert.Equal(t, "string", obj(t, aliases, "additionalProperties")["type"])

	// Struct slice recurses into element fields (the FieldSet-opaque case).
	sec := obj(t, props, "security")
	fw := obj(t, obj(t, sec, "properties"), "firewall")
	rules := obj(t, obj(t, fw, "properties"), "rules")
	assert.Equal(t, "array", rules["type"])
	items := obj(t, rules, "items")
	assert.Equal(t, "object", items["type"])
	assert.Equal(t, false, items["additionalProperties"])
	itemProps := obj(t, items, "properties")
	assert.Equal(t, "string", obj(t, itemProps, "dst")["type"])
	// jsontype tag overrides the reflected type with a union — a port is
	// written as 443 or "9000-9100" and both must validate in editors.
	assert.Equal(t, []any{"string", "integer"}, obj(t, itemProps, "port")["type"])
	// Nested struct slice within the element (path_rules) recurses too.
	pathRules := obj(t, itemProps, "path_rules")
	assert.Equal(t, "array", pathRules["type"])
	assert.Equal(t, "string", obj(t, obj(t, obj(t, pathRules, "items"), "properties"), "path")["type"])

	// required reflects the required:"true" tag.
	ws := obj(t, props, "workspace")
	req, ok := ws["required"].([]any)
	require.True(t, ok, "workspace should carry a required array")
	assert.Contains(t, req, "default_mode")

	// default tags are coerced to typed JSON values (defaultValue family).
	assert.Equal(t, "bind", obj(t, obj(t, ws, "properties"), "default_mode")["default"], "string default")
	assert.Equal(t, []any{"ripgrep"}, pkgs["default"], "[]string default → JSON array")
	aliasDefault, ok := aliases["default"].(map[string]any)
	require.True(t, ok, "map[string]string default → JSON object")
	assert.NotEmpty(t, aliasDefault["go"], "key=value default pairs preserved")
	assert.NotEmpty(t, aliasDefault["wt"])
}

func TestGenJSONSchema_Settings(t *testing.T) {
	raw, err := docs.GenJSONSchema(
		reflect.TypeFor[config.Settings](),
		"https://example.test/settings.schema.json",
		"clawker settings",
	)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	props := obj(t, doc, "properties")
	cp := obj(t, obj(t, props, "control_plane"), "properties")
	assert.Equal(t, "integer", obj(t, cp, "admin_port")["type"])

	// *bool renders as boolean.
	fw := obj(t, obj(t, props, "firewall"), "properties")
	assert.Equal(t, "boolean", obj(t, fw, "enable")["type"])

	// time.Duration renders as a string.
	hp := obj(t, obj(t, obj(t, props, "host_proxy"), "properties"), "daemon")
	assert.Equal(t, "string", obj(t, obj(t, hp, "properties"), "poll_interval")["type"])

	// default coercion across int, *bool, and duration.
	assert.InDelta(t, 7443, obj(t, cp, "admin_port")["default"], 0, "int default")
	assert.Equal(t, true, obj(t, fw, "enable")["default"], "*bool default → JSON bool")
	pollInterval := obj(t, obj(t, hp, "properties"), "poll_interval")
	assert.Equal(t, "30s", pollInterval["default"], "duration default stays a string")
}
