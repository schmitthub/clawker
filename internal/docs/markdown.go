// Package docs provides documentation generation for Cobra commands
// in multiple formats including Markdown, man pages, YAML, and reStructuredText.
package docs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// GenMarkdownTree generates markdown documentation for a command and all its subcommands.
// Files are created in the specified directory using the command path as filename.
func GenMarkdownTree(cmd *cobra.Command, dir string) error {
	return GenMarkdownTreeCustom(cmd, dir, defaultFilePrepender, defaultLinkHandler)
}

// GenMarkdownTreeCustom generates markdown documentation with custom file prepender and link handler.
// The filePrepender is called with each filename to prepend content (e.g., front matter).
// The linkHandler transforms command names to links (e.g., adding .md extension).
func GenMarkdownTreeCustom(cmd *cobra.Command, dir string, filePrepender, linkHandler func(string) string) error {
	for _, c := range cmd.Commands() {
		if c.Hidden {
			continue
		}
		if err := GenMarkdownTreeCustom(c, dir, filePrepender, linkHandler); err != nil {
			return err
		}
	}

	filename := filepath.Join(dir, cmdManualPath(cmd))
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer f.Close()

	if prepend := filePrepender(filename); prepend != "" {
		if _, err := io.WriteString(f, prepend); err != nil {
			return fmt.Errorf("failed to write prepender to %s: %w", filename, err)
		}
	}

	return GenMarkdownCustom(cmd, f, linkHandler)
}

// GenMarkdown generates markdown documentation for a single command.
func GenMarkdown(cmd *cobra.Command, w io.Writer) error {
	return GenMarkdownCustom(cmd, w, defaultLinkHandler)
}

// GenMarkdownCustom generates markdown documentation with a custom link handler.
func GenMarkdownCustom(cmd *cobra.Command, w io.Writer, linkHandler func(string) string) error {
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultHelpFlag()

	buf := new(bytes.Buffer)
	name := cmd.CommandPath()

	// Title
	buf.WriteString("## " + name + "\n\n")

	// Short description
	if cmd.Short != "" {
		buf.WriteString(cmd.Short + "\n\n")
	}

	// Synopsis
	if cmd.Runnable() || hasRunnableSubCommands(cmd) {
		buf.WriteString("### Synopsis\n\n")
		if cmd.Long != "" {
			buf.WriteString(cmd.Long + "\n\n")
		}
		if cmd.Runnable() {
			buf.WriteString("```\n" + cmd.UseLine() + "\n```\n\n")
		}
	}

	// Aliases
	if len(cmd.Aliases) > 0 {
		buf.WriteString("### Aliases\n\n")
		buf.WriteString("`" + cmd.Name() + "`, ")
		aliases := make([]string, len(cmd.Aliases))
		for i, a := range cmd.Aliases {
			aliases[i] = "`" + a + "`"
		}
		buf.WriteString(strings.Join(aliases, ", ") + "\n\n")
	}

	// Examples
	if cmd.Example != "" {
		buf.WriteString("### Examples\n\n")
		buf.WriteString("```\n" + cmd.Example + "\n```\n\n")
	}

	// Subcommands
	if subcommands := getNonHiddenCommands(cmd); len(subcommands) > 0 {
		buf.WriteString("### Subcommands\n\n")
		for _, c := range subcommands {
			link := linkHandler(c.CommandPath())
			fmt.Fprintf(buf, "* [%s](%s) - %s\n", c.CommandPath(), link, c.Short)
		}
		buf.WriteString("\n")
	}

	// Options
	if flags := cmd.NonInheritedFlags(); flags.HasAvailableFlags() {
		buf.WriteString("### Options\n\n")
		buf.WriteString("```\n")
		buf.WriteString(flags.FlagUsages())
		buf.WriteString("```\n\n")
	}

	// Inherited options
	if flags := cmd.InheritedFlags(); flags.HasAvailableFlags() {
		buf.WriteString("### Options inherited from parent commands\n\n")
		buf.WriteString("```\n")
		buf.WriteString(flags.FlagUsages())
		buf.WriteString("```\n\n")
	}

	// See also (parent and siblings)
	if cmd.HasParent() {
		buf.WriteString("### See also\n\n")
		parent := cmd.Parent()
		link := linkHandler(parent.CommandPath())
		fmt.Fprintf(buf, "* [%s](%s) - %s\n", parent.CommandPath(), link, parent.Short)
	}

	_, err := buf.WriteTo(w)
	return err
}

// cmdManualPath returns the filename for a command's manual page.
func cmdManualPath(cmd *cobra.Command) string {
	return strings.ReplaceAll(cmd.CommandPath(), " ", "_") + ".md"
}

// defaultFilePrepender returns empty string (no prepending).
func defaultFilePrepender(filename string) string {
	return ""
}

// defaultLinkHandler transforms a command path to a markdown link.
func defaultLinkHandler(cmdPath string) string {
	return strings.ReplaceAll(cmdPath, " ", "_") + ".md"
}

// hasRunnableSubCommands returns true if any non-hidden subcommand is runnable.
func hasRunnableSubCommands(cmd *cobra.Command) bool {
	for _, c := range cmd.Commands() {
		if !c.Hidden && (c.Runnable() || hasRunnableSubCommands(c)) {
			return true
		}
	}
	return false
}

// getNonHiddenCommands returns all non-hidden subcommands sorted by name.
func getNonHiddenCommands(cmd *cobra.Command) []*cobra.Command {
	var commands []*cobra.Command
	for _, c := range cmd.Commands() {
		if !c.Hidden && c.Name() != "help" {
			commands = append(commands, c)
		}
	}
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name() < commands[j].Name()
	})
	return commands
}
