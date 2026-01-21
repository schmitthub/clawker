package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple args",
			input: "foo bar",
			want:  []string{"foo", "bar"},
		},
		{
			name:  "single quotes",
			input: "foo 'bar baz'",
			want:  []string{"foo", "bar baz"},
		},
		{
			name:  "double quotes",
			input: `foo "bar baz"`,
			want:  []string{"foo", "bar baz"},
		},
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  nil,
		},
		{
			name:  "mixed quotes",
			input: `foo 'bar' "baz"`,
			want:  []string{"foo", "bar", "baz"},
		},
		{
			name:  "quoted with spaces",
			input: `"hello world" test`,
			want:  []string{"hello world", "test"},
		},
		{
			name:  "single arg",
			input: "foo",
			want:  []string{"foo"},
		},
		{
			name:  "multiple spaces between args",
			input: "foo   bar",
			want:  []string{"foo", "bar"},
		},
		{
			name:  "leading spaces",
			input: "  foo bar",
			want:  []string{"foo", "bar"},
		},
		{
			name:  "trailing spaces",
			input: "foo bar  ",
			want:  []string{"foo", "bar"},
		},
		{
			name:  "quote in middle joins",
			input: "foo'bar'",
			want:  []string{"foobar"},
		},
		{
			name:  "nested single quotes in double",
			input: `"foo 'bar' baz"`,
			want:  []string{"foo 'bar' baz"},
		},
		{
			name:  "nested double quotes in single",
			input: `'foo "bar" baz'`,
			want:  []string{`foo "bar" baz`},
		},
		{
			name:  "empty quotes",
			input: `""`,
			want:  nil,
		},
		{
			name:  "empty quotes with other args",
			input: `foo "" bar`,
			want:  []string{"foo", "bar"},
		},
		{
			name:  "complex command line",
			input: `--flag "value with spaces" -f 'another value'`,
			want:  []string{"--flag", "value with spaces", "-f", "another value"},
		},
		{
			name:  "path with spaces",
			input: `cp "/path/to/my file.txt" /dest`,
			want:  []string{"cp", "/path/to/my file.txt", "/dest"},
		},
		{
			name:  "equals in quoted value",
			input: `--env="FOO=bar baz"`,
			want:  []string{"--env=FOO=bar baz"},
		},
		{
			name:  "adjacent quoted sections",
			input: `"foo""bar"`,
			want:  []string{"foobar"},
		},
		{
			name:  "tab separator",
			input: "foo\tbar",
			want:  []string{"foo\tbar"},
		},
		{
			name:  "newline in input",
			input: "foo\nbar",
			want:  []string{"foo\nbar"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitArgs(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
