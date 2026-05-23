package cmdutil

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON_Struct(t *testing.T) {
	type item struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	var buf bytes.Buffer
	err := WriteJSON(&buf, item{Name: "Alice", Age: 30})
	require.NoError(t, err)

	assert.Equal(t, "{\"name\":\"Alice\",\"age\":30}\n", buf.String())
}

func TestWriteJSON_Slice(t *testing.T) {
	type item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	data := []item{
		{ID: 1, Name: "alpha"},
		{ID: 2, Name: "beta"},
	}

	var buf bytes.Buffer
	err := WriteJSON(&buf, data)
	require.NoError(t, err)

	assert.Equal(t, "[{\"id\":1,\"name\":\"alpha\"},{\"id\":2,\"name\":\"beta\"}]\n", buf.String())
}

func TestWriteJSON_EmptySlice(t *testing.T) {
	var buf bytes.Buffer
	err := WriteJSON(&buf, []string{})
	require.NoError(t, err)
	assert.Equal(t, "[]\n", buf.String())
}

func TestWriteJSON_Nil(t *testing.T) {
	var buf bytes.Buffer
	err := WriteJSON(&buf, nil)
	require.NoError(t, err)
	assert.Equal(t, "null\n", buf.String())
}

func TestWriteJSON_Compact(t *testing.T) {
	data := map[string]string{"key": "value"}

	var buf bytes.Buffer
	err := WriteJSON(&buf, data)
	require.NoError(t, err)

	output := buf.String()
	// Compact: a single object on one line, followed by the encoder's trailing newline.
	assert.Equal(t, "{\"key\":\"value\"}\n", output)
	// No 2-space indentation.
	assert.False(t, strings.Contains(output, "  \"key\""), "compact output should not contain indented keys")
}

func TestWriteJSON_NoHTMLEscaping(t *testing.T) {
	data := map[string]string{"image": "<none>:<none>"}

	var buf bytes.Buffer
	err := WriteJSON(&buf, data)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "<none>:<none>", "HTML characters should not be escaped")
	assert.NotContains(t, output, `\u003c`, "should not contain unicode escapes for <")
}
