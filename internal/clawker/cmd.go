package clawker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/cmd/factory"
	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/update"
)

// Main is the entry point for the clawker CLI.
// It initializes the Factory, creates the root command, and executes it.
// Error rendering is centralized here — commands return typed errors
// rather than printing them directly.
func Main() int {
	// Ensure logs and OTEL provider are flushed on exit
	defer logger.Close()

	buildDate := build.Date
	buildVersion := build.Version

	// Create factory with version info
	f := factory.New(buildVersion)

	// Start background update check with cancellable context.
	// Pattern from gh CLI: goroutine + buffered channel + blocking read.
	// Context cancellation aborts the HTTP request when the command finishes first.
	// Buffered(1) so the goroutine can send and exit even if Main() returns
	// early (e.g. root command creation fails) without reading from the channel.
	updateCtx, updateCancel := context.WithCancel(context.Background())
	defer updateCancel()
	updateMessageChan := make(chan *update.CheckResult, 1)
	go func() {
		rel, err := checkForUpdate(updateCtx, buildVersion)
		if err != nil {
			logger.Debug().Err(err).Msg("update check failed")
		}
		updateMessageChan <- rel
	}()

	// Create root command with build metadata
	rootCmd, err := root.NewCmdRoot(f, buildVersion, buildDate)
	if err != nil {
		fmt.Fprintf(f.IOStreams.ErrOut, "failed to create root command: %v\n", err)
		return 1
	}

	// Silence Cobra's built-in error printing — we handle it in printError.
	rootCmd.SilenceErrors = true

	cmd, err := rootCmd.ExecuteC()

	// Cancel the update context — if the goroutine is still running,
	// the HTTP request will be aborted and it will send nil promptly.
	updateCancel()

	if err != nil {
		if !errors.Is(err, cmdutil.SilentError) {
			printError(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err, cmd)
		}

		// Blocking read — goroutine always sends exactly once
		printUpdateNotification(f.IOStreams, <-updateMessageChan)

		var exitErr *cmdutil.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.Code
		}
		return 1
	}

	// Blocking read — goroutine always sends exactly once
	printUpdateNotification(f.IOStreams, <-updateMessageChan)

	return 0
}

// checkForUpdate wraps update.CheckForUpdate with state file resolution.
// Returns (nil, nil) if the state path can't be determined.
func checkForUpdate(ctx context.Context, currentVersion string) (*update.CheckResult, error) {
	stateFile := updateStatePath()
	if stateFile == "" {
		return nil, nil
	}
	return update.CheckForUpdate(ctx, stateFile, currentVersion, "schmitthub/clawker")
}

// printUpdateNotification prints a version upgrade notification to stderr
// if a newer version is available. Only shown on interactive terminals.
func printUpdateNotification(ios *iostreams.IOStreams, result *update.CheckResult) {
	if result == nil {
		return
	}
	if !ios.IsStderrTTY() {
		return
	}

	cs := ios.ColorScheme()
	fmt.Fprintf(ios.ErrOut, "\n%s %s → %s\n",
		cs.Yellow("A new release of clawker is available:"),
		cs.Cyan(result.CurrentVersion),
		cs.Cyan(result.LatestVersion))
	fmt.Fprintf(ios.ErrOut, "To upgrade:\n")
	fmt.Fprintf(ios.ErrOut, "  %s\n", cs.Bold("brew upgrade clawker"))
	fmt.Fprintf(ios.ErrOut, "  %s\n", cs.Bold("curl -fsSL https://raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh | bash"))
	fmt.Fprintf(ios.ErrOut, "%s\n", cs.Yellow(result.ReleaseURL))
}

// updateStatePath returns the path to the update state cache file.
// Returns empty string if the clawker state directory cannot be determined.
func updateStatePath() string {
	stateDir := config.StateDir()
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "update-state.yaml")
}

// userFormattedError is a duck-typed interface for errors that provide
// rich user-facing output (e.g., Docker errors with context and suggestions).
type userFormattedError interface {
	FormatUserError() string
}

// printError renders an error to the given writer. It dispatches based on
// error type:
//   - FlagError: prints the error followed by usage
//   - userFormattedError: uses rich formatting (e.g., Docker error context)
//   - default: prints failure icon + error message
//
// A contextual help hint is always appended.
func printError(out io.Writer, cs *iostreams.ColorScheme, err error, cmd *cobra.Command) {
	var flagErr *cmdutil.FlagError
	var ufErr userFormattedError

	switch {
	case errors.As(err, &flagErr):
		fmt.Fprintln(out, err)
		fmt.Fprintln(out)
		fmt.Fprintln(out, cmd.UsageString())
		fmt.Fprintf(out, "\nRun '%s --help' for more information.\n", cmd.CommandPath())
	case errors.As(err, &ufErr):
		fmt.Fprint(out, ufErr.FormatUserError())
	default:
		fmt.Fprintf(out, "%s %s\n", cs.FailureIcon(), err)
	}

}
