package generate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/claucker/internal/term"
	"github.com/schmitthub/claucker/pkg/build"
	"github.com/schmitthub/claucker/pkg/build/registry"
	"github.com/schmitthub/claucker/pkg/cmdutil"
	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/spf13/cobra"
)

// GenerateOptions contains the options for the generate command.
type GenerateOptions struct {
	SkipFetch bool
	Cleanup   bool
	Debug     bool
}

// NewCmdGenerate creates a new generate command.
func NewCmdGenerate(f *cmdutil.Factory) *cobra.Command {
	opts := &GenerateOptions{}

	cmd := &cobra.Command{
		Use:   "generate [versions...]",
		Short: "Generate versions.json for Claude Code releases",
		Long: `Fetches Claude Code versions from npm and generates versions.json.

If no versions are specified and versions.json exists, displays current versions.
If versions are specified, fetches them from npm and updates versions.json.

Version patterns:
  latest, stable, next   Resolve via npm dist-tags
  2.1                    Match highest 2.1.x release
  2.1.2                  Exact version match

Examples:
  claucker generate                    # Show current versions.json
  claucker generate latest             # Fetch latest version
  claucker generate latest 2.1         # Fetch multiple versions
  claucker generate --skip-fetch       # Use existing versions.json only`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Debug = f.Debug
			return runGenerate(f, opts, args)
		},
	}

	cmd.Flags().BoolVar(&opts.SkipFetch, "skip-fetch", false, "Skip npm fetch, use existing versions.json")
	cmd.Flags().BoolVar(&opts.Cleanup, "cleanup", true, "Remove obsolete version directories")

	return cmd
}

func runGenerate(f *cmdutil.Factory, opts *GenerateOptions, versions []string) error {
	ctx, cancel := term.SetupSignalContext(context.Background())
	defer cancel()

	versionsFile := filepath.Join(f.WorkDir, "versions.json")

	logger.Debug().
		Strs("versions", versions).
		Bool("skip-fetch", opts.SkipFetch).
		Str("versions-file", versionsFile).
		Msg("starting generate")

	// If no versions specified, show existing versions.json
	if len(versions) == 0 && !opts.SkipFetch {
		return showVersions(versionsFile)
	}

	// If skip-fetch, load and display existing file
	if opts.SkipFetch {
		vf, err := build.LoadVersionsFile(versionsFile)
		if err != nil {
			cmdutil.PrintError("Failed to load versions.json")
			cmdutil.PrintNextSteps(
				"Run 'claucker generate <versions...>' to fetch versions from npm",
				"Ensure versions.json exists in the project root",
			)
			return err
		}
		return displayVersionsFile(vf)
	}

	// Resolve versions from npm
	mgr := build.NewVersionsManager()
	vf, err := mgr.ResolveVersions(ctx, versions, build.ResolveOptions{
		Debug: opts.Debug,
	})
	if err != nil {
		cmdutil.HandleError(err)
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
		cmdutil.PrintError("Failed to save versions.json")
		return err
	}

	fmt.Printf("Saved %d version(s) to %s\n", len(*vf), versionsFile)
	return displayVersionsFile(vf)
}

func showVersions(path string) error {
	vf, err := build.LoadVersionsFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cmdutil.PrintError("No versions.json found")
			cmdutil.PrintNextSteps(
				"Run 'claucker generate latest' to fetch the latest version",
				"Run 'claucker generate 2.1.2' to fetch a specific version",
			)
			return err
		}
		return err
	}

	return displayVersionsFile(vf)
}

func displayVersionsFile(vf *registry.VersionsFile) error {
	fmt.Println("\nVersions:")
	for _, key := range vf.SortedKeys() {
		info := (*vf)[key]
		fmt.Printf("  %s\n", key)
		fmt.Printf("    Debian default: %s\n", info.DebianDefault)
		fmt.Printf("    Alpine default: %s\n", info.AlpineDefault)
		fmt.Printf("    Variants: %d\n", len(info.Variants))
	}
	return nil
}
