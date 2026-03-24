package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test schema types ---

type fieldTestConfig struct {
	Name     string            `yaml:"name" label:"Name" desc:"Project name"`
	Version  int               `yaml:"version" label:"Version" desc:"Schema version" default:"1"`
	Enabled  bool              `yaml:"enabled" desc:"Whether the project is enabled"`
	Timeout  time.Duration     `yaml:"timeout" label:"Timeout" desc:"Operation timeout"`
	Tags     []string          `yaml:"tags" desc:"Project tags"`
	Build    fieldTestBuild    `yaml:"build"`
	Optional *fieldTestNested  `yaml:"optional"`
	Debug    *bool             `yaml:"debug" label:"Debug" desc:"Enable debug mode"`
	Env      map[string]string `yaml:"env" desc:"Environment variables"`
	internal string            //nolint:unused // unexported, should be skipped
}

type fieldTestBuild struct {
	Image  string `yaml:"image" label:"Image" desc:"Base Docker image"`
	Target string `yaml:"target" desc:"Build target"`
}

type fieldTestNested struct {
	Port int `yaml:"port" label:"Port" desc:"Listening port"`
}

type fieldTestRequired struct {
	Host string `yaml:"host" required:"true" default:"localhost" desc:"Hostname"`
	Port int    `yaml:"port" desc:"Port number"`
}

type fieldTestTagless struct {
	NoTag     string `yaml:""`
	SkipField string `yaml:"-"`
	Bare      string
	OmitOnly  string `yaml:",omitempty"`
}

// --- NormalizeFields tests ---

func TestNormalizeFields_AllTypeMappings(t *testing.T) {
	fs := NormalizeFields(fieldTestConfig{})

	tests := []struct {
		path string
		kind FieldKind
	}{
		{"name", KindText},
		{"version", KindInt},
		{"enabled", KindBool},
		{"timeout", KindDuration},
		{"tags", KindStringSlice},
		{"build.image", KindText},
		{"build.target", KindText},
		{"optional.port", KindInt},
		{"debug", KindBool},
		{"env", KindMap},
	}

	for _, tt := range tests {
		f := fs.Get(tt.path)
		require.NotNilf(t, f, "field %q not found", tt.path)
		assert.Equalf(t, tt.kind, f.Kind(), "field %q kind mismatch", tt.path)
	}
}

func TestNormalizeFields_DescAndLabelTags(t *testing.T) {
	fs := NormalizeFields(fieldTestConfig{})

	// Explicit label tag.
	name := fs.Get("name")
	require.NotNil(t, name)
	assert.Equal(t, "Name", name.Label())
	assert.Equal(t, "Project name", name.Description())

	// No label tag — falls back to YAML key.
	enabled := fs.Get("enabled")
	require.NotNil(t, enabled)
	assert.Equal(t, "enabled", enabled.Label())
	assert.Equal(t, "Whether the project is enabled", enabled.Description())

	// Nested field with explicit label.
	image := fs.Get("build.image")
	require.NotNil(t, image)
	assert.Equal(t, "Image", image.Label())
	assert.Equal(t, "Base Docker image", image.Description())

	// Nil *struct field — label/desc still extracted from type definition.
	port := fs.Get("optional.port")
	require.NotNil(t, port)
	assert.Equal(t, "Port", port.Label())
	assert.Equal(t, "Listening port", port.Description())
}

func TestNormalizeFields_DefaultTag(t *testing.T) {
	fs := NormalizeFields(fieldTestConfig{})

	version := fs.Get("version")
	require.NotNil(t, version)
	assert.Equal(t, "1", version.Default())

	// Field without default tag.
	name := fs.Get("name")
	require.NotNil(t, name)
	assert.Equal(t, "", name.Default())
}

func TestNormalizeFields_PointerToStruct(t *testing.T) {
	fs := NormalizeFields(&fieldTestConfig{})

	// Should produce the same fields as non-pointer.
	assert.Equal(t, NormalizeFields(fieldTestConfig{}).Len(), fs.Len())
	assert.NotNil(t, fs.Get("name"))
	assert.NotNil(t, fs.Get("build.image"))
}

