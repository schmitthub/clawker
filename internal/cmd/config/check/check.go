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
  - Valid YAML syntax
  - Known configuration structure`,
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
	filePath    string // absolute path to the config file
	displayPath string // path for user-facing messages
}

// resolveConfigTarget resolves the --file flag into a configTarget.
func resolveConfigTarget(filePath string) (*configTarget, error) {
	if filePath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to determine working directory: %w", err)
		}
		absPath := filepath.Join(cwd, "clawker.yaml")
		return &configTarget{
			filePath:    absPath,
			displayPath: absPath,
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
			filePath:    absPath,
			displayPath: absPath,
		}, nil
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to access %s: %w", resolved, err)
		}
		return &configTarget{
			filePath:    absPath,
			displayPath: absPath,
		}, nil
	}

	if info.IsDir() {
		return nil, cmdutil.FlagErrorf("--file must be a file, not a directory: %s", filePath)
	}

	return &configTarget{
		filePath:    resolved,
		displayPath: resolved,
	}, nil
}

func checkRun(_ context.Context, opts *CheckOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	target, err := resolveConfigTarget(opts.File)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(target.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(ios.ErrOut, "%s Configuration file not found: %s\n", cs.FailureIcon(), target.displayPath)
			return cmdutil.SilentError
		}
		return fmt.Errorf("failed to read %s: %w", target.displayPath, err)
	}

	_, err = config.ReadFromString(string(data))
	if err != nil {
		fmt.Fprintf(ios.ErrOut, "%s %s: %s\n", cs.FailureIcon(), target.displayPath, err)
		return cmdutil.SilentError
	}

	fmt.Fprintf(ios.ErrOut, "%s %s is valid\n", cs.SuccessIcon(), target.displayPath)
	return nil
}
