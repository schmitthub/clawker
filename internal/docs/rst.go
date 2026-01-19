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
	"github.com/spf13/pflag"
)

// GenReSTTree generates reStructuredText documentation for a command and all its subcommands.
// Files are created in the specified directory using the command path as filename.
func GenReSTTree(cmd *cobra.Command, dir string) error {
	return GenReSTTreeCustom(cmd, dir, defaultFilePrepender, defaultRSTLinkHandler)
}

// GenReSTTreeCustom generates reStructuredText documentation with custom file prepender and link handler.
// The filePrepender is called with each filename to prepend content (e.g., directives).
// The linkHandler transforms command names to RST links.
func GenReSTTreeCustom(cmd *cobra.Command, dir string, filePrepender, linkHandler func(string) string) error {
	for _, c := range cmd.Commands() {
		if c.Hidden {
			continue
		}
		if err := GenReSTTreeCustom(c, dir, filePrepender, linkHandler); err != nil {
			return err
		}
	}

	filename := filepath.Join(dir, rstFilename(cmd))
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

	return GenReSTCustom(cmd, f, linkHandler)
}

// GenReST generates reStructuredText documentation for a single command.
func GenReST(cmd *cobra.Command, w io.Writer) error {
	return GenReSTCustom(cmd, w, defaultRSTLinkHandler)
}

// GenReSTCustom generates reStructuredText documentation with a custom link handler.
func GenReSTCustom(cmd *cobra.Command, w io.Writer, linkHandler func(string) string) error {
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultHelpFlag()

	buf := new(bytes.Buffer)
	name := cmd.CommandPath()

	// Title with underline
	buf.WriteString(rstTitle(name, '='))
	buf.WriteString("\n")

	// Short description
	if cmd.Short != "" {
		buf.WriteString(cmd.Short + "\n\n")
	}

	// Synopsis
	if cmd.Runnable() || hasRunnableSubCommands(cmd) {
		buf.WriteString(rstTitle("Synopsis", '-'))
		if cmd.Long != "" {
			buf.WriteString(cmd.Long + "\n\n")
		}
		if cmd.Runnable() {
			buf.WriteString("::\n\n")
			buf.WriteString("    " + cmd.UseLine() + "\n\n")
		}
	}

	// Aliases
	if len(cmd.Aliases) > 0 {
		buf.WriteString(rstTitle("Aliases", '-'))
		buf.WriteString("``" + cmd.Name() + "``, ")
		aliases := make([]string, len(cmd.Aliases))
		for i, a := range cmd.Aliases {
			aliases[i] = "``" + a + "``"
		}
		buf.WriteString(strings.Join(aliases, ", ") + "\n\n")
	}

	// Examples
	if cmd.Example != "" {
		buf.WriteString(rstTitle("Examples", '-'))
		buf.WriteString("::\n\n")
		// Indent each line for RST code block
		for line := range strings.SplitSeq(cmd.Example, "\n") {
			buf.WriteString("    " + line + "\n")
		}
		buf.WriteString("\n")
	}

	// Subcommands
	if subcommands := getNonHiddenCommands(cmd); len(subcommands) > 0 {
		buf.WriteString(rstTitle("Subcommands", '-'))
		for _, c := range subcommands {
			link := linkHandler(c.CommandPath())
			fmt.Fprintf(buf, "* `%s <%s>`_ - %s\n", c.CommandPath(), link, c.Short)
		}
		buf.WriteString("\n")
	}

	// Options
	if flags := cmd.NonInheritedFlags(); flags.HasAvailableFlags() {
		buf.WriteString(rstTitle("Options", '-'))
		rstPrintFlags(buf, flags)
		buf.WriteString("\n")
	}

	// Inherited options
	if flags := cmd.InheritedFlags(); flags.HasAvailableFlags() {
		buf.WriteString(rstTitle("Options inherited from parent commands", '-'))
		rstPrintFlags(buf, flags)
		buf.WriteString("\n")
	}

	// See also (parent and siblings)
	if cmd.HasParent() {
		buf.WriteString(rstTitle("See Also", '-'))
		parent := cmd.Parent()
		link := linkHandler(parent.CommandPath())
		fmt.Fprintf(buf, "* `%s <%s>`_ - %s\n", parent.CommandPath(), link, parent.Short)
		buf.WriteString("\n")
	}

	_, err := buf.WriteTo(w)
	return err
}

// rstTitle creates an RST title with underline character
func rstTitle(text string, underline rune) string {
	return text + "\n" + strings.Repeat(string(underline), len(text)) + "\n\n"
}

// rstPrintFlags formats flags as RST definition list
func rstPrintFlags(buf *bytes.Buffer, fs *pflag.FlagSet) {
	// Collect flags for sorting
	type flagInfo struct {
		name      string
		shorthand string
		defValue  string
		usage     string
		flagType  string
	}
	var flagList []flagInfo

	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		flagList = append(flagList, flagInfo{
			name:      f.Name,
			shorthand: f.Shorthand,
			defValue:  f.DefValue,
			usage:     f.Usage,
			flagType:  f.Value.Type(),
		})
	})

	sort.Slice(flagList, func(i, j int) bool {
		return flagList[i].name < flagList[j].name
	})

	for _, f := range flagList {
		// Build flag string
		var format string
		if f.shorthand != "" {
			format = fmt.Sprintf("``-%s``, ``--%s``", f.shorthand, f.name)
		} else {
			format = fmt.Sprintf("``--%s``", f.name)
		}

		// Add type hint for non-bool flags
		if f.flagType != "bool" {
			format += fmt.Sprintf(" <%s>", f.flagType)
		}

		buf.WriteString(format + "\n")
		buf.WriteString("    " + f.usage)
		if f.defValue != "" && f.defValue != "false" && f.defValue != "0" && f.defValue != "[]" {
			fmt.Fprintf(buf, " (default: ``%s``)", f.defValue)
		}
		buf.WriteString("\n\n")
	}
}

func rstFilename(cmd *cobra.Command) string {
	return strings.ReplaceAll(cmd.CommandPath(), " ", "_") + ".rst"
}

func defaultRSTLinkHandler(cmdPath string) string {
	return strings.ReplaceAll(cmdPath, " ", "_") + ".html"
}
