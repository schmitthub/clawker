package text

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"no truncation needed", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncate with ellipsis", "hello world", 8, "hello..."},
		{"very short width", "hello", 3, "hel"},
		{"zero width", "hello", 0, ""},
		{"negative width", "hello", -1, ""},
		{"empty string", "", 10, ""},
		{"unicode fits", "Hello世界", 8, "Hello世界"},
		{"with ANSI no truncation", "\x1b[31mhello\x1b[0m", 5, "\x1b[31mhello\x1b[0m"},
		{"truncate with ANSI", "\x1b[31mhello world\x1b[0m", 8, "hello..."},
		{"width 1", "hello", 1, "h"},
		{"width 2", "hello", 2, "he"},
		{"width 4 with ellipsis", "hello world", 4, "h..."},
		{"width 3 with ANSI", "\x1b[31mhello\x1b[0m", 3, "hel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Truncate(tt.input, tt.width))
		})
	}
}

func TestTruncateMiddle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"no truncation needed", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncate middle", "abcdefghij", 7, "ab...ij"},
		{"short width uses regular truncate", "hello", 4, "h..."},
		{"zero width", "hello", 0, ""},
		{"negative width", "hello", -1, ""},
		{"path example", "/Users/foo/bar", 10, "/Us.../bar"},
		{"empty string", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, TruncateMiddle(tt.input, tt.width))
		})
	}
}

func TestPadRight(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"add padding", "hello", 10, "hello     "},
		{"no padding needed", "hello", 5, "hello"},
		{"already wider", "hello world", 5, "hello world"},
		{"empty string", "", 5, "     "},
		{"zero width", "hello", 0, "hello"},
		{"with ANSI", "\x1b[31mhi\x1b[0m", 5, "\x1b[31mhi\x1b[0m   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PadRight(tt.input, tt.width))
		})
	}
}

func TestPadLeft(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"add padding", "hello", 10, "     hello"},
		{"no padding needed", "hello", 5, "hello"},
		{"already wider", "hello world", 5, "hello world"},
		{"empty string", "", 5, "     "},
		{"zero width", "hello", 0, "hello"},
		{"with ANSI", "\x1b[31mhi\x1b[0m", 5, "   \x1b[31mhi\x1b[0m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PadLeft(tt.input, tt.width))
		})
	}
}

func TestPadCenter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"center padding", "hi", 6, "  hi  "},
		{"odd padding", "hi", 7, "  hi   "},
		{"no padding needed", "hello", 5, "hello"},
		{"already wider", "hello world", 5, "hello world"},
		{"empty string", "", 4, "    "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PadCenter(tt.input, tt.width))
		})
	}
}

func TestWordWrap(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"no wrap needed", "hello world", 20, "hello world"},
		{"wrap at word", "hello world foo", 10, "hello\nworld foo"},
		{"multiple wraps", "one two three four", 8, "one two\nthree\nfour"},
		{"preserves newlines", "hello\nworld", 20, "hello\nworld"},
		{"empty string", "", 10, ""},
		{"zero width", "hello", 0, "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, WordWrap(tt.input, tt.width))
		})
	}
}

func TestWrapLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  []string
	}{
		{"single line", "hello world", 20, []string{"hello world"}},
		{"wrap needed", "hello world", 6, []string{"hello", "world"}},
		{"multiple paragraphs", "hello\nworld", 20, []string{"hello", "world"}},
		{"empty paragraph", "hello\n\nworld", 20, []string{"hello", "", "world"}},
		{"empty string", "", 10, []string{""}},
		{"zero width", "hello", 0, []string{"hello"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, WrapLines(tt.input, tt.width))
		})
	}
}

func TestCountVisibleWidth(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"plain text", "hello", 5},
		{"with ANSI", "\x1b[31mhello\x1b[0m", 5},
		{"multiple ANSI", "\x1b[1m\x1b[31mhi\x1b[0m", 2},
		{"empty", "", 0},
		{"unicode", "世界", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CountVisibleWidth(tt.input))
		})
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no ANSI", "hello", "hello"},
		{"color code", "\x1b[31mhello\x1b[0m", "hello"},
		{"bold", "\x1b[1mbold\x1b[0m", "bold"},
		{"multiple codes", "\x1b[1m\x1b[31mhi\x1b[0m", "hi"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, StripANSI(tt.input))
		})
	}
}

func TestIndent(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		spaces int
		want   string
	}{
		{"single line", "hello", 2, "  hello"},
		{"multi line", "hello\nworld", 2, "  hello\n  world"},
		{"empty line preserved", "hello\n\nworld", 2, "  hello\n\n  world"},
		{"empty string", "", 2, ""},
		{"zero spaces", "hello", 0, "hello"},
		{"four spaces", "hello", 4, "    hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Indent(tt.input, tt.spaces))
		})
	}
}

func TestJoinNonEmpty(t *testing.T) {
	tests := []struct {
		name  string
		sep   string
		parts []string
		want  string
	}{
		{"all non-empty", " ", []string{"a", "b", "c"}, "a b c"},
		{"some empty", " ", []string{"a", "", "c"}, "a c"},
		{"all empty", " ", []string{"", "", ""}, ""},
		{"single part", " ", []string{"a"}, "a"},
		{"no parts", " ", []string{}, ""},
		{"different sep", ", ", []string{"a", "b"}, "a, b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, JoinNonEmpty(tt.sep, tt.parts...))
		})
	}
}

func TestRepeat(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"repeat 3", "ab", 3, "ababab"},
		{"repeat 1", "ab", 1, "ab"},
		{"repeat 0", "ab", 0, ""},
		{"negative", "ab", -1, ""},
		{"empty string", "", 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Repeat(tt.s, tt.n))
		})
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"single line", "hello", "hello"},
		{"multi line", "hello\nworld", "hello"},
		{"empty", "", ""},
		{"only newline", "\n", ""},
		{"multiple newlines", "a\nb\nc", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FirstLine(tt.input))
		})
	}
}

func TestLineCount(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"single line", "hello", 1},
		{"two lines", "hello\nworld", 2},
		{"three lines", "a\nb\nc", 3},
		{"empty", "", 0},
		{"trailing newline", "hello\n", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, LineCount(tt.input))
		})
	}
}
