package cmdutil

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"text/template"
	"unicode/utf8"
)

// DefaultFuncMap returns the default template function map for --format templates.
// These functions are Docker CLI-compatible and available in all Go templates
// used by clawker commands.
func DefaultFuncMap() template.FuncMap {
	return template.FuncMap{
		"json": func(v any) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"title": func(s string) string {
			if s == "" {
				return ""
			}
			r, size := utf8.DecodeRuneInString(s)
			return strings.ToUpper(string(r)) + s[size:]
		},
		"split": strings.Split,
		"join":  strings.Join,
		"truncate": func(s string, n int) string {
			if n < 0 {
				n = 0
			}
			if len(s) <= n {
				return s
			}
			if n <= 3 {
				return s[:n]
			}
			return s[:n-3] + "..."
		},
	}
}

// ExecuteTemplate parses and executes a Go template from the given Format for
// each item, writing results to w. For table-template formats, output is
// aligned through a tabwriter. Each item produces one line of output.
func ExecuteTemplate(w io.Writer, f Format, items []any) error {
	tmpl, err := template.New("").Funcs(DefaultFuncMap()).Parse(f.Template())
	if err != nil {
		return fmt.Errorf("invalid template: %w", err)
	}

	var dest io.Writer = w
	var tw *tabwriter.Writer

	if f.IsTableTemplate() {
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		dest = tw
	}

	for _, item := range items {
		if err := tmpl.Execute(dest, item); err != nil {
			return fmt.Errorf("template execution failed: %w", err)
		}
		if _, err := fmt.Fprintln(dest); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}
	}

	if tw != nil {
		return tw.Flush()
	}

	return nil
}
