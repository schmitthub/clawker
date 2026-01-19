// Package docs provides the doc-gen command for generating CLI documentation.
package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/docs"
	"github.com/schmitthub/clawker/pkg/cmd/root"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/spf13/cobra"
)

// DocsOptions contains the options for the docs command.
type DocsOptions struct {
	DocPath  string
	Markdown bool
	Man      bool
	YAML     bool
	RST      bool
	Website  bool
}

// NewCmdDocs creates a new docs command.
func NewCmdDocs(f *cmdutil.Factory) *cobra.Command {
	opts := &DocsOptions{}

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate CLI documentation in multiple formats",
		Long: `Generate documentation for all clawker CLI commands.

Supports multiple output formats:
  --markdown    Markdown files (.md)
  --man         Man pages (section 1)
  --yaml        YAML reference files (.yaml)
  --rst         reStructuredText files (.rst)

At least one format flag must be specified.

The --website flag can be used with --markdown to add Jekyll front matter
for static site generation.`,
		Example: `  # Generate Markdown documentation
  doc-gen --doc-path ./docs --markdown

  # Generate man pages
  doc-gen --doc-path ./docs --man

  # Generate Markdown with Jekyll front matter for website
  doc-gen --doc-path ./docs --markdown --website

  # Generate multiple formats
  doc-gen --doc-path ./docs --markdown --man --yaml --rst`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocs(f, opts)
		},
	}

	cmd.Flags().StringVar(&opts.DocPath, "doc-path", "", "Output directory for generated docs (required)")
	cmd.Flags().BoolVar(&opts.Markdown, "markdown", false, "Generate Markdown documentation")
	cmd.Flags().BoolVar(&opts.Man, "man", false, "Generate man pages")
	cmd.Flags().BoolVar(&opts.YAML, "yaml", false, "Generate YAML reference")
	cmd.Flags().BoolVar(&opts.RST, "rst", false, "Generate reStructuredText documentation")
	cmd.Flags().BoolVar(&opts.Website, "website", false, "Add Jekyll front matter (requires --markdown)")

	_ = cmd.MarkFlagRequired("doc-path")

	return cmd
}

func runDocs(f *cmdutil.Factory, opts *DocsOptions) error {
	// Validate that at least one format is specified
	if !opts.Markdown && !opts.Man && !opts.YAML && !opts.RST {
		cmdutil.PrintError("At least one format flag must be specified")
		cmdutil.PrintNextSteps(
			"Use --markdown to generate Markdown documentation",
			"Use --man to generate man pages",
			"Use --yaml to generate YAML reference",
			"Use --rst to generate reStructuredText documentation",
		)
		return fmt.Errorf("no format specified")
	}

	// Validate that --website requires --markdown
	if opts.Website && !opts.Markdown {
		cmdutil.PrintError("--website flag requires --markdown")
		cmdutil.PrintNextSteps(
			"Add --markdown flag to enable Jekyll front matter generation",
		)
		return fmt.Errorf("--website requires --markdown")
	}

	// Create output directory
	if err := os.MkdirAll(opts.DocPath, 0755); err != nil {
		cmdutil.PrintError("Failed to create output directory: %v", err)
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	logger.Debug().
		Str("doc-path", opts.DocPath).
		Bool("markdown", opts.Markdown).
		Bool("man", opts.Man).
		Bool("yaml", opts.YAML).
		Bool("rst", opts.RST).
		Bool("website", opts.Website).
		Msg("generating documentation")

	// Build the command tree
	// Create a minimal factory for documentation generation (doesn't need Docker/config)
	docFactory := &cmdutil.Factory{
		Version: f.Version,
		Commit:  f.Commit,
	}
	rootCmd := root.NewCmdRoot(docFactory)

	// Generate each requested format
	if opts.Markdown {
		dir := filepath.Join(opts.DocPath, "markdown")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create markdown directory: %w", err)
		}

		var err error
		if opts.Website {
			err = docs.GenMarkdownTreeCustom(rootCmd, dir, jekyllFilePrepender, jekyllLinkHandler)
		} else {
			err = docs.GenMarkdownTree(rootCmd, dir)
		}
		if err != nil {
			cmdutil.PrintError("Failed to generate Markdown documentation: %v", err)
			return err
		}
		fmt.Fprintf(os.Stderr, "Generated Markdown documentation in %s\n", dir)
	}

	if opts.Man {
		dir := filepath.Join(opts.DocPath, "man")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create man directory: %w", err)
		}

		err := docs.GenManTree(rootCmd, dir)
		if err != nil {
			cmdutil.PrintError("Failed to generate man pages: %v", err)
			return err
		}
		fmt.Fprintf(os.Stderr, "Generated man pages in %s\n", dir)
	}

	if opts.YAML {
		dir := filepath.Join(opts.DocPath, "yaml")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create yaml directory: %w", err)
		}

		err := docs.GenYamlTree(rootCmd, dir)
		if err != nil {
			cmdutil.PrintError("Failed to generate YAML documentation: %v", err)
			return err
		}
		fmt.Fprintf(os.Stderr, "Generated YAML documentation in %s\n", dir)
	}

	if opts.RST {
		dir := filepath.Join(opts.DocPath, "rst")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create rst directory: %w", err)
		}

		err := docs.GenReSTTree(rootCmd, dir)
		if err != nil {
			cmdutil.PrintError("Failed to generate reStructuredText documentation: %v", err)
			return err
		}
		fmt.Fprintf(os.Stderr, "Generated reStructuredText documentation in %s\n", dir)
	}

	return nil
}

// jekyllFilePrepender returns Jekyll front matter for a given filename.
func jekyllFilePrepender(filename string) string {
	// Extract command name from filename (e.g., "clawker_container_run.md" -> "clawker container run")
	base := filepath.Base(filename)
	name := strings.TrimSuffix(base, ".md")
	cmdPath := strings.ReplaceAll(name, "_", " ")

	// Create permalink path (e.g., "/cli/clawker/container/run/")
	permalink := "/cli/" + strings.ReplaceAll(name, "_", "/") + "/"

	return fmt.Sprintf(`---
layout: manual
permalink: %s
title: %s
---

`, permalink, cmdPath)
}

// jekyllLinkHandler creates relative markdown links for Jekyll sites.
func jekyllLinkHandler(cmdPath string) string {
	// Transform command path to relative link (e.g., "clawker container" -> "clawker_container.md")
	return strings.ReplaceAll(cmdPath, " ", "_") + ".md"
}
