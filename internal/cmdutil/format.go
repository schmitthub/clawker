package cmdutil

import (
	"strings"

	"github.com/spf13/cobra"
)

// Format mode constants for --format flag parsing.
const (
	ModeDefault       = ""
	ModeTable         = "table"
	ModeJSON          = "json"
	ModeTemplate      = "template"
	ModeTableTemplate = "table-template"
)

// Format is a parsed format specification from the --format flag.
type Format struct {
	mode     string
	template string
}

// ParseFormat parses a raw --format flag value into a Format.
//
// Recognized inputs:
//   - ""                              → ModeDefault
//   - "table"                         → ModeTable
//   - "json"                          → ModeJSON
//   - "table {{.Name}}\t{{.ID}}"     → ModeTableTemplate (prefix "table ")
//   - "{{.Name}} {{.ID}}"            → ModeTemplate (contains "{{")
//   - anything else                   → FlagError
func ParseFormat(raw string) (Format, error) {
	switch {
	case raw == "":
		return Format{mode: ModeDefault}, nil
	case raw == "table":
		return Format{mode: ModeTable}, nil
	case raw == "json":
		return Format{mode: ModeJSON}, nil
	case strings.HasPrefix(raw, "table "):
		tmpl := strings.TrimPrefix(raw, "table ")
		return Format{mode: ModeTableTemplate, template: tmpl}, nil
	case strings.Contains(raw, "{{"):
		return Format{mode: ModeTemplate, template: raw}, nil
	default:
		return Format{}, FlagErrorf("invalid format string: %q", raw)
	}
}

// IsDefault reports whether the format is the default table output.
func (f Format) IsDefault() bool {
	return f.mode == ModeDefault || f.mode == ModeTable
}

// IsJSON reports whether the format is JSON output.
func (f Format) IsJSON() bool {
	return f.mode == ModeJSON
}

// IsTemplate reports whether the format uses a Go template (plain or table).
func (f Format) IsTemplate() bool {
	return f.mode == ModeTemplate || f.mode == ModeTableTemplate
}

// IsTableTemplate reports whether the format is a table with a Go template.
func (f Format) IsTableTemplate() bool {
	return f.mode == ModeTableTemplate
}

// Template returns the Go template string, or "" if not a template format.
func (f Format) Template() string {
	return f.template
}

// FormatFlags holds parsed state for --format, --json, and --quiet flags.
type FormatFlags struct {
	Format Format
	Quiet  bool
}

// IsJSON reports whether the format is JSON output.
func (ff *FormatFlags) IsJSON() bool { return ff.Format.IsJSON() }

// IsTemplate reports whether the format uses a Go template.
func (ff *FormatFlags) IsTemplate() bool { return ff.Format.IsTemplate() }

// IsDefault reports whether the format is the default table output.
func (ff *FormatFlags) IsDefault() bool { return ff.Format.IsDefault() }

// IsTableTemplate reports whether the format is a table with a Go template.
func (ff *FormatFlags) IsTableTemplate() bool { return ff.Format.IsTableTemplate() }

// Template returns the underlying Format value (for passing to ExecuteTemplate).
func (ff *FormatFlags) Template() Format { return ff.Format }

// AddFormatFlags registers --format, --json, and -q/--quiet flags on the
// command and chains PreRunE validation for mutual exclusivity.
//
// The returned FormatFlags is populated during PreRunE; commands read it
// in RunE after flag parsing is complete.
func AddFormatFlags(cmd *cobra.Command) *FormatFlags {
	ff := &FormatFlags{}

	cmd.Flags().String("format", "", `Output format: "json", "table", or a Go template`)
	cmd.Flags().Bool("json", false, "Output as JSON (shorthand for --format json)")
	cmd.Flags().BoolP("quiet", "q", false, "Only display IDs")

	existingPreRunE := cmd.PreRunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		// Preserve existing PreRunE chain.
		if existingPreRunE != nil {
			if err := existingPreRunE(cmd, args); err != nil {
				return err
			}
		}

		formatChanged := cmd.Flags().Changed("format")
		jsonChanged := cmd.Flags().Changed("json")

		jsonFlag, _ := cmd.Flags().GetBool("json")
		quietFlag, _ := cmd.Flags().GetBool("quiet")
		formatRaw, _ := cmd.Flags().GetString("format")

		// Mutual exclusivity checks.
		if jsonChanged && formatChanged {
			return FlagErrorf("--format and --json are mutually exclusive")
		}
		if quietFlag && (formatChanged || jsonChanged) {
			return FlagErrorf("--quiet and --format/--json are mutually exclusive")
		}

		ff.Quiet = quietFlag

		// Resolve format.
		if jsonFlag {
			ff.Format = Format{mode: ModeJSON}
			return nil
		}

		parsed, err := ParseFormat(formatRaw)
		if err != nil {
			return err
		}
		ff.Format = parsed
		return nil
	}

	return ff
}

// ToAny converts a typed slice to []any for use with ExecuteTemplate.
func ToAny[T any](items []T) []any {
	result := make([]any, len(items))
	for i, v := range items {
		result[i] = v
	}
	return result
}