func TestNormalizeFields_TaglessFallbacks(t *testing.T) {
	fs := NormalizeFields(fieldTestTagless{})

	// Empty yaml tag → lowercased field name, no desc/label.
	f := fs.Get("notag")
	require.NotNil(t, f)
	assert.Equal(t, "notag", f.Label())

	// yaml:"-" should be skipped.
	assert.Nil(t, fs.Get("skipfield"))
	assert.Nil(t, fs.Get("-"))

	// No yaml tag at all → lowercased field name.
	bare := fs.Get("bare")
	require.NotNil(t, bare)
	assert.Equal(t, "bare", bare.Label())

	// yaml:",omitempty" → empty name part, falls back to lowercased field name.
	omit := fs.Get("omitonly")
	require.NotNil(t, omit, "yaml:\",omitempty\" should produce field with lowercased Go name")
	assert.Equal(t, "omitonly", omit.Label())
}

func TestNormalizeFields_FieldCount(t *testing.T) {
	fs := NormalizeFields(fieldTestConfig{})
	// name, version, enabled, timeout, tags, build.image, build.target,
	// optional.port, debug, env = 10 leaf fields.
	// Also implicitly validates that unexported fields (internal) are skipped.
	assert.Equal(t, 10, fs.Len(), "unexpected field count — update if struct changes")
}

func TestNormalizeFields_PanicOnNonStruct(t *testing.T) {
	assert.Panics(t, func() { NormalizeFields("not a struct") })
	assert.Panics(t, func() { NormalizeFields(42) })
	assert.PanicsWithValue(t, "storage.NormalizeFields: expected struct or *struct, got nil", func() {
		NormalizeFields[any](nil)
	})
}

func TestNormalizeFields_RequiredTag(t *testing.T) {
	fs := NormalizeFields(fieldTestRequired{})

	host := fs.Get("host")
	require.NotNil(t, host)
	assert.True(t, host.Required(), "host should be required")
	assert.Equal(t, "localhost", host.Default())

	port := fs.Get("port")
	require.NotNil(t, port)
	assert.False(t, port.Required(), "port should not be required")
}

func TestNewField_Required(t *testing.T) {
	f := NewField("a.b", KindText, "A", "desc", "val", true)
	assert.True(t, f.Required())

	f2 := NewField("c.d", KindText, "C", "desc", "", false)
	assert.False(t, f2.Required())
}

func TestNormalizeFields_Int64(t *testing.T) {
	type withInt64 struct {
		Count int64 `yaml:"count" desc:"A 64-bit counter"`
	}
	fs := NormalizeFields(withInt64{})
	f := fs.Get("count")
	require.NotNil(t, f)
	assert.Equal(t, KindInt, f.Kind())
}

// --- FieldSet tests ---

func TestFieldSet_Get(t *testing.T) {
	fs := NormalizeFields(fieldTestConfig{})

	assert.NotNil(t, fs.Get("name"))
	assert.NotNil(t, fs.Get("build.image"))
	assert.Nil(t, fs.Get("nonexistent"))
	assert.Nil(t, fs.Get(""))
}

func TestFieldSet_Group(t *testing.T) {
	fs := NormalizeFields(fieldTestConfig{})

	buildFields := fs.Group("build")
	assert.Len(t, buildFields, 2) // build.image, build.target
	for _, f := range buildFields {
		assert.True(t, len(f.Path()) > len("build."))
	}

	// No matches.
	assert.Empty(t, fs.Group("nonexistent"))
}

func TestFieldSet_All_PreservesOrder(t *testing.T) {
	fs := NormalizeFields(fieldTestConfig{})

	all := fs.All()
	require.Greater(t, len(all), 0)

	// First field should be "name" (first exported field in struct).
	assert.Equal(t, "name", all[0].Path())
}

// --- NewField tests ---

func TestNewField_PanicOnEmptyPath(t *testing.T) {
	assert.PanicsWithValue(t, "storage.NewField: path must not be empty", func() {
		NewField("", KindText, "Label", "desc", "", false)
	})
}

// --- NewFieldSet tests ---

func TestNewFieldSet_Empty(t *testing.T) {
	fs := NewFieldSet(nil)
	assert.Equal(t, 0, fs.Len())
	assert.Empty(t, fs.All())
	assert.Nil(t, fs.Get("anything"))
}

func TestNewFieldSet_PanicOnDuplicatePaths(t *testing.T) {
	fields := []Field{
		NewField("a.b", KindText, "A", "", "", false),
		NewField("a.b", KindInt, "B", "", "", false),
	}
	assert.PanicsWithValue(t, "storage.NewFieldSet: duplicate field path a.b", func() {
		NewFieldSet(fields)
	})
}
