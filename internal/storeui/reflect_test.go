package storeui

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalkFields_SimpleStruct(t *testing.T) {
	v := simpleStruct{Name: "test", Enabled: true, Count: 42}
	fields := WalkFields(v)

	require.Len(t, fields, 3)

	assert.Equal(t, "name", fields[0].Path)
	assert.Equal(t, "name", fields[0].Label)
	assert.Equal(t, KindText, fields[0].Kind)
	assert.Equal(t, "test", fields[0].Value)

	assert.Equal(t, "enabled", fields[1].Path)
	assert.Equal(t, KindBool, fields[1].Kind)
	assert.Equal(t, "true", fields[1].Value)

	assert.Equal(t, "count", fields[2].Path)
	assert.Equal(t, KindInt, fields[2].Kind)
	assert.Equal(t, "42", fields[2].Value)
}

func TestWalkFields_NestedPaths(t *testing.T) {
	v := nestedStruct{
		Build: buildSection{Image: "ubuntu:22.04", Packages: []string{"git", "curl"}},
	}
	fields := WalkFields(v)

	require.Len(t, fields, 2)
	assert.Equal(t, "build.image", fields[0].Path)
	assert.Equal(t, "image", fields[0].Label)
	assert.Equal(t, "ubuntu:22.04", fields[0].Value)

	assert.Equal(t, "build.packages", fields[1].Path)
	assert.Equal(t, KindStringSlice, fields[1].Kind)
	assert.Equal(t, "git, curl", fields[1].Value)
}

func TestWalkFields_TriState(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		v := triStateStruct{Enabled: nil}
		fields := WalkFields(v)
		require.Len(t, fields, 1)
		assert.Equal(t, KindTriState, fields[0].Kind)
		assert.Equal(t, "<unset>", fields[0].Value)
		assert.Equal(t, []string{"true", "false", "<unset>"}, fields[0].Options)
	})

	t.Run("true", func(t *testing.T) {
		b := true
		v := triStateStruct{Enabled: &b}
		fields := WalkFields(v)
		require.Len(t, fields, 1)
		assert.Equal(t, "true", fields[0].Value)
	})

	t.Run("false", func(t *testing.T) {
		b := false
		v := triStateStruct{Enabled: &b}
		fields := WalkFields(v)
		require.Len(t, fields, 1)
		assert.Equal(t, "false", fields[0].Value)
	})
}

func TestWalkFields_NilPtrStructRecursesZeroValue(t *testing.T) {
	v := nilPtrStructParent{Loop: nil}
	fields := WalkFields(v)

	require.Len(t, fields, 2)
	assert.Equal(t, "loop.max_loops", fields[0].Path)
	assert.Equal(t, KindInt, fields[0].Kind)
	assert.Equal(t, "0", fields[0].Value)

	assert.Equal(t, "loop.hooks_file", fields[1].Path)
	assert.Equal(t, KindText, fields[1].Kind)
	assert.Equal(t, "", fields[1].Value)
}

func TestWalkFields_Duration(t *testing.T) {
	v := durationStruct{Timeout: 5 * 60 * 1e9} // 5 minutes in nanoseconds
	fields := WalkFields(v)

	require.Len(t, fields, 1)
	assert.Equal(t, "timeout", fields[0].Path)
	assert.Equal(t, KindDuration, fields[0].Kind)
	assert.Equal(t, "5m0s", fields[0].Value)
}

func TestWalkFields_ComplexTypes(t *testing.T) {
	v := complexStruct{
		Name: "test",
		Env:  map[string]string{"FOO": "bar"},
	}
	fields := WalkFields(v)

	require.Len(t, fields, 2)
	assert.Equal(t, "name", fields[0].Path)
	assert.Equal(t, KindText, fields[0].Kind)

	assert.Equal(t, "env", fields[1].Path)
	assert.Equal(t, KindComplex, fields[1].Kind)
	assert.True(t, fields[1].ReadOnly)
}

