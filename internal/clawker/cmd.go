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
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/update"
	"github.com/schmitthub/clawker/pkg/whail"
)

// Main is the entry point for the clawker CLI.
// It initializes the Factory, creates the root command, and executes it.
// Error rendering is centralized here — commands return typed errors
// rather than printing them directly.
func Main() int {
	buildDate := build.Date
	buildVersion := build.Version

	// Create factory with version info
	f := factory.New(buildVersion)

	// Fail fast if XDG directories collide (e.g. CLAWKER_DATA_DIR == CLAWKER_CONFIG_DIR).
	// Checked before any file I/O to prevent data corruption.
	if err := storage.ValidateDirectories(); err != nil {
		fmt.Fprintf(f.IOStreams.ErrOut, "%s %v\n", f.IOStreams.ColorScheme().FailureIcon(), err)
		return 1
	}

	// Ensure logs and OTEL provider are flushed on exit
	defer func() {
		if log, err := f.Logger(); err == nil {
			log.Close()
		}
	}()

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
			if log, logErr := f.Logger(); logErr == nil {
				log.Debug().Err(err).Msg("update check failed")
			}
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

	// Don't cancel the update context here — the goroutine needs to complete
	// so it can write the cache file. The blocking read below waits for it,
	// and the HTTP client has its own 5s timeout. defer updateCancel() handles
	// cleanup on exit.

	if err != nil {
		if errors.Is(err, cmdutil.SilentError) {
			// Already displayed — no-op
		} else if errors.Is(err, whail.ErrDockerNotAvailable) {
			printDockerInstallHelper(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err)
		} else {
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

// printDockerInstallHelper renders a user-friendly message when the Docker
// daemon cannot be reached, showing the actual error and troubleshooting steps.
func printDockerInstallHelper(out io.Writer, cs *iostreams.ColorScheme, err error) {
	// Extract the actual cause from the DockerError chain
	detail := err.Error()
	var dockerErr *whail.DockerError
	if errors.As(err, &dockerErr) && dockerErr.Unwrap() != nil {
		detail = dockerErr.Unwrap().Error()
	}

	fmt.Fprintf(out, "%s Failed to connect to Docker: %s\n\n", cs.FailureIcon(), cs.Muted(cs.Italic(detail)))
	fmt.Fprintf(out, "%s\n", cs.Bold("Troubleshooting:"))
	fmt.Fprintf(out, "  1. Install Docker Desktop: %s\n", cs.Cyan("https://docs.docker.com/get-docker/"))
	fmt.Fprintf(out, "  2. Start Docker Desktop or run %s\n", cs.Bold("sudo systemctl start docker"))
	fmt.Fprintf(out, "  3. Verify the daemon is reachable: %s\n", cs.Bold("docker info"))
	fmt.Fprintf(out, "  4. Re-run your command\n")
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
