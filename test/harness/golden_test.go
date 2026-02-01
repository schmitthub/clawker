package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoldenPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantPath string
	}{
		{
			name:     "empty_name_uses_default",
			input:    "",
			wantPath: "testdata/TestGoldenPath_empty_name_uses_default/output.golden",
		},
		{
			name:     "custom_name",
			input:    "stderr",
			wantPath: "testdata/TestGoldenPath_custom_name/stderr.golden",
		},
		{
			name:     "name_with_extension",
			input:    "output.txt",
			wantPath: "testdata/TestGoldenPath_name_with_extension/output.txt.golden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GoldenPath(t, tt.input)
			assert.Equal(t, tt.wantPath, got)
		})
	}
}

func TestSanitizeTestName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"TestFoo", "TestFoo"},
		{"TestFoo/case1", "TestFoo_case1"},
		{"Test/with/slashes", "Test_with_slashes"},
		{"Test:with:colons", "Test_with_colons"},
		{"Test*with*stars", "Test_with_stars"},
		{"Test?with?questions", "Test_with_questions"},
		{"Test<with>brackets", "Test_with_brackets"},
		{"Test\"with\"quotes", "Test_with_quotes"},
		{"Test|with|pipes", "Test_with_pipes"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeTestName(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCompareGoldenE_MissingFile(t *testing.T) {
	// Create a temp directory for test data
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Test: golden file doesn't exist, update mode disabled
	result, err := compareGoldenE(t, "missing", []byte("hello world"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "golden file not found")
	assert.Equal(t, goldenMissing, result.status)
}

func TestCompareGoldenE_CreateInUpdateMode(t *testing.T) {
	// Create a temp directory for test data
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Enable update mode
	t.Setenv("GOLDEN_UPDATE", "1")

	// Test: create new golden file
	content := []byte("test content")
	result, err := compareGoldenE(t, "create", content)
	require.NoError(t, err)
	assert.Equal(t, goldenCreated, result.status)

	// Verify file was created
	got, err := os.ReadFile(result.path)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestCompareGoldenE_Match(t *testing.T) {
	// Create a temp directory with pre-existing golden file
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create golden file with expected content
	content := []byte("expected content")
	goldenPath := GoldenPath(t, "match")
	err = os.MkdirAll(filepath.Dir(goldenPath), 0755)
	require.NoError(t, err)
	err = os.WriteFile(goldenPath, content, 0644)
	require.NoError(t, err)

	// Test: content matches
	result, err := compareGoldenE(t, "match", content)
	require.NoError(t, err)
	assert.Equal(t, goldenMatch, result.status)
}

func TestCompareGoldenE_Mismatch(t *testing.T) {
	// Create a temp directory with pre-existing golden file
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create golden file with different content
	goldenPath := GoldenPath(t, "mismatch")
	err = os.MkdirAll(filepath.Dir(goldenPath), 0755)
	require.NoError(t, err)
	err = os.WriteFile(goldenPath, []byte("expected"), 0644)
	require.NoError(t, err)

	// Test: content doesn't match (no error returned, mismatch status)
	result, err := compareGoldenE(t, "mismatch", []byte("actual"))
	require.NoError(t, err)
	assert.Equal(t, goldenMismatch, result.status)
	assert.Equal(t, []byte("expected"), result.want)
}

func TestCompareGoldenE_UpdateOnMismatch(t *testing.T) {
	// Create a temp directory with pre-existing golden file
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Enable update mode
	t.Setenv("GOLDEN_UPDATE", "1")

	// Create golden file with old content
	goldenPath := GoldenPath(t, "update")
	err = os.MkdirAll(filepath.Dir(goldenPath), 0755)
	require.NoError(t, err)
	err = os.WriteFile(goldenPath, []byte("old content"), 0644)
	require.NoError(t, err)

	// Test: content should be updated
	newContent := []byte("new content")
	result, err := compareGoldenE(t, "update", newContent)
	require.NoError(t, err)
	assert.Equal(t, goldenUpdated, result.status)

	// Verify file was updated
	got, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	assert.Equal(t, newContent, got)
}

func TestCompareGolden_Integration(t *testing.T) {
	// Integration test for the full CompareGolden function
	// This tests the success path where golden file exists and matches

	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create matching golden file
	content := []byte("test output")
	goldenPath := GoldenPath(t, "output")
	err = os.MkdirAll(filepath.Dir(goldenPath), 0755)
	require.NoError(t, err)
	err = os.WriteFile(goldenPath, content, 0644)
	require.NoError(t, err)

	// This should pass without failing the test
	CompareGolden(t, "output", content)
}

func TestCompareGoldenString_Integration(t *testing.T) {
	// Integration test for CompareGoldenString
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create matching golden file
	content := "hello world"
	goldenPath := GoldenPath(t, "string")
	err = os.MkdirAll(filepath.Dir(goldenPath), 0755)
	require.NoError(t, err)
	err = os.WriteFile(goldenPath, []byte(content), 0644)
	require.NoError(t, err)

	// This should pass without failing the test
	CompareGoldenString(t, "string", content)
}

func TestGoldenAssert(t *testing.T) {
	// Test GoldenAssert with matching file
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create matching golden file
	content := []byte("assert test")
	goldenPath := GoldenPath(t, "assert")
	err = os.MkdirAll(filepath.Dir(goldenPath), 0755)
	require.NoError(t, err)
	err = os.WriteFile(goldenPath, content, 0644)
	require.NoError(t, err)

	// This should pass without failing the test
	GoldenAssert(t, "assert", content)
}