func TestWalkFields_YAMLTags(t *testing.T) {
	v := yamlTagStruct{ImageName: "alpine", NoTag: "hello"}
	fields := WalkFields(v)

	require.Len(t, fields, 2)
	assert.Equal(t, "image", fields[0].Path)
	assert.Equal(t, "image", fields[0].Label)
	assert.Equal(t, "alpine", fields[0].Value)

	assert.Equal(t, "notag", fields[1].Path)
	assert.Equal(t, "notag", fields[1].Label)
	assert.Equal(t, "hello", fields[1].Value)
}

func TestWalkFields_StringSliceEmpty(t *testing.T) {
	type s struct {
		Items []string `yaml:"items"`
	}
	fields := WalkFields(s{Items: nil})
	require.Len(t, fields, 1)
	assert.Equal(t, KindStringSlice, fields[0].Kind)
	assert.Equal(t, "", fields[0].Value)
}

func TestWalkFields_PointerInputMatchesValue(t *testing.T) {
	v := &simpleStruct{Name: "ptr", Enabled: false, Count: 7}
	fields := WalkFields(v)

	require.Len(t, fields, 3)
	assert.Equal(t, "name", fields[0].Path)
	assert.Equal(t, KindText, fields[0].Kind)
	assert.Equal(t, "ptr", fields[0].Value)

	assert.Equal(t, "enabled", fields[1].Path)
	assert.Equal(t, KindBool, fields[1].Kind)
	assert.Equal(t, "false", fields[1].Value)

	assert.Equal(t, "count", fields[2].Path)
	assert.Equal(t, KindInt, fields[2].Kind)
	assert.Equal(t, "7", fields[2].Value)
}

func TestWalkFields_OrderMonotonicAcrossNesting(t *testing.T) {
	v := nestedStruct{Build: buildSection{Image: "alpine", Packages: []string{"git"}}}
	fields := WalkFields(v)

	require.Len(t, fields, 2)
	for i := 1; i < len(fields); i++ {
		assert.Greater(t, fields[i].Order, fields[i-1].Order,
			"field %d (%s) should have higher order than field %d (%s)",
			i, fields[i].Path, i-1, fields[i-1].Path)
	}
}

func TestWalkFields_PanicsOnNilInput(t *testing.T) {
	assert.Panics(t, func() { WalkFields(nil) })
}

func TestWalkFields_PanicsOnNonStructInput(t *testing.T) {
	assert.Panics(t, func() { WalkFields(42) })
	assert.Panics(t, func() { WalkFields("hello") })
}

func TestWalkFields_RealSettingsStruct(t *testing.T) {
	fields := WalkFields(config.Settings{})

	byPath := make(map[string]Field)
	for _, f := range fields {
		byPath[f.Path] = f
	}

	f, ok := byPath["logging.file_enabled"]
	require.True(t, ok, "should have logging.file_enabled")
	assert.Equal(t, KindTriState, f.Kind, "logging.file_enabled is *bool")

	f, ok = byPath["logging.max_size_mb"]
	require.True(t, ok, "should have logging.max_size_mb")
	assert.Equal(t, KindInt, f.Kind)

	f, ok = byPath["firewall.enable"]
	require.True(t, ok, "should have firewall.enable")
	assert.Equal(t, KindTriState, f.Kind, "firewall.enable is *bool")

	f, ok = byPath["monitoring.grafana_port"]
	require.True(t, ok, "should have monitoring.grafana_port")
	assert.Equal(t, KindInt, f.Kind)
}

func TestWalkFields_RealProjectStruct(t *testing.T) {
	fields := WalkFields(config.Project{})

	byPath := make(map[string]Field)
	for _, f := range fields {
		byPath[f.Path] = f
	}

	f, ok := byPath["build.image"]
	require.True(t, ok, "should have build.image")
	assert.Equal(t, KindText, f.Kind)

	f, ok = byPath["workspace.default_mode"]
	require.True(t, ok, "should have workspace.default_mode")
	assert.Equal(t, KindText, f.Kind)

	f, ok = byPath["security.docker_socket"]
	require.True(t, ok, "should have security.docker_socket")
	assert.Equal(t, KindBool, f.Kind)

	f, ok = byPath["loop.max_loops"]
	require.True(t, ok, "should have loop.max_loops from nil *LoopConfig")
	assert.Equal(t, KindInt, f.Kind)
}
