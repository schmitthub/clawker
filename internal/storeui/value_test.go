package storeui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetFieldValue_String(t *testing.T) {
	v := &simpleStruct{Name: "old"}
	err := SetFieldValue(v, "name", "new")
	require.NoError(t, err)
	assert.Equal(t, "new", v.Name)
}

func TestSetFieldValue_Bool(t *testing.T) {
	v := &simpleStruct{Enabled: false}

	require.NoError(t, SetFieldValue(v, "enabled", "true"))
	assert.True(t, v.Enabled)

	require.NoError(t, SetFieldValue(v, "enabled", "false"))
	assert.False(t, v.Enabled)
}

func TestSetFieldValue_Int(t *testing.T) {
	v := &simpleStruct{Count: 0}
	require.NoError(t, SetFieldValue(v, "count", "42"))
	assert.Equal(t, 42, v.Count)
}

func TestSetFieldValue_TriState(t *testing.T) {
	v := &triStateStruct{}

	require.NoError(t, SetFieldValue(v, "enabled", "true"))
	require.NotNil(t, v.Enabled)
	assert.True(t, *v.Enabled)

	require.NoError(t, SetFieldValue(v, "enabled", "false"))
	require.NotNil(t, v.Enabled)
	assert.False(t, *v.Enabled)

	require.NoError(t, SetFieldValue(v, "enabled", "<unset>"))
	assert.Nil(t, v.Enabled)
}

func TestSetFieldValue_StringSlice(t *testing.T) {
	type s struct {
		Items []string `yaml:"items"`
	}
	v := &s{}

	require.NoError(t, SetFieldValue(v, "items", "a, b, c"))
	assert.Equal(t, []string{"a", "b", "c"}, v.Items)

	require.NoError(t, SetFieldValue(v, "items", ""))
	assert.Empty(t, v.Items)

	require.NoError(t, SetFieldValue(v, "items", "  single  "))
	assert.Equal(t, []string{"single"}, v.Items)
}

func TestSetFieldValue_Duration(t *testing.T) {
	v := &durationStruct{}
	require.NoError(t, SetFieldValue(v, "timeout", "5m30s"))
	assert.Equal(t, 5*time.Minute+30*time.Second, v.Timeout)

	// Zero duration round-trip.
	require.NoError(t, SetFieldValue(v, "timeout", "0s"))
	assert.Equal(t, time.Duration(0), v.Timeout)
}

func TestSetFieldValue_NestedPath(t *testing.T) {
	v := &nestedStruct{}
	require.NoError(t, SetFieldValue(v, "build.image", "ubuntu:22.04"))
	assert.Equal(t, "ubuntu:22.04", v.Build.Image)
}

func TestSetFieldValue_NilPtrStructParentAllocated(t *testing.T) {
	v := &nilPtrStructParent{Loop: nil}
	require.NoError(t, SetFieldValue(v, "loop.max_loops", "50"))
	require.NotNil(t, v.Loop)
	assert.Equal(t, 50, v.Loop.MaxLoops)
}

func TestSetFieldValue_ErrorCases(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		val     string
		wantErr string
	}{
		{name: "unknown path", path: "nonexistent", val: "x", wantErr: "not found"},
		{name: "invalid int", path: "count", val: "not_a_number", wantErr: "invalid int"},
		{name: "invalid bool", path: "enabled", val: "maybe", wantErr: "invalid bool"},
		{name: "empty path", path: "", val: "x", wantErr: "must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &simpleStruct{}
			err := SetFieldValue(v, tt.path, tt.val)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestSetFieldValue_InvalidDuration(t *testing.T) {
	v := &durationStruct{}
	err := SetFieldValue(v, "timeout", "not_a_duration")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid duration")
}

func TestSetFieldValue_PanicsOnNonPointer(t *testing.T) {
	assert.Panics(t, func() { SetFieldValue(simpleStruct{}, "name", "val") })
}

func TestSetFieldValue_PanicsOnNonStructPointer(t *testing.T) {
	n := 42
	assert.Panics(t, func() { SetFieldValue(&n, "anything", "val") })
}
