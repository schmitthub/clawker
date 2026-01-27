package generate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/pkg/build"
	"github.com/schmitthub/clawker/pkg/build/registry"
	"github.com/spf13/cobra"
)

// GenerateOptions contains the options for the generate command.
type GenerateOptions struct {
	SkipFetch bool
	Cleanup   bool
	Debug     bool
	OutputDir string // Explicit output directory override
}

// NewCmdGenerate creates a new generate command.
func NewCmdGenerate(f *cmdutil2.Factory) *cobra.Command {
	opts := &GenerateOptions{}

	cmd := &cobra.Command{
		Use:   "generate [versions...]",
		Short: "Generate Dockerfiles for Claude Code releases",
		Long: `Fetches Claude Code versions from npm and generates Dockerfiles.

Generates versions.json and Dockerfiles for each version/variant combination.
Files are saved to ~/.clawker/build/ (or use --output to specify a directory).

If no versions are specified, displays current versions.json.
If versions are specified, fetches them from npm and generates Dockerfiles.

Version patterns:
  latest, stable, next   Resolve via npm dist-tags
  2.1                    Match highest 2.1.x release
  2.1.2                  Exact version match`,
		Example: `  # Generate Dockerfiles for latest version
  clawker generate latest

  # Generate for multiple versions
  clawker generate latest 2.1

  # Output to specific directory
  clawker generate --output ./build latest

  # Show existing versions.json
  clawker generate`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Debug = f.Debug
			return runGenerate(f, opts, args)
		},
	}

	cmd.Flags().BoolVar(&opts.SkipFetch, "skip-fetch", false, "Skip npm fetch, use existing versions.json")
	cmd.Flags().BoolVar(&opts.Cleanup, "cleanup", true, "Remove obsolete version directories")
	cmd.Flags().StringVarP(&opts.OutputDir, "output", "o", "", "Output directory for generated files")

	return cmd
}

func runGenerate(f *cmdutil2.Factory, opts *GenerateOptions, versions []string) error {
	ctx, cancel := term.SetupSignalContext(context.Background())
	defer cancel()
	ios := f.IOStreams

	// Determine output directory: explicit flag > factory default
	outputDir := f.BuildOutputDir
	if opts.OutputDir != "" {
		outputDir = opts.OutputDir
	}

	// Ensure output directory exists
	if err := config.EnsureDir(outputDir); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	versionsFile := filepath.Join(outputDir, "versions.json")

	logger.Debug().
		Strs("versions", versions).
		Bool("skip-fetch", opts.SkipFetch).
		Str("output-dir", outputDir).
		Str("versions-file", versionsFile).
		Msg("starting generate")

	// If no versions specified, show existing versions.json
	if len(versions) == 0 && !opts.SkipFetch {
		return showVersions(ios, versionsFile)
	}

	// If skip-fetch, load and display existing file
	if opts.SkipFetch {
		vf, err := build.LoadVersionsFile(versionsFile)
		if err != nil {
			cmdutil2.PrintError(ios, "Failed to load versions.json from %s", outputDir)
			cmdutil2.PrintNextSteps(ios,
				"Run 'clawker generate <versions...>' to fetch versions from npm",
				fmt.Sprintf("Ensure versions.json exists in %s", outputDir),
			)
			return err
		}
		return displayVersionsFile(vf, ios.ErrOut)
	}

	// Resolve versions from npm
	mgr := build.NewVersionsManager()
	vf, err := mgr.ResolveVersions(ctx, versions, build.ResolveOptions{
		Debug: opts.Debug,
	})
	if err != nil {
		cmdutil2.HandleError(ios, err)
		return err
	}

	// Merge with existing versions if file exists
	existing, err := build.LoadVersionsFile(versionsFile)
	if err == nil && existing != nil {
		// Merge: new versions override existing
		for k, v := range *vf {
			(*existing)[k] = v
		}
		vf = existing
	}

	// Save updated versions.json
	if err := build.SaveVersionsFile(versionsFile, vf); err != nil {
		cmdutil2.PrintError(ios, "Failed to save versions.json")
		return err
	}

	fmt.Fprintf(ios.ErrOut, "Saved %d version(s) to %s\n", len(*vf), versionsFile)

	// Generate Dockerfiles
	dfMgr := build.NewDockerfileManager(outputDir, nil)
	if err := dfMgr.GenerateDockerfiles(vf); err != nil {
		cmdutil2.PrintError(ios, "Failed to generate Dockerfiles")
		return err
	}
	fmt.Fprintf(ios.ErrOut, "Generated Dockerfiles in %s\n", dfMgr.DockerfilesDir())

	return displayVersionsFile(vf, ios.ErrOut)
}

func showVersions(ios *cmdutil2.IOStreams, path string) error {
	vf, err := build.LoadVersionsFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cmdutil2.PrintError(ios, "No versions.json found")
			cmdutil2.PrintNextSteps(ios,
				"Run 'clawker generate latest' to fetch the latest version",
				"Run 'clawker generate 2.1.2' to fetch a specific version",
			)
			return err
		}
		return err
	}

	return displayVersionsFile(vf, ios.ErrOut)
}

func displayVersionsFile(vf *registry.VersionsFile, w io.Writer) error {
	fmt.Fprintln(w, "\nVersions:")
	for _, key := range vf.SortedKeys() {
		info := (*vf)[key]
		fmt.Fprintf(w, "  %s\n", key)
		fmt.Fprintf(w, "    Debian default: %s\n", info.DebianDefault)
		fmt.Fprintf(w, "    Alpine default: %s\n", info.AlpineDefault)
		fmt.Fprintf(w, "    Variants: %d\n", len(info.Variants))
	}
	return nil
}
