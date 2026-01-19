package docs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenMan(t *testing.T) {
	rootCmd := newTestRootCmd()
	containerCmd, _, _ := rootCmd.Find([]string{"container"})
	require.NotNil(t, containerCmd)

	buf := new(bytes.Buffer)
	header := &GenManHeader{
		Title:   "CLAWKER-CONTAINER",
		Section: "1",
		Source:  "Clawker",
		Manual:  "Clawker Manual",
	}
	err := GenMan(containerCmd, header, buf)
	require.NoError(t, err)

	output := buf.String()

	// Man pages are in groff format after md2man processing
	// Check that the output contains expected groff directives
	checkStringContains(t, output, ".TH") // Title header
	checkStringContains(t, output, "NAME")
	checkStringContains(t, output, "container")
	checkStringContains(t, output, "SYNOPSIS")
	checkStringContains(t, output, "COMMANDS")
	checkStringContains(t, output, "SEE ALSO")
}

func TestGenMan_WithFlags(t *testing.T) {
	rootCmd := newTestRootCmd()
	listCmd, _, _ := rootCmd.Find([]string{"container", "list"})
	require.NotNil(t, listCmd)

	buf := new(bytes.Buffer)
	err := GenMan(listCmd, nil, buf)
	require.NoError(t, err)

	output := buf.String()

	// Check OPTIONS section exists in groff output
	checkStringContains(t, output, "OPTIONS")
	checkStringContains(t, output, "all")
	checkStringContains(t, output, "quiet")
}

func TestGenMan_WithExamples(t *testing.T) {
	rootCmd := newTestRootCmd()
	listCmd, _, _ := rootCmd.Find([]string{"container", "list"})
	require.NotNil(t, listCmd)

	buf := new(bytes.Buffer)
	err := GenMan(listCmd, nil, buf)
	require.NoError(t, err)

	output := buf.String()

	// Check EXAMPLES section
	checkStringContains(t, output, "EXAMPLES")
	checkStringContains(t, output, "container list")
}

func TestGenMan_WithDate(t *testing.T) {
	rootCmd := newTestRootCmd()

	date := time.Date(2025, time.January, 15, 0, 0, 0, 0, time.UTC)
	header := &GenManHeader{
		Title:   "CLAWKER",
		Section: "1",
		Date:    &date,
		Source:  "Clawker",
		Manual:  "Clawker Manual",
	}

	buf := new(bytes.Buffer)
	err := GenMan(rootCmd, header, buf)
	require.NoError(t, err)

	// Date should be in the output (Jan 2025 format)
	output := buf.String()
	checkStringContains(t, output, "2025")
}

func TestGenManTree(t *testing.T) {
	rootCmd := newTestRootCmd()
	dir := t.TempDir()

	err := GenManTree(rootCmd, dir)
	require.NoError(t, err)

	// Verify root file exists
	_, err = os.Stat(filepath.Join(dir, "clawker.1"))
	require.NoError(t, err)

	// Verify container command file exists
	_, err = os.Stat(filepath.Join(dir, "clawker-container.1"))
	require.NoError(t, err)

	// Verify container subcommand files exist
	_, err = os.Stat(filepath.Join(dir, "clawker-container-list.1"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "clawker-container-start.1"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "clawker-container-stop.1"))
	require.NoError(t, err)

	// Verify volume command files exist
	_, err = os.Stat(filepath.Join(dir, "clawker-volume.1"))
	require.NoError(t, err)

	// Verify hidden command was NOT generated
	_, err = os.Stat(filepath.Join(dir, "clawker-hidden.1"))
	assert.True(t, os.IsNotExist(err), "hidden command should not generate man pages")
}

func TestGenManTreeFromOpts(t *testing.T) {
	rootCmd := newTestRootCmd()
	dir := t.TempDir()

	date := time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
	opts := GenManTreeOptions{
		Path:             dir,
		CommandSeparator: "_",
		Header: &GenManHeader{
			Section: "8",
			Date:    &date,
			Source:  "CustomSource",
			Manual:  "Custom Manual",
		},
	}

	err := GenManTreeFromOpts(rootCmd, opts)
	require.NoError(t, err)

	// Verify files use custom separator and section
	_, err = os.Stat(filepath.Join(dir, "clawker.8"))
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "clawker_container.8"))
	require.NoError(t, err)

	// Read and verify custom values in content
	content, err := os.ReadFile(filepath.Join(dir, "clawker.8"))
	require.NoError(t, err)

	checkStringContains(t, string(content), "8")
}

func TestManFilename(t *testing.T) {
	t.Run("root command", func(t *testing.T) {
		cmd := &cobra.Command{Use: "clawker"}
		assert.Equal(t, "clawker.1", manFilename(cmd, "-", "1"))
	})

	t.Run("subcommand with dash separator", func(t *testing.T) {
		root := &cobra.Command{Use: "clawker"}
		container := &cobra.Command{Use: "container"}
		root.AddCommand(container)
		assert.Equal(t, "clawker-container.1", manFilename(container, "-", "1"))
	})

	t.Run("nested subcommand", func(t *testing.T) {
		root := &cobra.Command{Use: "clawker"}
		container := &cobra.Command{Use: "container"}
		list := &cobra.Command{Use: "list"}
		root.AddCommand(container)
		container.AddCommand(list)
		assert.Equal(t, "clawker-container-list.1", manFilename(list, "-", "1"))
	})

	t.Run("underscore separator", func(t *testing.T) {
		root := &cobra.Command{Use: "clawker"}
		container := &cobra.Command{Use: "container"}
		root.AddCommand(container)
		assert.Equal(t, "clawker_container.8", manFilename(container, "_", "8"))
	})
}
