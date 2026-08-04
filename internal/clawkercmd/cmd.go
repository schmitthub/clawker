package clawkercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/clawker"

	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/changelog"
	"github.com/schmitthub/clawker/internal/cmd/factory"
	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
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

	// CLI runtime state (the update-check cache + changelog cursor) is resolved
	// lazily inside checkForUpdate/checkForChanges via f.CLIState(), which run
	// AFTER the command. A state-store error there aborts that one check and is
	// reported as a warning on stderr.

	// Single root context for the process. The SIGINT/SIGTERM signal context
	// (below) and the background-notification context all derive from it as
	// siblings. The notification context is cancelled explicitly right after the
	// command returns (gh CLI pattern — see below the ExecuteC call), so it need
	// not be a child of the signal context to abort its I/O on Ctrl+C.
	ctx := context.Background()

	// Background FETCH for the update notifier and the changelog teaser. Pattern
	// from gh CLI: goroutine + buffered channel + blocking read. Context
	// cancellation aborts in-flight I/O when the command finishes first. The
	// buffered(1) channel lets the goroutine send and exit even if Main() returns
	// early (e.g. root command creation fails) without reading from it.
	//
	// This half is network ONLY — it touches no config, no state file, nothing on
	// disk. Everything that reads or writes state (the update TTL gate, the
	// changelog cursor) runs after the command in drainNotifications, so a
	// command can turn notifications off for itself mid-run and nothing will
	// have been persisted behind its back.
	//
	// ONE goroutine runs both fetches, in sequence. Nothing here writes, so the
	// serialization is no longer about being a single writer — it keeps the CLI
	// to one background goroutine and one outbound request at a time. The cost
	// is latency: the changelog fetch starts only after the release fetch
	// finishes. Both are abandoned on cancel and their result is drained after
	// the command returns, so the serial path costs the user nothing.
	notifyCtx, notifyCancel := context.WithCancel(ctx)
	defer notifyCancel()

	notifyChan := make(chan notifications, 1)

	// Create root command with build metadata
	rootCmd, err := root.NewCmdRoot(f, buildVersion, buildDate)
	if err != nil {
		fmt.Fprintf(f.IOStreams.ErrOut, "failed to create root command: %v\n", err)
		return 1
	}

	// notificationsSuppressed applies the env/CI/TTY opt-out to the session, the
	// single gate for BOTH background notifications (update notifier + changelog
	// teaser). With notifications off the fetch goroutine is not launched at all,
	// so a suppressed run does ZERO network I/O and, because the tail is skipped
	// too, no cursor persist. The opt-out lives here in the caller;
	// internal/update and internal/changelog do not enforce it.
	session := f.Session()
	notificationsSuppressed(f.IOStreams, session)

	if session.Notifications() {
		go func() {
			// Guarantee exactly one send on the buffered(1) channel on every path,
			// including a panic: the deferred func always runs, runs once, and is
			// the sole sender. Each check also carries its own recover (see
			// runUpdateCheck / runChangelogCheck), so this one is the backstop for
			// a panic outside them.
			var n notifications
			defer func() {
				if r := recover(); r != nil {
					cs := f.IOStreams.ColorScheme()
					fmt.Fprintf(f.IOStreams.ErrOut, "%s notification goroutine panicked: %v\n", cs.WarningIcon(), r)
				}
				notifyChan <- n
			}()
			n.release, n.releaseErr = getLatestReleaseInfo(notifyCtx, f, consts.GitHubRepo)
			n.entries, n.entriesErr = getChangelogEntries(notifyCtx, f)
		}()
	}

	// Silence Cobra's built-in error printing — we handle it in printError.
	rootCmd.SilenceErrors = true

	// Wire SIGINT/SIGTERM to the root context so Ctrl+C propagates through
	// cmd.Context() to every caller (WaitForHealthy, etc.) instead of hanging.
	signalCtx, signalStop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer signalStop()
	rootCmd.SetContext(signalCtx)

	cmd, err := rootCmd.ExecuteC()

	// Flush buffered logs + the OTEL provider on exit. loggerCtx is a child of the
	// signal context, and loggerCancel() is called below (before draining the
	// notifications) once the command has returned — so the deferred Close always
	// runs with an already-canceled context and never does a blocking final
	// export. A short-lived command must not block its own exit on an unreachable
	// collector; every record is already durable in the file, and the OTEL batch
	// rides the export interval during the run. A Ctrl+C cancels signalCtx, which
	// tears the flush down the same way.
	loggerCtx, loggerCancel := context.WithCancel(signalCtx)
	defer loggerCancel()
	defer func() {
		if log, err := f.Logger(); err == nil {
			_ = log.Close(loggerCtx)
		}
	}()

	// gh CLI pattern: cancel the background fetches now, before draining their
	// channel. Cancelling aborts any in-flight HTTP so the drain returns promptly
	// instead of blocking up to the 30s HTTP client timeout — most importantly
	// after a Ctrl+C, where the command was already interrupted and the user wants
	// out. An aborted fetch contributes its zero value; with nothing fetched
	// there is nothing to decide, so the state file is left untouched and the
	// whole check is simply retried next run. The deferred cancel above remains
	// for the early-return paths that never reach here (e.g. root command
	// creation failing).
	notifyCancel()
	// Cancel the logger context now that the command has returned, so the
	// deferred Close above unwinds its OTEL shutdown immediately instead of
	// blocking exit on a final export.
	loggerCancel()

	// drainNotifications collects what was fetched and only then reads and
	// writes the state file to decide what is worth showing. A session that
	// turned notifications off — including one the command itself turned off
	// while running, such as an elevated command that must not leave
	// root-owned files behind — skips the whole tail: no drain, no state read,
	// no cursor write, nothing printed. Any goroutine already in flight sends
	// into the buffered channel and exits on its own.
	drainNotifications := func() {
		if !session.Notifications() {
			return
		}
		n := <-notifyChan

		cs := f.IOStreams.ColorScheme()
		if n.releaseErr != nil {
			fmt.Fprintf(f.IOStreams.ErrOut, "%s update check failed: %v\n", cs.WarningIcon(), n.releaseErr)
		}
		if n.entriesErr != nil {
			fmt.Fprintf(f.IOStreams.ErrOut, "%s changelog check failed: %v\n", cs.WarningIcon(), n.entriesErr)
		}

		printUpdateNotification(f.IOStreams, runUpdateCheck(f, buildVersion, n.release))
		printChangelogTeaser(f.IOStreams, runChangelogCheck(f, buildVersion, n.entries))
	}

	if err != nil {
		if errors.Is(err, cmdutil.SilentError) {
			// Already displayed — no-op
		} else if errors.Is(err, whail.ErrDockerNotAvailable) {
			printDockerInstallHelper(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err)
		} else {
			printError(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err, cmd)
		}

		drainNotifications()

		var exitErr *cmdutil.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.Code
		}
		return 1
	}

	drainNotifications()

	return 0
}

