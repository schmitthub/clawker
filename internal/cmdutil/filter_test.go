package cmdutil

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFilters(t *testing.T) {
	tests := []struct {
		name    string
		raw     []string
		want    []Filter
		wantErr string
	}{
		{
			name: "single filter",
			raw:  []string{"status=running"},
			want: []Filter{{Key: "status", Value: "running"}},
		},
		{
			name: "multiple filters",
			raw:  []string{"name=foo", "status=running"},
			want: []Filter{
				{Key: "name", Value: "foo"},
				{Key: "status", Value: "running"},
			},
		},
		{
			name: "value contains equals",
			raw:  []string{"key=val=ue"},
			want: []Filter{{Key: "key", Value: "val=ue"}},
		},
		{
			name: "empty value is valid",
			raw:  []string{"status="},
			want: []Filter{{Key: "status", Value: ""}},
		},
		{
			name: "nil input",
			raw:  nil,
			want: []Filter{},
		},
		{
			name: "empty slice",
			raw:  []string{},
			want: []Filter{},
		},
		{
			name:    "missing equals sign",
			raw:     []string{"invalid"},
			wantErr: "invalid filter format",
		},
		{
			name:    "empty key",
			raw:     []string{"=value"},
			wantErr: "empty key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFilters(tt.raw)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.IsType(t, &FlagError{}, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateFilterKeys(t *testing.T) {
	validKeys := []string{"name", "status", "label"}

	tests := []struct {
		name    string
		filters []Filter
		wantErr string
	}{
		{
			name:    "valid key",
			filters: []Filter{{Key: "status", Value: "running"}},
		},
		{
			name:    "invalid key",
			filters: []Filter{{Key: "bogus", Value: "val"}},
			wantErr: "invalid filter key",
		},
		{
			name: "multiple valid filters",
			filters: []Filter{
				{Key: "name", Value: "foo"},
				{Key: "status", Value: "running"},
				{Key: "label", Value: "env=prod"},
			},
		},
		{
			name:    "empty filters",
			filters: []Filter{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFilterKeys(tt.filters, validKeys)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Contains(t, err.Error(), "name, status, label")
				assert.IsType(t, &FlagError{}, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestAddFilterFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	ff := AddFilterFlags(cmd)
	require.NotNil(t, ff)

	flag := cmd.Flags().Lookup("filter")
	require.NotNil(t, flag, "--filter flag should be registered")
	assert.Equal(t, "filter", flag.Name)
	assert.Equal(t, "[]", flag.DefValue)
}

func TestFilterFlags_Parse(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	ff := AddFilterFlags(cmd)

	// Simulate flag parsing with --filter values.
	cmd.SetArgs([]string{"--filter", "name=myapp", "--filter", "status=running"})
	require.NoError(t, cmd.Execute())

	filters, err := ff.Parse()
	require.NoError(t, err)
	assert.Equal(t, []Filter{
		{Key: "name", Value: "myapp"},
		{Key: "status", Value: "running"},
	}, filters)
}
