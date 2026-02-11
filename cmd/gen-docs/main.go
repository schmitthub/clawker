// gen-docs is a standalone binary for generating CLI documentation.
// It provides documentation generation for clawker CLI in multiple formats
// (Markdown, man pages, YAML, reStructuredText) without the full clawker CLI.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docs"
	"github.com/spf13/pflag"
)

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := pflag.NewFlagSet("gen-docs", pflag.ContinueOnError)

	var (
		flagDocPath  string
		flagMarkdown bool
		flagManPage  bool
		flagYAML     bool
		flagRST      bool
		flagWebsite  bool
	)

	flags.StringVar(&flagDocPath, "doc-path", "", "Output directory for generated docs (required)")
	flags.BoolVar(&flagMarkdown, "markdown", false, "Generate Markdown documentation")
	flags.BoolVar(&flagManPage, "man-page", false, "Generate man pages")
	flags.BoolVar(&flagYAML, "yaml", false, "Generate YAML reference")
	flags.BoolVar(&flagRST, "rst", false, "Generate reStructuredText documentation")
	flags.BoolVar(&flagWebsite, "website", false, "Add Jekyll front matter (requires --markdown)")

	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n\n%s", filepath.Base(args[0]), flags.FlagUsages())
	}

	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	// Validation
	if flagDocPath == "" {
		return fmt.Errorf("--doc-path is required")
	}

	if !flagMarkdown && !flagManPage && !flagYAML && !flagRST {
		return fmt.Errorf("at least one format must be specified (--markdown, --man-page, --yaml, --rst)")
	}

	if flagWebsite && !flagMarkdown {
		return fmt.Errorf("--website requires --markdown")
	}

	// Create output directory
	if err := os.MkdirAll(flagDocPath, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build the command tree
	f := &cmdutil.Factory{}
	rootCmd, err := root.NewCmdRoot(f, "", "")
	if err != nil {
		return fmt.Errorf("building command tree: %w", err)
	}

	// Generate each requested format
	if flagMarkdown {
		dir := filepath.Join(flagDocPath, "markdown")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create markdown directory: %w", err)
		}

		var err error
		if flagWebsite {
			err = docs.GenMarkdownTreeCustom(rootCmd, dir, jekyllFilePrepender, jekyllLinkHandler)
		} else {
			err = docs.GenMarkdownTree(rootCmd, dir)
		}
		if err != nil {
			return fmt.Errorf("failed to generate Markdown documentation: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Generated Markdown documentation in %s\n", dir)
	}

	if flagManPage {
		dir := filepath.Join(flagDocPath, "man")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create man directory: %w", err)
		}

		if err := docs.GenManTree(rootCmd, dir); err != nil {
			return fmt.Errorf("failed to generate man pages: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Generated man pages in %s\n", dir)
	}

	if flagYAML {
		dir := filepath.Join(flagDocPath, "yaml")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create yaml directory: %w", err)
		}

		if err := docs.GenYamlTree(rootCmd, dir); err != nil {
			return fmt.Errorf("failed to generate YAML documentation: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Generated YAML documentation in %s\n", dir)
	}

	if flagRST {
		dir := filepath.Join(flagDocPath, "rst")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create rst directory: %w", err)
		}

		if err := docs.GenReSTTree(rootCmd, dir); err != nil {
			return fmt.Errorf("failed to generate reStructuredText documentation: %w", err)
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