// notifications carries the results of the two background checks from the one
// goroutine that runs them to the drain in Main. A zero value renders nothing:
// both renderers self-guard, so an abandoned (cancelled) run needs no sentinel.
// notifications carries what the pre-command goroutine FETCHED — raw upstream
// data and the fetch errors, nothing decided. Interpreting it needs the state
// file (the update TTL gate, the changelog cursor), so that work happens after
// the command returns, where the session flags are final.
type notifications struct {
	release    *update.GithubRelease
	releaseErr error
	entries    []changelog.Entry
	entriesErr error
}

// runUpdateCheck interprets an already-fetched release and reports the newer
// version, or nil when there is nothing to report. It runs after the command,
// so this is where the state file is read (the TTL gate) and written
// (RecordUpdateCheck). A nil release means the fetch failed or never ran —
// the caller has already reported that, so there is nothing to decide.
//
// CheckForUpdate validates currentVersion as semver (a non-release "DEV" build
// is not parseable semver), applies the freshness gate, and persists the
// result itself. It returns (nil, nil) when not newer or TTL-fresh.
func runUpdateCheck(f *cmdutil.Factory, currentVersion string, release *update.GithubRelease) *update.ReleaseInfo {
	if release == nil {
		return nil
	}
	// A recovered panic returns the zero value: the result is unnamed, so
	// nothing was assigned to it when the stack unwound.
	defer func() {
		if r := recover(); r != nil {
			cs := f.IOStreams.ColorScheme()
			fmt.Fprintf(f.IOStreams.ErrOut, "%s update check panicked: %v\n", cs.WarningIcon(), r)
		}
	}()
	rel, err := checkForUpdate(f, currentVersion, release)
	if err != nil {
		cs := f.IOStreams.ColorScheme()
		fmt.Fprintf(f.IOStreams.ErrOut, "%s update check failed: %v\n", cs.WarningIcon(), err)
	}
	return rel
}

// runChangelogCheck diffs already-fetched entries against the cursor and
// reports what the user has not seen. Like runUpdateCheck it runs after the
// command — the cursor read and advance both happen here — and recovers on its
// own so neither check can take down the other.
func runChangelogCheck(f *cmdutil.Factory, currentVersion string, entries []changelog.Entry) []changelog.Entry {
	if entries == nil {
		return nil
	}
	// A recovered panic returns the zero value: the result is unnamed, so
	// nothing was assigned to it when the stack unwound.
	defer func() {
		if r := recover(); r != nil {
			cs := f.IOStreams.ColorScheme()
			fmt.Fprintf(f.IOStreams.ErrOut, "%s changelog check panicked: %v\n", cs.WarningIcon(), r)
		}
	}()
	// CheckForChanges returns gained entries even when only the cursor advance
	// fails, so the entries are kept regardless of the error.
	gained, err := checkForChanges(f, currentVersion, entries)
	if err != nil {
		cs := f.IOStreams.ColorScheme()
		fmt.Fprintf(f.IOStreams.ErrOut, "%s changelog check failed: %v\n", cs.WarningIcon(), err)
	}
	return gained
}

