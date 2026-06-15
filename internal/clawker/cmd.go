package clawker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/changelog"
	"github.com/schmitthub/clawker/internal/cmd/factory"
	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/state"
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
			log.Close() // REVIEW: shouldn't we be just passing logger a cancellable context instead of this Close() bullshit. if so create a TODO about it don't fix in this branch.
		}
	}()

	// Construct the CLI runtime-state facade directly — it is used only here in
	// Main (the background update check and the changelog teaser), so it is not a
	// Factory noun. A missing/unreadable state store degrades to a nil facade: the
	// update check proceeds with a zero "never checked" time and the changelog
	// teaser is a silent no-op. Errors are logged to the file log, never surfaced.
	var cliState *state.State
	if st, err := state.New(); err == nil {
		cliState = st
	} else if log, logErr := f.Logger(); logErr == nil {
		log.Debug().Err(err).Msg("reading CLI state") // REVIEW: if the state file is corrupted or errors we need to exit out
	}

	// Start background update check with cancellable context.
	// Pattern from gh CLI: goroutine + buffered channel + blocking read.
	// Context cancellation aborts the HTTP request when the command finishes first.
	// Buffered(1) so the goroutine can send and exit even if Main() returns
	// early (e.g. root command creation fails) without reading from the channel.
	updateCtx, updateCancel := context.WithCancel(context.Background()) // REVIEW: `context.Background()` should be assignned to a variable at the top of this function and passed down to all functions that need it instead of creating multiple contexts in this function and passing them down. ie ctx := context.Background() at the top and then ctx, updateCancel := context.WithCancel(ctx) here and ctx, changelogCancel := context.WithCancel(ctx) below. create a TODO about this.
	defer updateCancel()
	updateMessageChan := make(chan *update.CheckResult, 1) // REVIEW: why buffer this?
	go func() {
		// Guarantee exactly one send on the buffered(1) channel on every path,
		// including a panic: the deferred func always runs, runs once, and is
		// the sole sender. A panic in this teaser goroutine must not crash the
		// user's command — recover, log, and report no update.
		var rel *update.CheckResult
		defer func() {
			if r := recover(); r != nil {
				if log, logErr := f.Logger(); logErr == nil {
					log.Debug().Interface("panic", r).Msg("update check goroutine panicked") // we should just be printing a warning not a debug log if debug is enabled. do we not have a global debug flag?
				}
				rel = nil
			}
			updateMessageChan <- rel
		}()
		var err error
		// checkForUpdate reads the freshness gate from cliState and persists the
		// result there itself (RecordUpdateCheck). A non-nil err may accompany a
		// non-nil rel (a best-effort persistence failure) — log it, still report
		// the result.
		rel, err = checkForUpdate(updateCtx, cliState, buildVersion)
		if err != nil {
			if log, logErr := f.Logger(); logErr == nil {
				log.Debug().Err(err).Msg("update check failed")
			}
		}
	}()

	// Check the curated changelog for entries gained since the show-once cursor in
	// the background so the teaser never blocks the user's command on network I/O.
	// CheckForChanges owns the entire cursor lifecycle (read, first-run seed,
	// advance); Main only parses the running version and renders the result. Its
	// own cancellable context (NOT the update check's) bounds the request.
	// Buffered(1) so the goroutine can send and exit even if Main() returns early.
	changelogCtx, changelogCancel := context.WithCancel(context.Background())
	defer changelogCancel()
	changelogChan := make(chan []changelog.Entry, 1) // REVIEW: why buffer this?
	// persistCursor is false on a suppressed run (non-TTY / CI / opt-out): the
	// cursor is left for the next interactive run to advance.
	persistCursor := !changelogSuppressed(f.IOStreams)
	go func() {
		// Guarantee exactly one send on the buffered(1) channel on every path,
		// including a panic: the deferred func always runs, runs once, and is the
		// sole sender. A panic in this teaser goroutine must not crash the user's
		// command — recover, log, and show nothing.
		var gained []changelog.Entry
		defer func() {
			if r := recover(); r != nil {
				if log, logErr := f.Logger(); logErr == nil {
					log.Debug().Interface("panic", r).Msg("changelog goroutine panicked")
				}
				gained = nil
			}
			changelogChan <- gained
		}()
		// build.Version is overwritten from build info at startup. On a non-release
		// build whose version is not a parseable semver there is no range to diff,
		// so show nothing — the parse failure is the signal, not an explicit
		// dev-build gate (opting out is the env var's job).
		current, err := semver.NewVersion(strings.TrimPrefix(buildVersion, "v")) // REVIEW: NewVersion accepts and handles the "v" prefix automatically through its regex pattern which includes an optional v? at the start.
		if err != nil {
			if log, logErr := f.Logger(); logErr == nil {
				log.Debug().Err(err).Str("version", buildVersion).Msg("unparseable build version; skipping changelog teaser")
			}
			return
		}
		g, err := changelog.CheckForChanges(changelogCtx, cliState, current, persistCursor)
		if err != nil {
			if log, logErr := f.Logger(); logErr == nil {
				log.Debug().Err(err).Msg("checking changelog for teaser")
			}
			return
		}
		gained = g
	}()

	// Create root command with build metadata
	rootCmd, err := root.NewCmdRoot(f, buildVersion, buildDate)
	if err != nil {
		fmt.Fprintf(f.IOStreams.ErrOut, "failed to create root command: %v\n", err)
		return 1
	}

	// Silence Cobra's built-in error printing — we handle it in printError.
	rootCmd.SilenceErrors = true

	// Wire SIGINT/SIGTERM to the root context so Ctrl+C propagates through
	// cmd.Context() to every caller (WaitForHealthy, etc.) instead of hanging.
	signalCtx, signalStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer signalStop()
	rootCmd.SetContext(signalCtx)

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
		maybeShowChangelog(f, <-changelogChan)

		var exitErr *cmdutil.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.Code
		}
		return 1
	}

	// Blocking read — goroutine always sends exactly once
	printUpdateNotification(f.IOStreams, <-updateMessageChan)
	maybeShowChangelog(f, <-changelogChan)

	return 0
}

