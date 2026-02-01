package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentHash_Stability(t *testing.T) {
	dockerfile := []byte("FROM alpine:latest\nRUN echo hello\n")

	h1, err := ContentHash(dockerfile, nil, "")
	require.NoError(t, err)

	h2, err := ContentHash(dockerfile, nil, "")
	require.NoError(t, err)

	assert.Equal(t, h1, h2, "same input should produce same hash")
	assert.Len(t, h1, 12, "hash should be 12 hex characters")
}

func TestContentHash_Sensitivity(t *testing.T) {
	df1 := []byte("FROM alpine:latest\nRUN echo hello\n")
	df2 := []byte("FROM alpine:latest\nRUN echo world\n")

	h1, err := ContentHash(df1, nil, "")
	require.NoError(t, err)

	h2, err := ContentHash(df2, nil, "")
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2, "different Dockerfiles should produce different hashes")
}

func TestContentHash_IncludeFiles(t *testing.T) {
	dir := t.TempDir()

	// Create two include files
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bbb"), 0644))

	dockerfile := []byte("FROM alpine:latest\n")

	// Hash with includes
	h1, err := ContentHash(dockerfile, []string{"a.txt", "b.txt"}, dir)
	require.NoError(t, err)

	// Hash without includes should differ
	h2, err := ContentHash(dockerfile, nil, dir)
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2, "adding includes should change the hash")

	// Hash with same includes in different order should be the same (sorted)
	h3, err := ContentHash(dockerfile, []string{"b.txt", "a.txt"}, dir)
	require.NoError(t, err)

	assert.Equal(t, h1, h3, "include order should not affect hash")
}

func TestContentHash_IncludeContentChange(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("v1"), 0644))
	dockerfile := []byte("FROM alpine:latest\n")

	h1, err := ContentHash(dockerfile, []string{"file.txt"}, dir)
	require.NoError(t, err)

	// Change file content
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("v2"), 0644))

	h2, err := ContentHash(dockerfile, []string{"file.txt"}, dir)
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2, "changing include file content should change hash")
}

func TestContentHash_MissingInclude(t *testing.T) {
	dockerfile := []byte("FROM alpine:latest\n")

	// Should not error on missing include — just hashes the path
	h, err := ContentHash(dockerfile, []string{"nonexistent.txt"}, "/tmp")
	require.NoError(t, err)
	assert.Len(t, h, 12)
}
