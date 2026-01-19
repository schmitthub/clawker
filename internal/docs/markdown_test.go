package docs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenMarkdown(t *testing.T) {
	rootCmd := newTestRootCmd()
	containerCmd, _, _ := rootCmd.Find([]string{"container"})
	require.NotNil(t, containerCmd)

	buf := new(bytes.Buffer)
	err := GenMarkdown(containerCmd, buf)
	require.NoError(t, err)

	output := buf.String()

	// Check title
	checkStringContains(t, output, "## clawker container")

	// Check short description
	checkStringContains(t, output, "Manage containers")

	// Check long description in synopsis
	checkStringContains(t, output, "Manage clawker containers including create")

	// Check aliases are documented
	checkStringContains(t, output, "### Aliases")
	checkStringContains(t, output, "`container`")
	checkStringContains(t, output, "`c`")

	// Check subcommands are listed
	checkStringContains(t, output, "### Subcommands")
	checkStringContains(t, output, "clawker container list")
	checkStringContains(t, output, "clawker container start")
	checkStringContains(t, output, "clawker container stop")

	// Check see also points to parent
	checkStringContains(t, output, "### See also")
	checkStringContains(t, output, "clawker")
}

func TestGenMarkdown_WithFlags(t *testing.T) {
	rootCmd := newTestRootCmd()
	listCmd, _, _ := rootCmd.Find([]string{"container", "list"})
	require.NotNil(t, listCmd)

	buf := new(bytes.Buffer)
	err := GenMarkdown(listCmd, buf)
	require.NoError(t, err)

	output := buf.String()

	// Check options section exists
	checkStringContains(t, output, "### Options")

	// Check flags are documented
	checkStringContains(t, output, "--all")
	checkStringContains(t, output, "-a")
	checkStringContains(t, output, "Show all containers")
	checkStringContains(t, output, "--quiet")
	checkStringContains(t, output, "-q")

	// Check inherited options from parent
	checkStringContains(t, output, "### Options inherited from parent commands")
	checkStringContains(t, output, "--debug")
	checkStringContains(t, output, "--config")
}

func TestGenMarkdown_WithExamples(t *testing.T) {
	rootCmd := newTestRootCmd()
	listCmd, _, _ := rootCmd.Find([]string{"container", "list"})
	require.NotNil(t, listCmd)

	buf := new(bytes.Buffer)
	err := GenMarkdown(listCmd, buf)
	require.NoError(t, err)

	output := buf.String()

	// Check examples section
	checkStringContains(t, output, "### Examples")
	checkStringContains(t, output, "clawker container list")
	checkStringContains(t, output, "clawker container list --all")
}

func TestGenMarkdown_HiddenCommandsOmitted(t *testing.T) {
	rootCmd := newTestRootCmd()

	buf := new(bytes.Buffer)
	err := GenMarkdown(rootCmd, buf)
	require.NoError(t, err)

	output := buf.String()

	// Hidden command should not appear
	checkStringOmits(t, output, "hidden")
}

func TestGenMarkdownTree(t *testing.T) {
	rootCmd := newTestRootCmd()
	dir := t.TempDir()

	err := GenMarkdownTree(rootCmd, dir)
	require.NoError(t, err)

	// Verify root file exists
	_, err = os.Stat(filepath.Join(dir, "clawker.md"))
	require.NoError(t, err)

	// Verify container command file exists
	_, err = os.Stat(filepath.Join(dir, "clawker_container.md"))
	require.NoError(t, err)

	// Verify container subcommand files exist
	_, err = os.Stat(filepath.Join(dir, "clawker_container_list.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "clawker_container_start.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "clawker_container_stop.md"))
	require.NoError(t, err)

	// Verify volume command files exist
	_, err = os.Stat(filepath.Join(dir, "clawker_volume.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "clawker_volume_list.md"))
	require.NoError(t, err)

	// Verify hidden command was NOT generated
	_, err = os.Stat(filepath.Join(dir, "clawker_hidden.md"))
	assert.True(t, os.IsNotExist(err), "hidden command should not generate docs")
}

func TestGenMarkdownTreeCustom(t *testing.T) {
	rootCmd := newTestRootCmd()
	dir := t.TempDir()

	// Custom prepender that adds YAML front matter
	prepender := func(filename string) string {
		return "---\nlayout: docs\n---\n\n"
	}

	// Custom link handler that uses absolute paths
	linkHandler := func(cmdPath string) string {
		return "/docs/" + cmdManualPath(&cobra.Command{Use: cmdPath})
	}

	err := GenMarkdownTreeCustom(rootCmd, dir, prepender, linkHandler)
	require.NoError(t, err)

	// Read generated file and verify prepender was applied
	content, err := os.ReadFile(filepath.Join(dir, "clawker.md"))
	require.NoError(t, err)

	checkStringContains(t, string(content), "---\nlayout: docs\n---")
}

func TestCmdManualPath(t *testing.T) {
	t.Run("root command", func(t *testing.T) {
		cmd := &cobra.Command{Use: "clawker"}
		assert.Equal(t, "clawker.md", cmdManualPath(cmd))
	})

	t.Run("subcommand", func(t *testing.T) {
		root := &cobra.Command{Use: "clawker"}
		child := &cobra.Command{Use: "container"}
		root.AddCommand(child)
		assert.Equal(t, "clawker_container.md", cmdManualPath(child))
	})

	t.Run("nested subcommand", func(t *testing.T) {
		root := &cobra.Command{Use: "clawker"}
		container := &cobra.Command{Use: "container"}
		list := &cobra.Command{Use: "list"}
		root.AddCommand(container)
		container.AddCommand(list)
		assert.Equal(t, "clawker_container_list.md", cmdManualPath(list))
	})
}
