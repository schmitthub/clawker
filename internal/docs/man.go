package docs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cpuguy83/go-md2man/v2/md2man"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// GenManHeader contains man page metadata
type GenManHeader struct {
	Title   string
	Section string
	Date    *time.Time
	Source  string
	Manual  string
}

// GenManTreeOptions configures man page generation
type GenManTreeOptions struct {
	Path             string
	CommandSeparator string
	Header           *GenManHeader
}

// GenManTree generates man pages for cmd and all subcommands.
// Man pages are written to the specified directory.
func GenManTree(cmd *cobra.Command, dir string) error {
	header := &GenManHeader{
		Section: "1",
		Source:  "Clawker",
		Manual:  "Clawker Manual",
	}
	return genManTreeFromOpts(cmd, GenManTreeOptions{
		Path:             dir,
		CommandSeparator: "-",
		Header:           header,
	})
}

// GenManTreeFromOpts generates man pages with custom options.
func GenManTreeFromOpts(cmd *cobra.Command, opts GenManTreeOptions) error {
	return genManTreeFromOpts(cmd, opts)
}

func genManTreeFromOpts(cmd *cobra.Command, opts GenManTreeOptions) error {
	for _, c := range cmd.Commands() {
		if c.Hidden {
			continue
		}
		if err := genManTreeFromOpts(c, opts); err != nil {
			return err
		}
	}

	section := "1"
	if opts.Header != nil && opts.Header.Section != "" {
		section = opts.Header.Section
	}

	separator := opts.CommandSeparator
	if separator == "" {
		separator = "-"
	}

	filename := filepath.Join(opts.Path, manFilename(cmd, separator, section))
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer f.Close()

	return GenMan(cmd, opts.Header, f)
}

// GenMan generates a man page for a single command.
func GenMan(cmd *cobra.Command, header *GenManHeader, w io.Writer) error {
	if header == nil {
		header = &GenManHeader{
			Section: "1",
		}
	}
	if header.Section == "" {
		header.Section = "1"
	}

	b := genMan(cmd, header)
	manPage := md2man.Render(b)
	_, err := w.Write(manPage)
	return err
}

func genMan(cmd *cobra.Command, header *GenManHeader) []byte {
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultHelpFlag()

	buf := new(bytes.Buffer)
	name := cmd.CommandPath()

	// TH header
	manPreamble(buf, header, name)

	// NAME section
	buf.WriteString("# NAME\n")
	short := cmd.Short
	if short == "" {
		short = "manual page for " + name
	}
	fmt.Fprintf(buf, "%s \\- %s\n\n", name, short)

	// SYNOPSIS section
	buf.WriteString("# SYNOPSIS\n")
	buf.WriteString("**" + name + "**")

	if flags := cmd.NonInheritedFlags(); flags.HasAvailableFlags() {
		buf.WriteString(" [OPTIONS]")
	}

	if cmd.HasAvailableSubCommands() {
		buf.WriteString(" COMMAND")
	}
	buf.WriteString("\n\n")

	// DESCRIPTION section
	if cmd.Long != "" {
		buf.WriteString("# DESCRIPTION\n")
		buf.WriteString(cmd.Long + "\n\n")
	}

	// SUBCOMMANDS section
	if subcommands := getNonHiddenCommands(cmd); len(subcommands) > 0 {
		buf.WriteString("# COMMANDS\n")
		for _, c := range subcommands {
			fmt.Fprintf(buf, "**%s**\n: %s\n\n", c.Name(), c.Short)
		}
	}

	// OPTIONS section
	manPrintOptions(buf, cmd)

	// EXAMPLES section
	if cmd.Example != "" {
		buf.WriteString("# EXAMPLES\n")
		buf.WriteString("```\n" + cmd.Example + "\n```\n\n")
	}

	// SEE ALSO section
	manPrintSeeAlso(buf, cmd, header.Section)

	return buf.Bytes()
}

func manPreamble(buf *bytes.Buffer, header *GenManHeader, name string) {
	// TH title section date source manual
	dateStr := ""
	if header.Date != nil {
		dateStr = header.Date.Format("Jan 2006")
	}

	title := header.Title
	if title == "" {
		title = strings.ToUpper(strings.ReplaceAll(name, " ", "-"))
	}

	fmt.Fprintf(buf, "%% %s(%s) %s | %s\n\n",
		title,
		header.Section,
		dateStr,
		header.Manual,
	)
}

func manPrintOptions(buf *bytes.Buffer, cmd *cobra.Command) {
	flags := cmd.NonInheritedFlags()
	parentFlags := cmd.InheritedFlags()

	if !flags.HasAvailableFlags() && !parentFlags.HasAvailableFlags() {
		return
	}

	buf.WriteString("# OPTIONS\n")

	if flags.HasAvailableFlags() {
		manPrintFlags(buf, flags)
	}

	if parentFlags.HasAvailableFlags() {
		manPrintFlags(buf, parentFlags)
	}
	buf.WriteString("\n")
}

func manPrintFlags(buf *bytes.Buffer, flags *pflag.FlagSet) {
	// Collect flags for sorting
	type flagInfo struct {
		name      string
		shorthand string
		defValue  string
		usage     string
		flagType  string
	}
	var flagList []flagInfo

	flags.VisitAll(func(f *pflag.Flag) {
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
		format := ""
		if f.shorthand != "" {
			format = fmt.Sprintf("**-%s**, **--%s**", f.shorthand, f.name)
		} else {
			format = fmt.Sprintf("**--%s**", f.name)
		}

		// Add type hint for non-bool flags
		if f.flagType != "bool" {
			format += fmt.Sprintf(" <%s>", f.flagType)
		}

		buf.WriteString(format + "\n")
		buf.WriteString(": " + f.usage)
		if f.defValue != "" && f.defValue != "false" && f.defValue != "0" && f.defValue != "[]" {
			fmt.Fprintf(buf, " (default: %s)", f.defValue)
		}
		buf.WriteString("\n\n")
	}
}

func manPrintSeeAlso(buf *bytes.Buffer, cmd *cobra.Command, section string) {
	buf.WriteString("# SEE ALSO\n")

	// Parent command
	if cmd.HasParent() {
		parent := cmd.Parent()
		parentName := strings.ReplaceAll(parent.CommandPath(), " ", "-")
		fmt.Fprintf(buf, "**%s(%s)**", parentName, section)

		// Sibling commands
		siblings := getNonHiddenCommands(parent)
		for _, s := range siblings {
			if s.Name() != cmd.Name() {
				siblingName := strings.ReplaceAll(s.CommandPath(), " ", "-")
				fmt.Fprintf(buf, ", **%s(%s)**", siblingName, section)
			}
		}
	}

	// Subcommands
	subcommands := getNonHiddenCommands(cmd)
	if len(subcommands) > 0 && cmd.HasParent() {
		buf.WriteString(", ")
	}
	for i, c := range subcommands {
		if i > 0 {
			buf.WriteString(", ")
		}
		subName := strings.ReplaceAll(c.CommandPath(), " ", "-")
		fmt.Fprintf(buf, "**%s(%s)**", subName, section)
	}

	buf.WriteString("\n")
}

func manFilename(cmd *cobra.Command, separator, section string) string {
	name := strings.ReplaceAll(cmd.CommandPath(), " ", separator)
	return name + "." + section
}
