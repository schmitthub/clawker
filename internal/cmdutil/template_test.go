package cmdutil

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultFuncMap(t *testing.T) {
	fm := DefaultFuncMap()

	t.Run("json", func(t *testing.T) {
		fn := fm["json"].(func(any) (string, error))
		got, err := fn(map[string]string{"name": "foo"})
		require.NoError(t, err)
		assert.Equal(t, `{"name":"foo"}`, got)
	})

	t.Run("json_error", func(t *testing.T) {
		fn := fm["json"].(func(any) (string, error))
		// Channels cannot be marshaled to JSON.
		_, err := fn(make(chan int))
		assert.Error(t, err)
	})

	t.Run("upper", func(t *testing.T) {
		fn := fm["upper"].(func(string) string)
		assert.Equal(t, "HELLO", fn("hello"))
	})

	t.Run("lower", func(t *testing.T) {
		fn := fm["lower"].(func(string) string)
		assert.Equal(t, "hello", fn("HELLO"))
	})

	t.Run("title", func(t *testing.T) {
		fn := fm["title"].(func(string) string)
		assert.Equal(t, "Hello", fn("hello"))
		assert.Equal(t, "", fn(""))
	})

	t.Run("title_multibyte", func(t *testing.T) {
		fn := fm["title"].(func(string) string)
		// ñ is a multi-byte UTF-8 character (2 bytes)
		assert.Equal(t, "Ñoño", fn("ñoño"))
	})

	t.Run("split", func(t *testing.T) {
		fn := fm["split"].(func(string, string) []string)
		assert.Equal(t, []string{"a", "b", "c"}, fn("a,b,c", ","))
	})

	t.Run("join", func(t *testing.T) {
		fn := fm["join"].(func([]string, string) string)
		assert.Equal(t, "a,b,c", fn([]string{"a", "b", "c"}, ","))
	})

	t.Run("truncate_longer", func(t *testing.T) {
		fn := fm["truncate"].(func(string, int) string)
		assert.Equal(t, "hello...", fn("hello world", 8))
	})

	t.Run("truncate_shorter", func(t *testing.T) {
		fn := fm["truncate"].(func(string, int) string)
		assert.Equal(t, "hi", fn("hi", 8))
	})

	t.Run("truncate_negative", func(t *testing.T) {
		fn := fm["truncate"].(func(string, int) string)
		assert.Equal(t, "", fn("hello", -1))
	})

	t.Run("truncate_multibyte", func(t *testing.T) {
		fn := fm["truncate"].(func(string, int) string)
		// "café world" is 10 runes; truncate to 8 runes should give "café ..."
		assert.Equal(t, "café ...", fn("café world", 8))
	})

	t.Run("truncate_multibyte_short", func(t *testing.T) {
		fn := fm["truncate"].(func(string, int) string)
		// n <= 3 path with multi-byte characters
		assert.Equal(t, "ñoñ", fn("ñoño", 3))
	})

	t.Run("all_functions_registered", func(t *testing.T) {
		expected := []string{"json", "upper", "lower", "title", "split", "join", "truncate"}
		for _, name := range expected {
			assert.Contains(t, fm, name, "missing function: %s", name)
		}
	})

	t.Run("functions_usable_in_template", func(t *testing.T) {
		// Verify all functions can be used in a real template parse.
		tmpl, err := template.New("test").Funcs(fm).Parse(
			`{{upper .Name}} {{lower .Tag}} {{title .Status}} {{truncate .Desc 5}} {{json .}}`,
		)
		require.NoError(t, err)

		var buf bytes.Buffer
		data := map[string]string{
			"Name": "foo", "Tag": "LATEST", "Status": "running", "Desc": "a long description",
		}
		err = tmpl.Execute(&buf, data)
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "FOO")
		assert.Contains(t, buf.String(), "latest")
		assert.Contains(t, buf.String(), "Running")
		assert.Contains(t, buf.String(), "a ...")
	})
}

func TestExecuteTemplate_Plain(t *testing.T) {
	f, err := ParseFormat("{{.Name}} {{.ID}}")
	require.NoError(t, err)

	items := []any{
		map[string]string{"Name": "foo", "ID": "123"},
		map[string]string{"Name": "bar", "ID": "456"},
	}

	var buf bytes.Buffer
	err = ExecuteTemplate(&buf, f, items)
	require.NoError(t, err)
	assert.Equal(t, "foo 123\nbar 456\n", buf.String())
}

func TestExecuteTemplate_TableTemplate(t *testing.T) {
	f, err := ParseFormat("table {{.Name}}\t{{.ID}}")
	require.NoError(t, err)

	items := []any{
		map[string]string{"Name": "foo", "ID": "123"},
		map[string]string{"Name": "barbaz", "ID": "456"},
	}

	var buf bytes.Buffer
	err = ExecuteTemplate(&buf, f, items)
	require.NoError(t, err)

	// Tabwriter should align columns with padding.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)

	// Both lines should have the same column alignment for the second field.
	// "foo" and "barbaz" differ in length, so tabwriter pads them.
	assert.Contains(t, lines[0], "foo")
	assert.Contains(t, lines[0], "123")
	assert.Contains(t, lines[1], "barbaz")
	assert.Contains(t, lines[1], "456")

	// The ID column should start at the same position in both lines.
	idx0 := strings.Index(lines[0], "123")
	idx1 := strings.Index(lines[1], "456")
	assert.Equal(t, idx0, idx1, "columns should be aligned: line0=%q line1=%q", lines[0], lines[1])
}

func TestExecuteTemplate_WithFunctions(t *testing.T) {
	f, err := ParseFormat("{{upper .Name}}")
	require.NoError(t, err)

	items := []any{
		map[string]string{"Name": "foo"},
	}

	var buf bytes.Buffer
	err = ExecuteTemplate(&buf, f, items)
	require.NoError(t, err)
	assert.Equal(t, "FOO\n", buf.String())
}

func TestExecuteTemplate_InvalidTemplate(t *testing.T) {
	f, err := ParseFormat("{{.Name")
	require.NoError(t, err)

	var buf bytes.Buffer
	err = ExecuteTemplate(&buf, f, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid template")
}

func TestExecuteTemplate_ExecutionError(t *testing.T) {
	f, err := ParseFormat("{{.MissingField}}")
	require.NoError(t, err)

	items := []any{
		struct{ Name string }{Name: "foo"},
	}

	var buf bytes.Buffer
	err = ExecuteTemplate(&buf, f, items)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template execution failed")
}

func TestExecuteTemplate_EmptyItems(t *testing.T) {
	f, err := ParseFormat("{{.Name}}")
	require.NoError(t, err)

	var buf bytes.Buffer
	err = ExecuteTemplate(&buf, f, []any{})
	require.NoError(t, err)
	assert.Empty(t, buf.String())
}

// errWriter is an io.Writer that succeeds for the first n writes, then returns an error.
type errWriter struct {
	remaining int
}

func (w *errWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, fmt.Errorf("write error")
	}
	w.remaining--
	return len(p), nil
}

func TestExecuteTemplate_WriteError(t *testing.T) {
	f, err := ParseFormat("{{.Name}}")
	require.NoError(t, err)

	items := []any{
		map[string]string{"Name": "foo"},
	}

	// Allow template Execute to succeed (1 write), then fail on Fprintln.
	err = ExecuteTemplate(&errWriter{remaining: 1}, f, items)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing output")
}
