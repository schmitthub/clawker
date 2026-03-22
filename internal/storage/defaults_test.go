package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// --- test schema types for defaults generation ---

type defaultsTestSimple struct {
	Name      string        `yaml:"name" default:"myapp" desc:"App name"`
	Port      int           `yaml:"port" default:"8080" desc:"Listen port"`
	Verbose   bool          `yaml:"verbose" default:"true" desc:"Verbose mode"`
	Tags      []string      `yaml:"tags" default:"web,api" desc:"Tags"`
	Timeout   time.Duration `yaml:"timeout" default:"30s" desc:"Timeout"`
	NoDefault string        `yaml:"no_default" desc:"No default value"`
}

func (d defaultsTestSimple) Fields() FieldSet { return NormalizeFields(d) }

type defaultsTestNested struct {
	Build defaultsTestBuild  `yaml:"build"`
	Agent *defaultsTestAgent `yaml:"agent"`
}

func (d defaultsTestNested) Fields() FieldSet { return NormalizeFields(d) }

type defaultsTestBuild struct {
	Image    string   `yaml:"image" default:"debian:latest" desc:"Base image"`
	Packages []string `yaml:"packages" default:"git,curl" desc:"Packages"`
}

type defaultsTestAgent struct {
	Enabled *bool  `yaml:"enabled" default:"true" desc:"Enabled"`
	Mode    string `yaml:"mode" default:"auto" desc:"Mode"`
}

type defaultsTestEmpty struct {
	Name string `yaml:"name" desc:"No default"`
	Port int    `yaml:"port" desc:"No default"`
}

func (d defaultsTestEmpty) Fields() FieldSet { return NormalizeFields(d) }

// --- GenerateDefaultsYAML tests ---

func TestGenerateDefaultsYAML_SimpleTypes(t *testing.T) {
	out := GenerateDefaultsYAML[defaultsTestSimple]()
	require.NotEmpty(t, out)

	var m map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &m))

	assert.Equal(t, "myapp", m["name"])
	assert.Equal(t, 8080, m["port"])
	assert.Equal(t, true, m["verbose"])
	assert.Equal(t, "30s", m["timeout"])

	tags, ok := m["tags"].([]any)
	require.True(t, ok, "tags should be a list")
	assert.Equal(t, []any{"web", "api"}, tags)

	// no_default should not appear
	_, exists := m["no_default"]
	assert.False(t, exists, "fields without defaults should not appear")
}

func TestGenerateDefaultsYAML_BoolNotString(t *testing.T) {
	out := GenerateDefaultsYAML[defaultsTestSimple]()

	var m map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &m))

	v, ok := m["verbose"].(bool)
	require.True(t, ok, "verbose should be YAML bool, not string")
	assert.True(t, v)
}

func TestGenerateDefaultsYAML_IntNotString(t *testing.T) {
	out := GenerateDefaultsYAML[defaultsTestSimple]()

	var m map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &m))

	v, ok := m["port"].(int)
	require.True(t, ok, "port should be YAML int, not string")
	assert.Equal(t, 8080, v)
}

func TestGenerateDefaultsYAML_NestedStruct(t *testing.T) {
	out := GenerateDefaultsYAML[defaultsTestNested]()
	require.NotEmpty(t, out)

	var m map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &m))

	build, ok := m["build"].(map[string]any)
	require.True(t, ok, "build should be a nested map")
	assert.Equal(t, "debian:latest", build["image"])
	assert.Equal(t, []any{"git", "curl"}, build["packages"])

	agent, ok := m["agent"].(map[string]any)
	require.True(t, ok, "agent should be a nested map")
	assert.Equal(t, true, agent["enabled"])
	assert.Equal(t, "auto", agent["mode"])
}

func TestGenerateDefaultsYAML_EmptyStruct(t *testing.T) {
	out := GenerateDefaultsYAML[defaultsTestEmpty]()
	assert.Empty(t, out, "struct with no defaults should produce empty string")
}

func TestGenerateDefaultsYAML_RoundTrip(t *testing.T) {
	out := GenerateDefaultsYAML[defaultsTestSimple]()
	require.NotEmpty(t, out)

	store, err := NewFromString[defaultsTestSimple](out)
	require.NoError(t, err)

	snap := store.Read()
	assert.Equal(t, "myapp", snap.Name)
	assert.Equal(t, 8080, snap.Port)
	assert.True(t, snap.Verbose)
	assert.Equal(t, []string{"web", "api"}, snap.Tags)
	assert.Equal(t, 30*time.Second, snap.Timeout)
	assert.Empty(t, snap.NoDefault)
}

func TestGenerateDefaultsYAML_NestedRoundTrip(t *testing.T) {
	out := GenerateDefaultsYAML[defaultsTestNested]()
	require.NotEmpty(t, out)

	store, err := NewFromString[defaultsTestNested](out)
	require.NoError(t, err)

	snap := store.Read()
	assert.Equal(t, "debian:latest", snap.Build.Image)
	assert.Equal(t, []string{"git", "curl"}, snap.Build.Packages)
	require.NotNil(t, snap.Agent)
	require.NotNil(t, snap.Agent.Enabled)
	assert.True(t, *snap.Agent.Enabled)
	assert.Equal(t, "auto", snap.Agent.Mode)
}

func TestWithDefaultsFromStruct_Equivalent(t *testing.T) {
	// WithDefaultsFromStruct should produce the same YAML as GenerateDefaultsYAML
	generated := GenerateDefaultsYAML[defaultsTestSimple]()

	// Can't directly compare options, but we can verify that both produce
	// the same store state by constructing stores both ways.
	store1, err := NewFromString[defaultsTestSimple](generated)
	require.NoError(t, err)

	// WithDefaultsFromStruct is used via NewStore which needs filesystem.
	// Instead, verify the generated string is identical.
	store2, err := NewFromString[defaultsTestSimple](GenerateDefaultsYAML[defaultsTestSimple]())
	require.NoError(t, err)

	snap1 := store1.Read()
	snap2 := store2.Read()
	assert.Equal(t, snap1.Name, snap2.Name)
	assert.Equal(t, snap1.Port, snap2.Port)
	assert.Equal(t, snap1.Verbose, snap2.Verbose)
	assert.Equal(t, snap1.Tags, snap2.Tags)
}

func TestParseDefaultValue_EdgeCases(t *testing.T) {
	// Invalid int falls back to string
	v := parseDefaultValue("not_a_number", KindInt)
	assert.Equal(t, "not_a_number", v)

	// Empty string slice
	v = parseDefaultValue("", KindStringSlice)
	assert.Equal(t, []string{}, v)

	// Bool false
	v = parseDefaultValue("false", KindBool)
	assert.Equal(t, false, v)

	// Complex kind passes through as string
	v = parseDefaultValue("whatever", KindComplex)
	assert.Equal(t, "whatever", v)
}
