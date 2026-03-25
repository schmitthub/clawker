package storeui

import (
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSchema is a minimal schema for testing wizard field mapping.
type testSchema struct {
	Name     string            `yaml:"name" label:"Name" desc:"Project name"`
	Enabled  bool              `yaml:"enabled" label:"Enabled" desc:"Whether feature is enabled"`
	Count    int               `yaml:"count" label:"Count" desc:"Item count"`
	Timeout  time.Duration     `yaml:"timeout" label:"Timeout" desc:"Operation timeout"`
	Tags     []string          `yaml:"tags,omitempty" label:"Tags" desc:"Tag list"`
	OptBool  *bool             `yaml:"opt_bool,omitempty" label:"Optional Bool" desc:"An optional bool"`
	Nested   testNested        `yaml:"nested"`
	Metadata map[string]string `yaml:"metadata,omitempty" label:"Metadata" desc:"Key-value metadata"`
}

type testNested struct {
	Mode string `yaml:"mode" label:"Mode" desc:"Operating mode"`
}

func (t testSchema) Fields() storage.FieldSet {
	return storage.NormalizeFields(t)
}

func TestFieldToWizardField_KindMapping(t *testing.T) {
	tests := []struct {
		name           string
		field          Field
		wantKind       tui.WizardFieldKind
		wantOK         bool
		wantDefaultIdx int    // for select fields
		wantPrompt     string // expected prompt text
	}{
		{
			name:       "KindText maps to FieldText",
			field:      Field{Path: "name", Label: "Name", Description: "Project name", Kind: KindText, Value: "foo"},
			wantKind:   tui.FieldText,
			wantOK:     true,
			wantPrompt: "Project name",
		},
		{
			name:       "KindText falls back to Label when no Description",
			field:      Field{Path: "name", Label: "Name", Kind: KindText, Value: "foo"},
			wantKind:   tui.FieldText,
			wantOK:     true,
			wantPrompt: "Name",
		},
		{
			name:     "KindBool maps to FieldConfirm",
			field:    Field{Path: "enabled", Label: "Enabled", Kind: KindBool, Value: "true"},
			wantKind: tui.FieldConfirm,
			wantOK:   true,
		},
		{
			name:     "KindInt maps to FieldText with validator",
			field:    Field{Path: "count", Label: "Count", Kind: KindInt, Value: "42"},
			wantKind: tui.FieldText,
			wantOK:   true,
		},
		{
			name:     "KindDuration maps to FieldText with validator",
			field:    Field{Path: "timeout", Label: "Timeout", Kind: KindDuration, Value: "30s"},
			wantKind: tui.FieldText,
			wantOK:   true,
		},
		{
			name: "KindSelect maps to FieldSelect with DefaultIdx",
			field: Field{
				Path: "mode", Label: "Mode", Kind: KindSelect,
				Value: "snapshot", Options: []string{"bind", "snapshot"},
			},
			wantKind:       tui.FieldSelect,
			wantOK:         true,
			wantDefaultIdx: 1, // "snapshot" is at index 1
		},
		{
			name:     "KindStringSlice maps to FieldText",
			field:    Field{Path: "tags", Label: "Tags", Kind: KindStringSlice, Value: "a, b, c"},
			wantKind: tui.FieldText,
			wantOK:   true,
		},
		{
			name:   "KindMap is skipped",
			field:  Field{Path: "metadata", Label: "Metadata", Kind: KindMap},
			wantOK: false,
		},
		{
			name:   "KindStructSlice is skipped",
			field:  Field{Path: "rules", Label: "Rules", Kind: KindStructSlice},
			wantOK: false,
		},
		{
			name:   "Consumer-defined kind is skipped",
			field:  Field{Path: "custom", Label: "Custom", Kind: storage.KindLast + 1},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf, ok := fieldToWizardField(tt.field)
			assert.Equal(t, tt.wantOK, ok)
			if !ok {
				return
			}
			assert.Equal(t, tt.wantKind, wf.Kind)
			assert.Equal(t, tt.field.Path, wf.ID)

			if tt.wantPrompt != "" {
				assert.Equal(t, tt.wantPrompt, wf.Prompt)
			}
			if tt.field.Kind == KindSelect {
				assert.Equal(t, tt.wantDefaultIdx, wf.DefaultIdx)
			}
		})
	}
}

