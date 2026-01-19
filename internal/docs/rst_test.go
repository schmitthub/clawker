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

func TestGenReST(t *testing.T) {
	rootCmd := newTestRootCmd()
	containerCmd, _, _ := rootCmd.Find([]string{"container"})
	require.NotNil(t, containerCmd)

	buf := new(bytes.Buffer)
	err := GenReST(containerCmd, buf)
	require.NoError(t, err)

	output := buf.String()

	// Check title with underline
	checkStringContains(t, output, "clawker container")
	checkStringContains(t, output, "=================")

	// Check short description
	checkStringContains(t, output, "Manage containers")

	// Check long description in synopsis
	checkStringContains(t, output, "Synopsis")
	checkStringContains(t, output, "Manage clawker containers including create")

	// Check aliases are documented
	checkStringContains(t, output, "Aliases")
	checkStringContains(t, output, "``container``")
	checkStringContains(t, output, "``c``")

	// Check subcommands are listed with RST link syntax
	checkStringContains(t, output, "Subcommands")
	checkStringContains(t, output, "`clawker container list")
	checkStringContains(t, output, "`clawker container start")
	checkStringContains(t, output, "`clawker container stop")

	// Check see also points to parent with RST link syntax
	checkStringContains(t, output, "See Also")
	checkStringContains(t, output, "`clawker")
}

func TestGenReST_WithFlags(t *testing.T) {
	rootCmd := newTestRootCmd()
	listCmd, _, _ := rootCmd.Find([]string{"container", "list"})
	require.NotNil(t, listCmd)

	buf := new(bytes.Buffer)
	err := GenReST(listCmd, buf)
	require.NoError(t, err)

	output := buf.String()

	// Check options section exists
	checkStringContains(t, output, "Options")
	checkStringContains(t, output, "-------")

	// Check flags are documented with RST syntax
	checkStringContains(t, output, "``--all``")
	checkStringContains(t, output, "``-a``")
	checkStringContains(t, output, "Show all containers")
	checkStringContains(t, output, "``--quiet``")
	checkStringContains(t, output, "``-q``")

	// Check inherited options from parent
	checkStringContains(t, output, "Options inherited from parent commands")
	checkStringContains(t, output, "``--debug``")
	checkStringContains(t, output, "``--config``")
}

func TestGenReST_WithExamples(t *testing.T) {
	rootCmd := newTestRootCmd()
	listCmd, _, _ := rootCmd.Find([]string{"container", "list"})
	require.NotNil(t, listCmd)

	buf := new(bytes.Buffer)
	err := GenReST(listCmd, buf)
	require.NoError(t, err)

	output := buf.String()

	// Check examples section with RST code block syntax
	checkStringContains(t, output, "Examples")
	checkStringContains(t, output, "::")
	checkStringContains(t, output, "clawker container list")
	checkStringContains(t, output, "clawker container list --all")
}

func TestGenReST_HiddenCommandsOmitted(t *testing.T) {
	rootCmd := newTestRootCmd()

	buf := new(bytes.Buffer)
	err := GenReST(rootCmd, buf)
	require.NoError(t, err)

	output := buf.String()

	// Hidden command should not appear
	checkStringOmits(t, output, "hidden")
}

func TestGenReSTTree(t *testing.T) {
	rootCmd := newTestRootCmd()
	dir := t.TempDir()

	err := GenReSTTree(rootCmd, dir)
	require.NoError(t, err)

	// Verify root file exists
	_, err = os.Stat(filepath.Join(dir, "clawker.rst"))
	require.NoError(t, err)

	// Verify container command file exists
	_, err = os.Stat(filepath.Join(dir, "clawker_container.rst"))
	require.NoError(t, err)

	// Verify container subcommand files exist
	_, err = os.Stat(filepath.Join(dir, "clawker_container_list.rst"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "clawker_container_start.rst"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "clawker_container_stop.rst"))
	require.NoError(t, err)

	// Verify volume command files exist
	_, err = os.Stat(filepath.Join(dir, "clawker_volume.rst"))
	require.NoError(t, err)

	// Verify hidden command was NOT generated
	_, err = os.Stat(filepath.Join(dir, "clawker_hidden.rst"))
	assert.True(t, os.IsNotExist(err), "hidden command should not generate RST docs")
}

func TestGenReSTTreeCustom(t *testing.T) {
	rootCmd := newTestRootCmd()
	dir := t.TempDir()

	// Custom prepender that adds RST directive
	prepender := func(filename string) string {
		return ".. meta::\n   :description: Clawker CLI Documentation\n\n"
	}

	// Custom link handler that uses absolute paths
	linkHandler := func(cmdPath string) string {
		return "/docs/" + rstFilename(&cobra.Command{Use: cmdPath})
	}

	err := GenReSTTreeCustom(rootCmd, dir, prepender, linkHandler)
	require.NoError(t, err)

	// Read generated file and verify prepender was applied
	content, err := os.ReadFile(filepath.Join(dir, "clawker.rst"))
	require.NoError(t, err)

	checkStringContains(t, string(content), ".. meta::")
	checkStringContains(t, string(content), ":description: Clawker CLI Documentation")
}

func TestRstTitle(t *testing.T) {
	tests := []struct {
		text      string
		underline rune
		expected  string
	}{
		{
			text:      "Hello",
			underline: '=',
			expected:  "Hello\n=====\n\n",
		},
		{
			text:      "Section",
			underline: '-',
			expected:  "Section\n-------\n\n",
		},
		{
			text:      "A",
			underline: '~',
			expected:  "A\n~\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			result := rstTitle(tt.text, tt.underline)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRstFilename(t *testing.T) {
	t.Run("root command", func(t *testing.T) {
		cmd := &cobra.Command{Use: "clawker"}
		assert.Equal(t, "clawker.rst", rstFilename(cmd))
	})

	t.Run("subcommand", func(t *testing.T) {
		root := &cobra.Command{Use: "clawker"}
		container := &cobra.Command{Use: "container"}
		root.AddCommand(container)
		assert.Equal(t, "clawker_container.rst", rstFilename(container))
	})

	t.Run("nested subcommand", func(t *testing.T) {
		root := &cobra.Command{Use: "clawker"}
		container := &cobra.Command{Use: "container"}
		list := &cobra.Command{Use: "list"}
		root.AddCommand(container)
		container.AddCommand(list)
		assert.Equal(t, "clawker_container_list.rst", rstFilename(list))
	})
}

func TestDefaultRSTLinkHandler(t *testing.T) {
	tests := []struct {
		cmdPath  string
		expected string
	}{
		{
			cmdPath:  "clawker",
			expected: "clawker.html",
		},
		{
			cmdPath:  "clawker container",
			expected: "clawker_container.html",
		},
		{
			cmdPath:  "clawker container list",
			expected: "clawker_container_list.html",
		},
	}

	for _, tt := range tests {
		t.Run(tt.cmdPath, func(t *testing.T) {
			result := defaultRSTLinkHandler(tt.cmdPath)
			assert.Equal(t, tt.expected, result)
		})
	}
}
