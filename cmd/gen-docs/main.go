// gen-docs is a standalone binary for generating CLI and configuration documentation.
// It provides documentation generation for clawker CLI in multiple formats
// (Markdown, man pages, YAML, reStructuredText) and auto-generates configuration
// reference docs from schema struct tags.
package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/spf13/pflag"

	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docs"
)

//go:embed configuration.mdx.tmpl
var configDocTemplate string

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
		flagSchemas  bool
	)

	flags.StringVar(&flagDocPath, "doc-path", "", "Output directory for generated docs (required)")
	flags.BoolVar(&flagMarkdown, "markdown", false, "Generate Markdown documentation")
	flags.BoolVar(&flagManPage, "man-page", false, "Generate man pages")
	flags.BoolVar(&flagYAML, "yaml", false, "Generate YAML reference")
	flags.BoolVar(&flagRST, "rst", false, "Generate reStructuredText documentation")
	flags.BoolVar(
		&flagWebsite,
		"website",
		false,
		"Generate MDX-safe output with Mintlify front matter (requires --markdown)",
	)
	flags.BoolVar(
		&flagSchemas,
		"schemas",
		false,
		"Generate config JSON Schemas from struct tags (written to <doc-path>/schemas/)",
	)

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

	if !flagMarkdown && !flagManPage && !flagYAML && !flagRST && !flagSchemas {
		return errors.New("at least one output must be specified (--markdown, --man-page, --yaml, --rst, --schemas)")
	}

	if flagWebsite && !flagMarkdown {
		return fmt.Errorf("--website requires --markdown")
	}

	// Create output directory
	if err := os.MkdirAll( //nolint:gosec // non-secret generated docs; conventional world-readable perms
		flagDocPath,
		0o755,
	); err != nil {
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
		dir := filepath.Join(flagDocPath, "cli-reference")
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("failed to clean cli-reference directory: %w", err)
		}
		if err = os.MkdirAll( //nolint:gosec // non-secret generated docs; conventional world-readable perms
			dir,
			0o755,
		); err != nil {
			return fmt.Errorf("failed to create cli-reference directory: %w", err)
		}

		var err error
		if flagWebsite {
			err = docs.GenMarkdownTreeWebsite(rootCmd, dir, mintlifyFilePrepender, mintlifyLinkHandler)
		} else {
			err = docs.GenMarkdownTree(rootCmd, dir)
		}
		if err != nil {
			return fmt.Errorf("failed to generate Markdown documentation: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Generated CLI reference documentation in %s\n", dir)

		// Generate configuration reference from schema struct tags.
		if flagWebsite {
			if err := genConfigDoc(flagDocPath); err != nil {
				return fmt.Errorf("failed to generate config documentation: %w", err)
			}
			fmt.Fprintf(
				os.Stderr,
				"Generated configuration reference in %s\n",
				filepath.Join(flagDocPath, "configuration.mdx"),
			)
		}
	}

	// JSON Schemas are standalone artifacts served raw (yaml-language-server
	// headers point at them) — independent of every doc format above.
	if flagSchemas {
		schemaDir, sErr := genConfigSchemas(flagDocPath)
		if sErr != nil {
			return fmt.Errorf("failed to generate config JSON schemas: %w", sErr)
		}
		fmt.Fprintf(os.Stderr, "Generated config JSON schemas in %s\n", schemaDir)
	}

	if flagManPage {
		dir := filepath.Join(flagDocPath, "man")
		if err = os.MkdirAll( //nolint:gosec // non-secret generated docs; conventional world-readable perms
			dir,
			0o755,
		); err != nil {
			return fmt.Errorf("failed to create man directory: %w", err)
		}

		if err := docs.GenManTree(rootCmd, dir); err != nil {
			return fmt.Errorf("failed to generate man pages: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Generated man pages in %s\n", dir)
	}

	if flagYAML {
		dir := filepath.Join(flagDocPath, "yaml")
		if err = os.MkdirAll( //nolint:gosec // non-secret generated docs; conventional world-readable perms
			dir,
			0o755,
		); err != nil {
			return fmt.Errorf("failed to create yaml directory: %w", err)
		}

		if err := docs.GenYamlTree(rootCmd, dir); err != nil {
			return fmt.Errorf("failed to generate YAML documentation: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Generated YAML documentation in %s\n", dir)
	}

	if flagRST {
		dir := filepath.Join(flagDocPath, "rst")
		if err = os.MkdirAll( //nolint:gosec // non-secret generated docs; conventional world-readable perms
			dir,
			0o755,
		); err != nil {
			return fmt.Errorf("failed to create rst directory: %w", err)
		}

		if err := docs.GenReSTTree(rootCmd, dir); err != nil {
			return fmt.Errorf("failed to generate reStructuredText documentation: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Generated reStructuredText documentation in %s\n", dir)
	}

	return nil
}

// genConfigDoc renders the embedded configuration.mdx.tmpl template with
// schema metadata to produce the final configuration.mdx.
func genConfigDoc(docPath string) error {
	var buf bytes.Buffer
	if err := docs.GenConfigDoc(&buf, configDocTemplate); err != nil {
		return err
	}

	outPath := filepath.Join(docPath, "configuration.mdx")
	if err := os.WriteFile( //nolint:gosec // non-secret generated docs; conventional world-readable perms
		outPath,
		buf.Bytes(),
		0o644,
	); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	return nil
}

// configSchemaSpec describes one generated config JSON Schema file.
type configSchemaSpec struct {
	typ   reflect.Type
	id    string
	title string
	file  string
}

// configSchemaSpecs is the single source for which config schemas are generated
// and under what id/title/filename. genConfigSchemas writes them; the drift test
// regenerates and compares against the committed files.
func configSchemaSpecs() []configSchemaSpec {
	return []configSchemaSpec{
		{
			reflect.TypeFor[config.Project](),
			consts.SchemaURL(consts.ProjectSchemaFile, consts.GitHubRefMain),
			"clawker project configuration (clawker.yaml)",
			consts.ProjectSchemaFile,
		},
		{
			reflect.TypeFor[config.Settings](),
			consts.SchemaURL(consts.SettingsSchemaFile, consts.GitHubRefMain),
			"clawker settings (settings.yaml)",
			consts.SettingsSchemaFile,
		},
		{
			reflect.TypeFor[config.Manifest](),
			consts.SchemaURL(consts.HarnessSchemaFile, consts.GitHubRefMain),
			"clawker harness bundle manifest (harness.yaml)",
			consts.HarnessSchemaFile,
		},
		{
			reflect.TypeFor[config.StackManifest](),
			consts.SchemaURL(consts.StackSchemaFile, consts.GitHubRefMain),
			"clawker stack definition manifest (stack.yaml)",
			consts.StackSchemaFile,
		},
	}
}

// genConfigSchemas writes the JSON Schema files for the project and settings
// config types into <docPath>/schemas/. The schemas are generated from the same
// struct tags as the configuration reference and are served as raw GitHub
// content addressed by git ref (consts.SchemaURL) so the
// yaml-language-server header the storage layer stamps resolves — release
// binaries pin their own version tag, dev builds follow main. Returns the
// schema output directory.
func genConfigSchemas(docPath string) (string, error) {
	dir := filepath.Join(docPath, filepath.Base(consts.SchemaDocsDir))
	if err := os.MkdirAll( //nolint:gosec // non-secret generated docs; conventional world-readable perms
		dir,
		0o755,
	); err != nil {
		return "", fmt.Errorf("creating schema directory: %w", err)
	}

	for _, s := range configSchemaSpecs() {
		data, err := docs.GenJSONSchema(s.typ, s.id, s.title)
		if err != nil {
			return "", fmt.Errorf("generating %s: %w", s.file, err)
		}
		if err = os.WriteFile( //nolint:gosec // non-secret generated docs; conventional world-readable perms
			filepath.Join(dir, s.file),
			data,
			0o644,
		); err != nil {
			return "", fmt.Errorf("writing %s: %w", s.file, err)
		}
	}
	return dir, nil
}

// mintlifyFilePrepender returns Mintlify-compatible front matter for a given filename.
func mintlifyFilePrepender(filename string) string {
	base := filepath.Base(filename)
	name := strings.TrimSuffix(base, ".md")
	cmdPath := strings.ReplaceAll(name, "_", " ")

	return fmt.Sprintf("---\ntitle: \"%s\"\n---\n\n", cmdPath)
}

// mintlifyLinkHandler creates relative links for Mintlify docs.
// Mintlify uses the file path without extension as the page slug.
func mintlifyLinkHandler(cmdPath string) string {
	return strings.ReplaceAll(cmdPath, " ", "_")
}
