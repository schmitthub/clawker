package storeui

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/tui"
)

func TestApplyOverrides_HiddenRemoval(t *testing.T) {
	fields := []Field{
		{Path: "build.image", Label: "image", Kind: KindText},
		{Path: "build.instructions.env", Label: "env", Kind: KindMap},
		{Path: "build.packages", Label: "packages", Kind: KindStringSlice},
	}
	overrides := []Override{
		{Path: "build.instructions.env", Hidden: true},
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

func TestApplyOverrides_EditorCopied(t *testing.T) {
	called := false
	factory := func(label, value string) any {
		called = true
		return nil
	}
	fields := []Field{
		{Path: "build.env", Label: "env", Kind: KindMap},
	}
	overrides := []Override{
		{Path: "build.env", Editor: factory},
	}

	result := ApplyOverrides(fields, overrides)

	require.Len(t, result, 1)
	require.NotNil(t, result[0].Editor)
	result[0].Editor("test", "value")
	assert.True(t, called, "Editor factory should be copied from override to field")
}

func TestApplyOverrides_MapAndStructSliceNotForcedReadOnly(t *testing.T) {
	fields := []Field{
		{Path: "build.env", Label: "env", Kind: KindMap, ReadOnly: false},
		{Path: "build.rules", Label: "rules", Kind: KindStructSlice, ReadOnly: false},
	}

	result := ApplyOverrides(fields, nil)

	require.Len(t, result, 2)
	assert.False(t, result[0].ReadOnly, "KindMap fields should not be forced read-only")
	assert.False(t, result[1].ReadOnly, "KindStructSlice fields should not be forced read-only")
}

func TestApplyOverrides_PrefixHidingHidesChildren(t *testing.T) {
	fields := []Field{
		{Path: "build.image", Label: "image", Kind: KindText},
		{Path: "build.instructions.env", Label: "env", Kind: KindMap},
		{Path: "build.instructions.copy", Label: "copy", Kind: KindStructSlice},
		{Path: "build.inject.after_from", Label: "after_from", Kind: KindStringSlice},
	}
	overrides := []Override{
		{Path: "build.instructions", Hidden: true},
	}

	result := ApplyOverrides(fields, overrides)

	// "build.instructions.*" prefix = all hidden.
	// Only "build.image" and "build.inject.after_from" remain.
	require.Len(t, result, 2)
	assert.Equal(t, "build.image", result[0].Path)
	assert.Equal(t, "build.inject.after_from", result[1].Path)
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

func TestFieldKindToBrowserKind_CoverAllKinds(t *testing.T) {
	kinds := []struct {
		kind     FieldKind
		expected tui.BrowserFieldKind
	}{
		{KindText, tui.BrowserText},
		{KindBool, tui.BrowserBool},
		{KindSelect, tui.BrowserSelect},
		{KindInt, tui.BrowserInt},
		{KindStringSlice, tui.BrowserStringSlice},
		{KindDuration, tui.BrowserDuration},
		{KindMap, tui.BrowserMap},
		{KindStructSlice, tui.BrowserStructSlice},
	}

	for _, tc := range kinds {
		got := fieldKindToBrowserKind(tc.kind)
		assert.Equal(t, tc.expected, got, "FieldKind %d should map correctly", tc.kind)
	}

	// Verify unknown kinds fall back to BrowserMap (generic fallback).
	assert.Equal(t, tui.BrowserMap, fieldKindToBrowserKind(FieldKind(99)))
}