func TestFieldToWizardField_Validators(t *testing.T) {
	t.Run("int validator rejects non-integers", func(t *testing.T) {
		f := Field{Path: "count", Label: "Count", Kind: KindInt, Value: "0"}
		wf, ok := fieldToWizardField(f)
		require.True(t, ok)
		require.NotNil(t, wf.Validator)

		assert.NoError(t, wf.Validator("42"))
		assert.NoError(t, wf.Validator(""))
		assert.Error(t, wf.Validator("abc"))
		assert.Error(t, wf.Validator("3.14"))
	})

	t.Run("duration validator rejects invalid durations", func(t *testing.T) {
		f := Field{Path: "timeout", Label: "Timeout", Kind: KindDuration, Value: "0s"}
		wf, ok := fieldToWizardField(f)
		require.True(t, ok)
		require.NotNil(t, wf.Validator)

		assert.NoError(t, wf.Validator("30s"))
		assert.NoError(t, wf.Validator("5m"))
		assert.NoError(t, wf.Validator(""))
		assert.Error(t, wf.Validator("abc"))
		assert.Error(t, wf.Validator("30"))
	})

	t.Run("domain validator chains with type validator", func(t *testing.T) {
		domainValidator := func(val string) error {
			if val == "bad" {
				return assert.AnError
			}
			return nil
		}
		f := Field{
			Path: "count", Label: "Count", Kind: KindInt,
			Value: "0", Validator: domainValidator,
		}
		wf, ok := fieldToWizardField(f)
		require.True(t, ok)

		assert.Error(t, wf.Validator("bad"))
		assert.Error(t, wf.Validator("abc"))
		assert.NoError(t, wf.Validator("42"))
	})
}

func TestFieldToWizardField_OverrideApplied(t *testing.T) {
	fields := []Field{
		{Path: "nested.mode", Label: "mode", Kind: KindText, Value: "fast"},
	}

	overrides := []Override{
		{
			Path:    "nested.mode",
			Kind:    Ptr(KindSelect),
			Options: []string{"bind", "snapshot"},
		},
	}

	applied := ApplyOverrides(fields, overrides)
	require.Len(t, applied, 1)

	wf, ok := fieldToWizardField(applied[0])
	require.True(t, ok)
	assert.Equal(t, tui.FieldSelect, wf.Kind)
	assert.Len(t, wf.Options, 2)
}

func TestFilterAndOrder(t *testing.T) {
	fields := []Field{
		{Path: "a", Label: "A"},
		{Path: "b", Label: "B"},
		{Path: "c", Label: "C"},
		{Path: "d", Label: "D"},
	}

	t.Run("filters to specified paths in order", func(t *testing.T) {
		result := filterAndOrder(fields, []string{"c", "a"})
		require.Len(t, result, 2)
		assert.Equal(t, "c", result[0].Path)
		assert.Equal(t, "a", result[1].Path)
	})

	t.Run("missing paths are silently ignored", func(t *testing.T) {
		result := filterAndOrder(fields, []string{"c", "nonexistent", "a"})
		require.Len(t, result, 2)
		assert.Equal(t, "c", result[0].Path)
		assert.Equal(t, "a", result[1].Path)
	})

	t.Run("empty paths returns empty", func(t *testing.T) {
		result := filterAndOrder(fields, []string{})
		assert.Empty(t, result)
	})
}

func TestWizardWriteBack_RoundTrip(t *testing.T) {
	yaml := `name: original
enabled: false
count: 10
timeout: 30s
tags:
  - git
  - curl
nested:
  mode: fast
`

	store, err := storage.NewFromString[testSchema](yaml)
	require.NoError(t, err)

	snap := store.Read()
	assert.Equal(t, "original", snap.Name)
	assert.Equal(t, false, snap.Enabled)
	assert.Equal(t, 10, snap.Count)

	require.NoError(t, store.Set(func(s *testSchema) {
		require.NoError(t, SetFieldValue(s, "name", "updated"))
		require.NoError(t, SetFieldValue(s, "count", "42"))
		require.NoError(t, SetFieldValue(s, "enabled", "true"))
	}))

	snap = store.Read()
	assert.Equal(t, "updated", snap.Name)
	assert.Equal(t, 42, snap.Count)
	assert.True(t, snap.Enabled)
}