// checkForUpdate wraps update.CheckForUpdate with the clawker repo. The update
// package reads the freshness gate from st and persists the result there itself.
// REVIEW: why does this even exist at this point its just wrapping the exported package function jfc
func checkForUpdate(ctx context.Context, st *state.State, currentVersion string) (*update.CheckResult, error) {
	return update.CheckForUpdate(ctx, st, currentVersion, consts.GitHubRepo)
}

// printUpdateNotification prints a version upgrade notification to stderr
// if a newer version is available. Only shown on interactive terminals.
func printUpdateNotification(ios *iostreams.IOStreams, result *update.CheckResult) {
	if result == nil || !result.IsNewer {
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
	fmt.Fprintf(ios.ErrOut, "  %s\n", cs.Bold("curl -fsSL "+consts.RawGitHubBaseURL+"/"+consts.GitHubRepo+"/main/scripts/install.sh | bash"))
	fmt.Fprintf(ios.ErrOut, "%s\n", cs.Yellow(result.ReleaseURL))
	fmt.Fprintf(ios.ErrOut, "\n%s After upgrading, run %s in each project to apply security fixes and avoid breaking changes.\n",
		cs.WarningIcon(), cs.Bold("clawker build"))
}

// changelogSuppressed reports whether the show-once changelog teaser must stay
// silent. It mirrors the update-notifier discipline: stderr must be a TTY, and
// the same opt-out env vars (CLAWKER_NO_UPDATE_NOTIFIER, CI) apply. A suppressed
// run leaves the cursor untouched so the teaser retries on the next interactive
// run.
// REVIEW: we need to unify this as a shared helper with the update check's suppression logic
func changelogSuppressed(ios *iostreams.IOStreams) bool {
	if !ios.IsStderrTTY() {
		return true
	}
	if os.Getenv(consts.EnvNoUpdateNotifier) != "" {
		return true
	}
	// "CI" is the canonical cross-tool CI-detection env var (kept literal,
	// matching internal/update's convention).
	if os.Getenv("CI") != "" {
		return true
	}
	return false
}

// maybeShowChangelog renders the show-once teaser after the command completes.
// gained is the result of the background changelog.CheckForChanges (which, when
// output is shown, already advanced the cursor). This only renders: nothing
// prints when there are no gained entries or when output is suppressed (non-TTY
// / CI / opt-out) — a suppressed run left the cursor for the next interactive
// run to retry. Mirrors the update-notifier discipline (stderr, TTY-only).
// REVIEW: This 100000% belongs in the changelog package. but based on what it does right now it just needs to be in the shared output gate lol
func maybeShowChangelog(f *cmdutil.Factory, gained []changelog.Entry) {
	if len(gained) == 0 {
		return
	}
	ios := f.IOStreams
	if changelogSuppressed(ios) {
		return
	}
	printChangelogTeaser(ios, gained)
}

// printChangelogTeaser renders the entries gained since the last shown version.
// Each entry's full Keep-a-Changelog body is rendered as markdown (sections,
// bullets, inline docs links) under a bold version header — a release spans
// many kinds, so the body is the unit, not a single derived headline.
func printChangelogTeaser(ios *iostreams.IOStreams, entries []changelog.Entry) {
	cs := ios.ColorScheme()
	icon := "[new]"
	if cs.Enabled() {
		icon = "📣"
	}
	fmt.Fprintf(ios.ErrOut, "\n%s What's new in clawker:\n", icon)
	for _, e := range entries {
		header := "v" + e.Version
		if e.Date != "" {
			header += " — " + e.Date
		}
		fmt.Fprintf(ios.ErrOut, "\n%s\n", cs.Bold(header))
		fmt.Fprintln(ios.ErrOut, strings.TrimRight(ios.RenderMarkdown(e.Body), "\n"))
	}
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
//   - FlagError: prints the error followed by usage and a help hint
//   - userFormattedError: uses rich formatting (e.g., Docker error context)
//   - default: prints failure icon + error message
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