// getChangelogEntries resolves the HttpClient noun from the Factory and fetches
// the curated changelog. It is the fetch half of the teaser — network only, no
// state — so it can run before the command with only the context to bound it.
func getChangelogEntries(ctx context.Context, f *cmdutil.Factory) ([]changelog.Entry, error) {
	httpClient, err := f.HttpClient()
	if err != nil {
		return nil, err
	}
	entries, err := changelog.GetChangelogEntries(ctx, httpClient)
	if err != nil {
		return nil, fmt.Errorf("getting changelog entries: %w", err)
	}
	return entries, nil
}

// checkForChanges resolves the CLIState noun from the Factory and hands the
// already-fetched entries to changelog.CheckForChanges. It is the decide half:
// it reads the cursor and advances it, so it runs after the command.
func checkForChanges(f *cmdutil.Factory, currentVersion string, entries []changelog.Entry) ([]changelog.Entry, error) {
	cliState, err := f.CLIState()
	if err != nil {
		return nil, err
	}
	gained, err := changelog.CheckForChanges(entries, cliState, currentVersion)
	if err != nil {
		return gained, fmt.Errorf("checking changelog for teaser: %w", err)
	}
	return gained, nil
}

func getLatestReleaseInfo(ctx context.Context, f *cmdutil.Factory, repo string) (*update.GithubRelease, error) {
	httpClient, err := f.HttpClient()
	if err != nil {
		return nil, fmt.Errorf("resolving HTTP client: %w", err)
	}
	release, err := update.GetLatestReleaseInfo(ctx, httpClient, repo)
	if err != nil {
		return nil, fmt.Errorf("checking %s: %w", repo, err)
	}
	return release, nil
}

// checkForUpdate resolves the HttpClient and CLIState nouns from the Factory and
// hands them to update.CheckForUpdate. It is the update notifier's single entry
// from Main; a noun-resolution error aborts just this one background check and is
// logged by the caller, never surfaced.
func checkForUpdate(
	f *cmdutil.Factory,
	currentVersion string,
	release *update.GithubRelease,
) (*update.ReleaseInfo, error) {
	cliState, err := f.CLIState()
	if err != nil {
		return nil, err
	}
	rel, err := update.CheckForUpdate(release, cliState, currentVersion)
	if err != nil {
		return nil, fmt.Errorf("checking for a newer release: %w", err)
	}
	return rel, nil
}

// notificationsSuppressed is the single gate for ALL clawker background
// notifications (the update notifier and the show-once changelog teaser). It is
// computed once in Main, up front: when true, the background goroutine that
// runs both checks is not launched, so the run does zero network I/O and no
// state writes.
func notificationsSuppressed(ios *iostreams.IOStreams, session clawker.Session) {
	// "CI" is the canonical cross-tool CI-detection env var (kept literal).
	if !ios.IsStderrTTY() || os.Getenv(consts.EnvNoNotifier) != "" || os.Getenv("CI") != "" {
		session.SetNotifications(false)
	}
}

// printUpdateNotification prints a version upgrade notification to stderr.
// It self-guards on a nil info (nothing to report); suppression for non-TTY /
// CI / opt-out is gated once up front in Main (notificationsSuppressed).
func printUpdateNotification(ios *iostreams.IOStreams, info *update.ReleaseInfo) {
	if info == nil {
		return
	}

	cs := ios.ColorScheme()
	fmt.Fprintf(ios.ErrOut, "\n%s %s → %s\n",
		cs.Yellow("A new release of clawker is available:"),
		cs.Cyan(info.CurrentVersion),
		cs.Cyan(info.LatestVersion))
	fmt.Fprintf(ios.ErrOut, "To upgrade:\n")
	fmt.Fprintf(ios.ErrOut, "  %s\n", cs.Bold("brew upgrade clawker"))
	fmt.Fprintf(
		ios.ErrOut,
		"  %s\n",
		cs.Bold(
			"curl -fsSL "+consts.RawGitHubBaseURL+"/"+consts.GitHubRepo+"/"+consts.GitHubRefMain+"/scripts/install.sh | bash",
		),
	)
	fmt.Fprintf(ios.ErrOut, "%s\n", cs.Yellow(info.ReleaseURL))
	fmt.Fprintf(
		ios.ErrOut,
		"\n%s After upgrading, run %s in each project to apply security fixes and avoid breaking changes.\n",
		cs.WarningIcon(),
		cs.Bold("clawker build"),
	)
}

// printChangelogTeaser renders the entries gained since the last shown version.
// It self-guards on an empty slice (nothing to show); suppression for non-TTY /
// CI / opt-out is gated once up front in Main (notificationsSuppressed). Each
// entry's full Keep-a-Changelog body is rendered as markdown (sections, bullets,
// inline docs links) under a bold version header — a release spans many kinds,
// so the body is the unit, not a single derived headline.
func printChangelogTeaser(ios *iostreams.IOStreams, entries []changelog.Entry) {
	if len(entries) == 0 {
		return
	}
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
