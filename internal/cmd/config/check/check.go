package check

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// CheckOptions holds options for the config check command.
type CheckOptions struct {
	IOStreams *iostreams.IOStreams
	File      string
}

// NewCmdCheck creates the config check command.
func NewCmdCheck(f *cmdutil.Factory, runF func(context.Context, *CheckOptions) error) *cobra.Command {
	opts := &CheckOptions{
		IOStreams: f.IOStreams,
	}

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate your clawker configuration",
		Long: `Validates a clawker.yaml configuration file.

Checks for:
  - Unknown or misspelled fields
  - Required fields (version, build.image or build.dockerfile)
  - Valid field values and formats
  - File existence for referenced paths (dockerfile, includes)
  - Security configuration consistency`,
		Example: `  # Validate configuration in current directory
  clawker config check

  # Validate a specific file
  clawker config check --file examples/go.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return checkRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVarP(&opts.File, "file", "f", "", "Path to clawker.yaml file to validate")

	return cmd
}

// configTarget holds the resolved paths for config validation.
type configTarget struct {
	loaderDir   string // directory to pass to NewProjectLoader
	displayPath string // original path for user-facing messages
	cleanup     func() // cleanup function (e.g. remove temp dir)
}

// close calls cleanup if set. Nil-safe for use in defer.
func (t *configTarget) close() {
	if t != nil && t.cleanup != nil {
		t.cleanup()
	}
}

// resolveConfigTarget resolves the --file flag into a configTarget.
// If the file is not named "clawker.yaml", it symlinks it into a temp dir
// so the ProjectLoader (which always looks for "clawker.yaml") can find it.
func resolveConfigTarget(filePath string) (*configTarget, error) {
	if filePath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to determine working directory: %w", err)
		}
		return &configTarget{
			loaderDir:   cwd,
			displayPath: filepath.Join(cwd, config.ConfigFileName),
			cleanup:     func() {},
		}, nil
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	// Resolve symlinks for accurate stat
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to access %s: %w", absPath, err)
		}
		// File doesn't exist — return unresolved path for "not found" message
		return &configTarget{
			loaderDir:   filepath.Dir(absPath),
			displayPath: absPath,
			cleanup:     func() {},
		}, nil
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to access %s: %w", resolved, err)
		}
		return &configTarget{
			loaderDir:   filepath.Dir(absPath),
			displayPath: absPath,
			cleanup:     func() {},
		}, nil
	}

	if info.IsDir() {
		return nil, cmdutil.FlagErrorf("--file must be a file, not a directory: %s", filePath)
	}

	// If the file is already named "clawker.yaml", use its directory directly
	if filepath.Base(resolved) == config.ConfigFileName {
		return &configTarget{
			loaderDir:   filepath.Dir(resolved),
			displayPath: resolved,
			cleanup:     func() {},
		}, nil
	}

	// File has a different name — symlink it as "clawker.yaml" in a temp dir
	tmpDir, err := os.MkdirTemp("", "clawker-check-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	linkPath := filepath.Join(tmpDir, config.ConfigFileName)
	if err := os.Symlink(resolved, linkPath); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("failed to prepare config file: %w", err)
	}

	return &configTarget{
		loaderDir:   tmpDir,
		displayPath: resolved,
		cleanup:     func() { os.RemoveAll(tmpDir) },
	}, nil
}

func checkRun(_ context.Context, opts *CheckOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	// Resolve config path
	target, err := resolveConfigTarget(opts.File)
	if err != nil {
		return err
	}
	defer target.close()

	// Create loader: skip user defaults when --file is set (validate in isolation)
	var loaderOpts []config.ProjectLoaderOption
	if opts.File == "" {
		loaderOpts = append(loaderOpts, config.WithUserDefaults(""))
	}
	loader := config.NewProjectLoader(target.loaderDir, loaderOpts...)

	// Check file exists
	if !loader.Exists() {
		fmt.Fprintf(ios.ErrOut, "%s Configuration file not found: %s\n", cs.FailureIcon(), target.displayPath)
		return cmdutil.SilentError
	}

	// Load config — catches YAML errors and unknown fields
	project, err := loader.Load()
	if err != nil {
		fmt.Fprintf(ios.ErrOut, "%s %s: %s\n", cs.FailureIcon(), target.displayPath, err)
		return cmdutil.SilentError
	}

	// Semantic validation
	validator := config.NewValidator(target.loaderDir)
	valErr := validator.Validate(project)

	// Print warnings
	for _, w := range validator.Warnings() {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), w)
	}

	// Print errors
	if valErr != nil {
		var multi *config.MultiValidationError
		if errors.As(valErr, &multi) {
			for _, e := range multi.ValidationErrors() {
				fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.FailureIcon(), e)
			}
		} else {
			fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.FailureIcon(), valErr)
		}
		return cmdutil.SilentError
	}

	fmt.Fprintf(ios.ErrOut, "%s %s is valid\n", cs.SuccessIcon(), target.displayPath)
	return nil
}
