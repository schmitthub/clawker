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

func TestSetFieldValue_PtrBool(t *testing.T) {
	v := &triStateStruct{}

	require.NoError(t, SetFieldValue(v, "enabled", "true"))
	require.NotNil(t, v.Enabled)
	assert.True(t, *v.Enabled)

	require.NoError(t, SetFieldValue(v, "enabled", "false"))
	require.NotNil(t, v.Enabled)
	assert.False(t, *v.Enabled)
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

func TestSetFieldValue_Time(t *testing.T) {
	v := &timeStruct{}
	require.NoError(t, SetFieldValue(v, "seen_at", "2026-01-02T03:04:05Z"))
	assert.Equal(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), v.SeenAt.UTC())

	// Empty round-trips to the zero time (the "unset" representation).
	require.NoError(t, SetFieldValue(v, "seen_at", ""))
	assert.True(t, v.SeenAt.IsZero())
}

func TestSetFieldValue_InvalidTime(t *testing.T) {
	v := &timeStruct{}
	err := SetFieldValue(v, "seen_at", "not_a_time")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid time")
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

func TestGetFieldValue(t *testing.T) {
	t.Run("reads a leaf value", func(t *testing.T) {
		v, err := GetFieldValue(&simpleStruct{Name: "app", Enabled: false, Count: 7}, "name")
		require.NoError(t, err)
		assert.Equal(t, "app", v)
	})

	t.Run("reads a nested value", func(t *testing.T) {
		v, err := GetFieldValue(&nestedStruct{Build: buildSection{Image: "alpine", Packages: nil}}, "build.image")
		require.NoError(t, err)
		assert.Equal(t, "alpine", v)
	})

	t.Run("non-nil pointer field is dereferenced", func(t *testing.T) {
		b := true
		v, err := GetFieldValue(&triStateStruct{Enabled: &b}, "enabled")
		require.NoError(t, err)
		assert.Equal(t, true, v)
	})

	t.Run("nil pointer field yields nil", func(t *testing.T) {
		v, err := GetFieldValue(&triStateStruct{Enabled: nil}, "enabled")
		require.NoError(t, err)
		assert.Nil(t, v)
	})

	t.Run("nil intermediate pointer yields nil", func(t *testing.T) {
		v, err := GetFieldValue(&nilPtrStructParent{Loop: nil}, "loop.max_loops")
		require.NoError(t, err)
		assert.Nil(t, v)
	})

	t.Run("missing field errors", func(t *testing.T) {
		_, err := GetFieldValue(&simpleStruct{Name: "", Enabled: false, Count: 0}, "nope")
		require.Error(t, err)
	})

	t.Run("non-struct intermediate errors", func(t *testing.T) {
		_, err := GetFieldValue(&simpleStruct{Name: "", Enabled: false, Count: 0}, "name.foo")
		require.Error(t, err)
	})

	t.Run("non-pointer input errors", func(t *testing.T) {
		_, err := GetFieldValue(simpleStruct{Name: "", Enabled: false, Count: 0}, "name")
		require.Error(t, err)
	})

	t.Run("empty path errors", func(t *testing.T) {
		_, err := GetFieldValue(&simpleStruct{Name: "", Enabled: false, Count: 0}, "")
		require.Error(t, err)
	})
}
