package list

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdList(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		output     ListOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "no flags",
			input:  "",
			output: ListOptions{},
		},
		{
			name:   "with all flag",
			input:  "--all",
			output: ListOptions{All: true},
		},
		{
			name:   "with shorthand all flag",
			input:  "-a",
			output: ListOptions{All: true},
		},
		{
			name:   "with project flag",
			input:  "--project myproject",
			output: ListOptions{Project: "myproject"},
		},
		{
			name:   "with shorthand project flag",
			input:  "-p myproject",
			output: ListOptions{Project: "myproject"},
		},
		{
			name:   "with all and project flags",
			input:  "-a -p myproject",
			output: ListOptions{All: true, Project: "myproject"},
		},
		{
			name:   "with format flag",
			input:  "--format '{{.Names}}'",
			output: ListOptions{Format: "{{.Names}}"},
		},
		{
			name:   "with format and all flags",
			input:  "-a --format '{{.Name}} {{.Status}}'",
			output: ListOptions{All: true, Format: "{{.Name}} {{.Status}}"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *ListOptions
			cmd := NewCmdList(f, func(_ context.Context, opts *ListOptions) error {
				gotOpts = opts
				return nil
			})

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
			argv := []string{}
			if tt.input != "" {
				argv = splitArgs(tt.input)
			}

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.EqualError(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.output.All, gotOpts.All)
			require.Equal(t, tt.output.Project, gotOpts.Project)
			require.Equal(t, tt.output.Format, gotOpts.Format)
		})
	}
}

func TestCmdList_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdList(f, nil)

	// Test command basics
	require.Equal(t, "list", cmd.Use)
	require.Contains(t, cmd.Aliases, "ls")
	require.Contains(t, cmd.Aliases, "ps")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("all"))
	require.NotNil(t, cmd.Flags().Lookup("project"))
	require.NotNil(t, cmd.Flags().Lookup("format"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("a"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("p"))
}

func TestFormatCreatedTime(t *testing.T) {
	tests := []struct {
		name     string
		duration int64 // seconds ago
		expected string
	}{
		{
			name:     "less than a minute",
			duration: 30,
			expected: "Less than a minute ago",
		},
		{
			name:     "1 minute",
			duration: 60,
			expected: "1 minute ago",
		},
		{
			name:     "5 minutes",
			duration: 300,
			expected: "5 minutes ago",
		},
		{
			name:     "1 hour",
			duration: 3600,
			expected: "1 hour ago",
		},
		{
			name:     "3 hours",
			duration: 10800,
			expected: "3 hours ago",
		},
		{
			name:     "1 day",
			duration: 86400,
			expected: "1 day ago",
		},
		{
			name:     "5 days",
			duration: 432000,
			expected: "5 days ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate timestamp for the given duration ago
			timestamp := time.Now().Unix() - tt.duration
			result := formatCreatedTime(timestamp)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestTruncateImage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "short image name",
			input:    "nginx:latest",
			expected: "nginx:latest",
		},
		{
			name:     "exactly 40 chars",
			input:    "1234567890123456789012345678901234567890",
			expected: "1234567890123456789012345678901234567890",
		},
		{
			name:     "long image name",
			input:    "registry.example.com/organization/very-long-image-name:v1.2.3",
			expected: "registry.example.com/organization/ver...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateImage(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

// splitArgs splits a command string into arguments.
func splitArgs(input string) []string {
	var args []string
	var current string
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(input); i++ {
		c := input[i]
		if c == '"' || c == '\'' {
			if inQuote && c == quoteChar {
				inQuote = false
			} else if !inQuote {
				inQuote = true
				quoteChar = c
			} else {
				current += string(c)
			}
		} else if c == ' ' && !inQuote {
			if current != "" {
				args = append(args, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		args = append(args, current)
	}
	return args
}
