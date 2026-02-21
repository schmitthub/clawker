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

// --- Website (MDX-safe) generation tests ---

func TestEscapeMDXProse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no angle brackets",
			input: "Simple text without placeholders",
			want:  "Simple text without placeholders",
		},
		{
			name:  "single placeholder",
			input: "Container name is clawker.<project>.<agent>",
			want:  "Container name is clawker.`<project>`.`<agent>`",
		},
		{
			name:  "multiple placeholders",
			input: "Resolves <project> and <agent> from context",
			want:  "Resolves `<project>` and `<agent>` from context",
		},
		{
			name:  "hyphenated placeholder",
			input: "Use <my-value> as the argument",
			want:  "Use `<my-value>` as the argument",
		},
		{
			name:  "html-like tag is escaped",
			input: "Output is <div> formatted",
			want:  "Output is `<div>` formatted",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "path with angle brackets",
			input: "~/.local/share/clawker/worktrees/<hash>/",
			want:  "~/.local/share/clawker/worktrees/`<hash>`/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EscapeMDXProse(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGenMarkdownWebsite(t *testing.T) {
	// Create a command with angle brackets in descriptions
	root := &cobra.Command{
		Use:   "clawker",
		Short: "Claude Code in Docker containers",
	}
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run a container for <project>.<agent>",
		Long:  "When --agent is provided, the container is named clawker.<project>.<agent>",
		RunE:  func(cmd *cobra.Command, args []string) error { return nil },
		Example: `  clawker run -it --agent dev @
  clawker run --detach --agent test @`,
	}
	root.AddCommand(runCmd)

	buf := new(bytes.Buffer)
	err := GenMarkdownWebsite(runCmd, buf, defaultLinkHandler)
	require.NoError(t, err)

	output := buf.String()

	// Short description should have escaped angle brackets
	checkStringContains(t, output, "Run a container for `<project>`.`<agent>`")

	// Long description should have escaped angle brackets
	checkStringContains(t, output, "clawker.`<project>`.`<agent>`")

	// Examples in code block should NOT be escaped (they're inside ```)
	checkStringContains(t, output, "clawker run -it --agent dev @")
}

func TestGenMarkdownTreeWebsite(t *testing.T) {
	root := &cobra.Command{
		Use:   "clawker",
		Short: "Claude Code in Docker containers",
	}
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run a container for <project>.<agent>",
		Long:  "When --agent is provided, the container is named clawker.<project>.<agent>",
		RunE:  func(cmd *cobra.Command, args []string) error { return nil },
	}
	root.AddCommand(runCmd)

	dir := t.TempDir()
	prepender := func(filename string) string {
		return "---\ntitle: test\n---\n\n"
	}

	err := GenMarkdownTreeWebsite(root, dir, prepender, defaultLinkHandler)
	require.NoError(t, err)

	// Read the run command file and verify escaping
	content, err := os.ReadFile(filepath.Join(dir, "clawker_run.md"))
	require.NoError(t, err)

	contentStr := string(content)
	checkStringContains(t, contentStr, "---\ntitle: test\n---")
	checkStringContains(t, contentStr, "`<project>`")
	checkStringContains(t, contentStr, "`<agent>`")
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
