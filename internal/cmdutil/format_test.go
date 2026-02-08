package cmdutil

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFormat(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantMode string
		wantTmpl string
		wantErr  string
	}{
		{
			name:     "empty string",
			raw:      "",
			wantMode: ModeDefault,
		},
		{
			name:     "table",
			raw:      "table",
			wantMode: ModeTable,
		},
		{
			name:     "json",
			raw:      "json",
			wantMode: ModeJSON,
		},
		{
			name:     "single template field",
			raw:      "{{.Name}}",
			wantMode: ModeTemplate,
			wantTmpl: "{{.Name}}",
		},
		{
			name:     "multi-field template",
			raw:      "{{.Name}} {{.ID}}",
			wantMode: ModeTemplate,
			wantTmpl: "{{.Name}} {{.ID}}",
		},
		{
			name:     "table template",
			raw:      "table {{.Name}}\t{{.ID}}",
			wantMode: ModeTableTemplate,
			wantTmpl: "{{.Name}}\t{{.ID}}",
		},
		{
			name:    "invalid bare word",
			raw:     "invalid",
			wantErr: `invalid format string: "invalid"`,
		},
		{
			name:    "yaml is not supported",
			raw:     "yaml",
			wantErr: `invalid format string: "yaml"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFormat(tt.raw)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				var flagErr *FlagError
				assert.True(t, errors.As(err, &flagErr), "error should be a FlagError")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMode, got.mode)
			assert.Equal(t, tt.wantTmpl, got.template)
		})
	}
}

func TestFormat_Methods(t *testing.T) {
	tests := []struct {
		name            string
		format          Format
		isDefault       bool
		isJSON          bool
		isTemplate      bool
		isTableTemplate bool
		template        string
	}{
		{
			name:      "ModeDefault",
			format:    Format{mode: ModeDefault},
			isDefault: true,
		},
		{
			name:      "ModeTable",
			format:    Format{mode: ModeTable},
			isDefault: true,
		},
		{
			name:   "ModeJSON",
			format: Format{mode: ModeJSON},
			isJSON: true,
		},
		{
			name:       "ModeTemplate",
			format:     Format{mode: ModeTemplate, template: "{{.Name}}"},
			isTemplate: true,
			template:   "{{.Name}}",
		},
		{
			name:            "ModeTableTemplate",
			format:          Format{mode: ModeTableTemplate, template: "{{.Name}}\t{{.ID}}"},
			isTemplate:      true,
			isTableTemplate: true,
			template:        "{{.Name}}\t{{.ID}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.isDefault, tt.format.IsDefault(), "IsDefault")
			assert.Equal(t, tt.isJSON, tt.format.IsJSON(), "IsJSON")
			assert.Equal(t, tt.isTemplate, tt.format.IsTemplate(), "IsTemplate")
			assert.Equal(t, tt.isTableTemplate, tt.format.IsTableTemplate(), "IsTableTemplate")
			assert.Equal(t, tt.template, tt.format.Template(), "Template")
		})
	}
}

func TestAddFormatFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	ff := AddFormatFlags(cmd)
	require.NotNil(t, ff)

	// Verify flags are registered.
	assert.NotNil(t, cmd.Flags().Lookup("format"), "--format flag should exist")
	assert.NotNil(t, cmd.Flags().Lookup("json"), "--json flag should exist")
	assert.NotNil(t, cmd.Flags().Lookup("quiet"), "--quiet flag should exist")

	// Verify shorthand.
	qFlag := cmd.Flags().Lookup("quiet")
	require.NotNil(t, qFlag)
	assert.Equal(t, "q", qFlag.Shorthand)
}

func TestAddFormatFlags_Validation(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantErr   string
		checkFunc func(t *testing.T, ff *FormatFlags)
	}{
		{
			name:    "json and format are mutually exclusive",
			args:    []string{"--json", "--format", "json"},
			wantErr: "--format and --json are mutually exclusive",
		},
		{
			name:    "quiet and json are mutually exclusive",
			args:    []string{"--quiet", "--json"},
			wantErr: "--quiet and --format/--json are mutually exclusive",
		},
		{
			name:    "quiet and format are mutually exclusive",
			args:    []string{"--quiet", "--format", "json"},
			wantErr: "--quiet and --format/--json are mutually exclusive",
		},
		{
			name: "json alone",
			args: []string{"--json"},
			checkFunc: func(t *testing.T, ff *FormatFlags) {
				assert.True(t, ff.Format.IsJSON())
				assert.False(t, ff.Quiet)
			},
		},
		{
			name: "format json alone",
			args: []string{"--format", "json"},
			checkFunc: func(t *testing.T, ff *FormatFlags) {
				assert.True(t, ff.Format.IsJSON())
				assert.False(t, ff.Quiet)
			},
		},
		{
			name: "quiet alone",
			args: []string{"--quiet"},
			checkFunc: func(t *testing.T, ff *FormatFlags) {
				assert.True(t, ff.Quiet)
				assert.True(t, ff.Format.IsDefault())
			},
		},
		{
			name: "no flags",
			args: nil,
			checkFunc: func(t *testing.T, ff *FormatFlags) {
				assert.False(t, ff.Quiet)
				assert.True(t, ff.Format.IsDefault())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{
				Use:  "test",
				RunE: func(cmd *cobra.Command, args []string) error { return nil },
			}
			ff := AddFormatFlags(cmd)

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.checkFunc != nil {
				tt.checkFunc(t, ff)
			}
		})
	}
}

func TestAddFormatFlags_PreservesExistingPreRunE(t *testing.T) {
	existingCalled := false

	cmd := &cobra.Command{
		Use: "test",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			existingCalled = true
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	ff := AddFormatFlags(cmd)

	cmd.SetArgs(nil)
	err := cmd.Execute()
	require.NoError(t, err)

	assert.True(t, existingCalled, "existing PreRunE should have been called")
	assert.True(t, ff.Format.IsDefault(), "format should be default")
}

func TestFormatFlags_ConvenienceMethods(t *testing.T) {
	tests := []struct {
		name            string
		format          Format
		quiet           bool
		isJSON          bool
		isTemplate      bool
		isDefault       bool
		isTableTemplate bool
	}{
		{
			name:      "default",
			format:    Format{mode: ModeDefault},
			isDefault: true,
		},
		{
			name:   "json",
			format: Format{mode: ModeJSON},
			isJSON: true,
		},
		{
			name:       "template",
			format:     Format{mode: ModeTemplate, template: "{{.Name}}"},
			isTemplate: true,
		},
		{
			name:            "table template",
			format:          Format{mode: ModeTableTemplate, template: "{{.Name}}\t{{.ID}}"},
			isTemplate:      true,
			isTableTemplate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ff := &FormatFlags{Format: tt.format, Quiet: tt.quiet}
			assert.Equal(t, tt.isJSON, ff.IsJSON(), "IsJSON")
			assert.Equal(t, tt.isTemplate, ff.IsTemplate(), "IsTemplate")
			assert.Equal(t, tt.isDefault, ff.IsDefault(), "IsDefault")
			assert.Equal(t, tt.isTableTemplate, ff.IsTableTemplate(), "IsTableTemplate")
			assert.Equal(t, tt.format, ff.Template(), "Template")
		})
	}
}

func TestToAny(t *testing.T) {
	type item struct{ Name string }

	items := []item{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	result := ToAny(items)

	require.Len(t, result, 3)
	assert.Equal(t, item{Name: "a"}, result[0])
	assert.Equal(t, item{Name: "b"}, result[1])
	assert.Equal(t, item{Name: "c"}, result[2])

	t.Run("empty", func(t *testing.T) {
		empty := ToAny([]string{})
		assert.Empty(t, empty)
	})
}

func TestAddFormatFlags_ExistingPreRunE_Error(t *testing.T) {
	cmd := &cobra.Command{
		Use: "test",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("upstream error")
		},
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	_ = AddFormatFlags(cmd)

	cmd.SetArgs(nil)
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upstream error")
}
