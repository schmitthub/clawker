package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// GoldenPath returns the path to a golden file for the current test.
// Golden files are stored in a "testdata" directory alongside the test file.
//
// The path is constructed as: testdata/<testname>/<name>.golden
//
// For example, if the current test is TestFoo and name is "output",
// the path would be testdata/TestFoo/output.golden
//
// If name is empty, defaults to "output":
//
//	GoldenPath(t, "") → testdata/TestFoo/output.golden
//	GoldenPath(t, "stderr") → testdata/TestFoo/stderr.golden
//
// Note: Subtest names like "TestFoo/case1" have "/" replaced with "_"
// to create valid file paths: testdata/TestFoo_case1/output.golden
func GoldenPath(t *testing.T, name string) string {
	t.Helper()

	if name == "" {
		name = "output"
	}

	// Use test name as subdirectory to avoid collisions
	// Replace / with _ for subtests (e.g., TestFoo/case1 → TestFoo_case1)
	testName := sanitizeTestName(t.Name())

	return filepath.Join("testdata", testName, name+".golden")
}

// CompareGolden compares the actual output against a golden file.
//
// If the golden file doesn't exist:
//   - In update mode (GOLDEN_UPDATE=1): Creates the file with actual content
//   - Otherwise: Fails the test with a helpful message
//
// If the golden file exists:
//   - In update mode (GOLDEN_UPDATE=1): Updates the file if content differs
//   - Otherwise: Compares content and fails if different
//
// Update mode is enabled by setting the GOLDEN_UPDATE environment variable to "1":
//
//	GOLDEN_UPDATE=1 go test ./... -run TestFoo
//
// Usage:
//
//	func TestOutputFormat(t *testing.T) {
//	    got := runCommand(t, "mycommand", "--flag")
//	    testutil.CompareGolden(t, "output", got)
//	}
//
// Parameters:
//   - t: The test instance
//   - name: The golden file name (without .golden extension). Use "" for default "output"
//   - got: The actual output bytes to compare
func CompareGolden(t *testing.T, name string, got []byte) {
	t.Helper()

	result, err := compareGoldenE(t, name, got)
	if err != nil {
		t.Fatal(err)
	}

	switch result.status {
	case goldenCreated:
		t.Logf("Created golden file: %s", result.path)
	case goldenUpdated:
		t.Logf("Updated golden file: %s", result.path)
	case goldenMismatch:
		t.Errorf("Output does not match golden file: %s\n\nTo update, run:\n  GOLDEN_UPDATE=1 go test ./... -run %s\n\nGot:\n%s\n\nWant:\n%s",
			result.path, t.Name(), string(got), string(result.want))
	}
}

// CompareGoldenString is a convenience wrapper around CompareGolden for string values.
func CompareGoldenString(t *testing.T, name string, got string) {
	t.Helper()
	CompareGolden(t, name, []byte(got))
}

type goldenStatus int

const (
	goldenMatch goldenStatus = iota
	goldenCreated
	goldenUpdated
	goldenMismatch
	goldenMissing
)

type goldenResult struct {
	status goldenStatus
	path   string
	want   []byte
}

// compareGoldenE is the error-returning implementation for testing.
// Returns an error for file system failures, and a result indicating the comparison outcome.
func compareGoldenE(t *testing.T, name string, got []byte) (goldenResult, error) {
	t.Helper()

	path := GoldenPath(t, name)
	updateMode := os.Getenv("GOLDEN_UPDATE") == "1"

	// Read existing golden file
	want, err := os.ReadFile(path)

	if os.IsNotExist(err) {
		if updateMode {
			if err := writeGoldenFile(path, got); err != nil {
				return goldenResult{}, err
			}
			return goldenResult{status: goldenCreated, path: path}, nil
		}
		return goldenResult{status: goldenMissing, path: path}, fmt.Errorf("golden file not found: %s\n\nTo create it, run:\n  GOLDEN_UPDATE=1 go test ./... -run %s", path, t.Name())
	}

	if err != nil {
		return goldenResult{}, fmt.Errorf("failed to read golden file %s: %w", path, err)
	}

	if string(got) != string(want) {
		if updateMode {
			if err := writeGoldenFile(path, got); err != nil {
				return goldenResult{}, err
			}
			return goldenResult{status: goldenUpdated, path: path}, nil
		}
		return goldenResult{status: goldenMismatch, path: path, want: want}, nil
	}

	return goldenResult{status: goldenMatch, path: path}, nil
}

// writeGoldenFile creates or updates a golden file with the given content.
// Creates parent directories as needed.
func writeGoldenFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create golden file directory %s: %w", dir, err)
	}

	if err := os.WriteFile(path, content, 0644); err != nil {
		return fmt.Errorf("failed to write golden file %s: %w", path, err)
	}
	return nil
}

// sanitizeTestName converts a test name into a valid directory name.
// Replaces problematic characters (/, \, :, etc.) with underscores.
func sanitizeTestName(name string) string {
	// Replace characters that are problematic in file paths
	result := make([]byte, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch c {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			result[i] = '_'
		default:
			result[i] = c
		}
	}
	return string(result)
}

// GoldenAssert is a variant that uses require.NoError and require.Equal
// for assertion-style test failures. Use this when you want the test
// to stop immediately on mismatch without update mode support.
func GoldenAssert(t *testing.T, name string, got []byte) {
	t.Helper()

	path := GoldenPath(t, name)
	want, err := os.ReadFile(path)
	require.NoError(t, err, "Failed to read golden file: %s", path)
	require.Equal(t, string(want), string(got), "Output does not match golden file: %s", path)
}

// ErrGoldenMissing is returned when a golden file doesn't exist and update mode is disabled.
var ErrGoldenMissing = errors.New("golden file not found")

// ErrGoldenMismatch is returned when content doesn't match and update mode is disabled.
var ErrGoldenMismatch = errors.New("golden file content mismatch")
