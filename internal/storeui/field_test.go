package storeui

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyOverrides_HiddenRemoval(t *testing.T) {
	fields := []Field{
		{Path: "build.image", Label: "image", Kind: KindText},
		{Path: "build.instructions", Label: "instructions", Kind: KindComplex},
		{Path: "build.packages", Label: "packages", Kind: KindStringSlice},
	}
	overrides := []Override{
		{Path: "build.instructions", Hidden: true},
	}

	result := ApplyOverrides(fields, overrides)

	require.Len(t, result, 2)
	assert.Equal(t, "build.image", result[0].Path)
	assert.Equal(t, "build.packages", result[1].Path)
}

func TestApplyOverrides_OrderReorder(t *testing.T) {
	fields := []Field{
		{Path: "a", Label: "A", Order: 0},
		{Path: "b", Label: "B", Order: 1},
		{Path: "c", Label: "C", Order: 2},
	}
	overrides := []Override{
		{Path: "c", Order: ptr(0)},
		{Path: "a", Order: ptr(2)},
	}

	result := ApplyOverrides(fields, overrides)

	require.Len(t, result, 3)
	assert.Equal(t, "c", result[0].Path)
	assert.Equal(t, "b", result[1].Path)
	assert.Equal(t, "a", result[2].Path)
}

func TestApplyOverrides_NoMatchHarmless(t *testing.T) {
	fields := []Field{
		{Path: "build.image", Label: "image", Kind: KindText},
	}
	overrides := []Override{
		{Path: "nonexistent.field", Label: ptr("Doesn't Exist")},
	}

	result := ApplyOverrides(fields, overrides)

	require.Len(t, result, 1)
	assert.Equal(t, "image", result[0].Label)
}

func TestApplyOverrides_NilPointerNoClobber(t *testing.T) {
	fields := []Field{
		{Path: "build.image", Label: "original", Description: "original desc", Kind: KindText, Required: true, ReadOnly: false, Order: 5},
	}
	// Override with only Label set — all other pointer fields are nil and should not clobber.
	overrides := []Override{
		{Path: "build.image", Label: ptr("New Label")},
	}

	result := ApplyOverrides(fields, overrides)

	require.Len(t, result, 1)
	f := result[0]
	assert.Equal(t, "New Label", f.Label)
	assert.Equal(t, "original desc", f.Description)
	assert.Equal(t, KindText, f.Kind)
	assert.True(t, f.Required)
	assert.False(t, f.ReadOnly)
	assert.Equal(t, 5, f.Order)
}

func TestApplyOverrides_KindOverride(t *testing.T) {
	fields := []Field{
		{Path: "workspace.default_mode", Label: "default_mode", Kind: KindText},
	}
	overrides := []Override{
		{Path: "workspace.default_mode", Kind: ptr(KindSelect), Options: []string{"bind", "snapshot"}},
	}

	result := ApplyOverrides(fields, overrides)

	require.Len(t, result, 1)
	assert.Equal(t, KindSelect, result[0].Kind)
	assert.Equal(t, []string{"bind", "snapshot"}, result[0].Options)
}

func TestApplyOverrides_ValidatorOverride(t *testing.T) {
	sentinel := errors.New("sentinel")
	validator := func(s string) error {
		if s == "bad" {
			return sentinel
		}
		return nil
	}
	fields := []Field{
		{Path: "build.image", Label: "image", Kind: KindText},
	}
	overrides := []Override{
		{Path: "build.image", Validator: validator},
	}

	result := ApplyOverrides(fields, overrides)

	require.Len(t, result, 1)
	require.NotNil(t, result[0].Validator)
	assert.NoError(t, result[0].Validator("good"))
	assert.ErrorIs(t, result[0].Validator("bad"), sentinel)
}

func TestApplyOverrides_MultipleOverrides(t *testing.T) {
	fields := []Field{
		{Path: "a", Label: "A", Order: 0},
		{Path: "b", Label: "B", Order: 1},
		{Path: "c", Label: "C", Order: 2},
	}
	overrides := []Override{
		{Path: "a", Label: ptr("Alpha"), Hidden: true},
		{Path: "b", Label: ptr("Beta"), Description: ptr("The B field")},
		{Path: "c", ReadOnly: ptr(true)},
	}

	result := ApplyOverrides(fields, overrides)

	require.Len(t, result, 2) // "a" is hidden
	assert.Equal(t, "Beta", result[0].Label)
	assert.Equal(t, "The B field", result[0].Description)
	assert.True(t, result[1].ReadOnly)
}

func TestApplyOverrides_EmptyInputs(t *testing.T) {
	assert.Empty(t, ApplyOverrides(nil, nil))

	fields := []Field{{Path: "a", Label: "A"}}
	result := ApplyOverrides(fields, nil)
	require.Len(t, result, 1)
	assert.Equal(t, "A", result[0].Label)
}

func TestApplyOverrides_PreservesOriginal(t *testing.T) {
	fields := []Field{
		{Path: "a", Label: "Original"},
	}
	overrides := []Override{
		{Path: "a", Label: ptr("Modified")},
	}

	result := ApplyOverrides(fields, overrides)

	// Original slice should not be mutated.
	assert.Equal(t, "Original", fields[0].Label)
	// Result should have the override applied.
	assert.Equal(t, "Modified", result[0].Label)
}

func TestApplyOverrides_ComplexKindForcesReadOnly(t *testing.T) {
	fields := []Field{
		{Path: "build.instructions", Label: "instructions", Kind: KindComplex, ReadOnly: false},
	}

	result := ApplyOverrides(fields, nil)

	require.Len(t, result, 1)
	assert.True(t, result[0].ReadOnly, "KindComplex fields must always be read-only")
}

func TestApplyOverrides_DuplicatePathsPanics(t *testing.T) {
	fields := []Field{
		{Path: "build.image", Label: "image", Kind: KindText},
	}
	overrides := []Override{
		{Path: "build.image", Label: ptr("First")},
		{Path: "build.image", Label: ptr("Second")},
	}

	assert.Panics(t, func() {
		ApplyOverrides(fields, overrides)
	})
}
