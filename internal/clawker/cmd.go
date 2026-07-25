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

	"github.com/spf13/cobra"

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
	// lazily inside checkForUpdate/checkForChanges via f.CLIState(). A state-store
	// error there aborts that one background check and is logged to the file log,
	// never surfaced.

	// Single root context for the process. The SIGINT/SIGTERM signal context
	// (below) and the background-notification context all derive from it as
	// siblings. The notification context is cancelled explicitly right after the
	// command returns (gh CLI pattern — see below the ExecuteC call), so it need
	// not be a child of the signal context to abort its I/O on Ctrl+C.
	ctx := context.Background()

	// notificationsSuppressed is the single gate for BOTH background notifications
	// (update notifier + changelog teaser). When it is true the background
	// goroutine is not launched at all, so a suppressed run does ZERO network I/O
	// and no cursor persist. The env/CI opt-out lives here in the caller;
	// internal/update and internal/changelog do not enforce it.
	suppressed := notificationsSuppressed(f.IOStreams)

	// Background update check + changelog teaser, both gated by `suppressed`.
	// Pattern from gh CLI: goroutine + buffered channel + blocking read. Context
	// cancellation aborts in-flight I/O when the command finishes first. The
	// buffered(1) channel lets the goroutine send and exit even if Main() returns
	// early (e.g. root command creation fails) without reading from it.
	//
	// ONE goroutine runs BOTH checks, in sequence. They share the one
	// f.CLIState() facade, and each check's persist is a Set+Set+Write cycle the
	// store cannot make atomic across calls (see internal/storage/CLAUDE.md).
	// Serializing them onto a single goroutine is what makes the CLI a single
	// writer of the state file, so a Write can never flush another check's
	// half-staged fields. The cost is latency: the changelog fetch starts only
	// after the update fetch finishes. Both are background work whose result is
	// drained after the command returns, and both are abandoned on cancel, so
	// the serial path costs the user nothing.
	notifyCtx, notifyCancel := context.WithCancel(ctx)
	defer notifyCancel()

	notifyChan := make(chan notifications, 1)

	if !suppressed {
		go func() {
			// Guarantee exactly one send on the buffered(1) channel on every path,
			// including a panic: the deferred func always runs, runs once, and is
			// the sole sender. Each check also carries its own recover (see
			// runUpdateCheck / runChangelogCheck), so this one is the backstop for
			// a panic outside them.
			var n notifications
			defer func() {
				if r := recover(); r != nil {
					if log, logErr := f.Logger(); logErr == nil {
						// TODO: CLAWKER_DEBUG env → stderr ConsoleWriter sink so devs
						// see logs live.
						log.Warn().Interface("panic", r).Msg("notification goroutine panicked")
					}
				}
				notifyChan <- n
			}()
			n.release = runUpdateCheck(notifyCtx, f, buildVersion)
			n.gained = runChangelogCheck(notifyCtx, f, buildVersion)
		}()
	}

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
	signalCtx, signalStop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer signalStop()
	rootCmd.SetContext(signalCtx)

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

	cmd, err := rootCmd.ExecuteC()

	// gh CLI pattern: cancel the background checks now, before draining their
	// channel. Cancelling aborts any in-flight HTTP so the drain returns promptly
	// instead of blocking up to the 30s HTTP client timeout — most importantly
	// after a Ctrl+C, where the command was already interrupted and the user wants
	// out. A check that had not finished contributes its zero value and is simply
	// retried next run; its update cache / changelog cursor only advances on a
	// completed check. The deferred cancel above remains for the early-return
	// paths that never reach here (e.g. root command creation failing).
	notifyCancel()
	// Cancel the logger context now that the command has returned, so the
	// deferred Close above unwinds its OTEL shutdown immediately instead of
	// blocking exit on a final export.
	loggerCancel()

	// drainNotifications blocks on the channel (only when the goroutine was
	// launched) and renders both notifications. printUpdateNotification and
	// printChangelogTeaser each self-guard on nil/empty, so calling them
	// unconditionally on a suppressed run is a safe no-op.
	drainNotifications := func() {
		var n notifications
		if !suppressed {
			n = <-notifyChan
		}
		printUpdateNotification(f.IOStreams, n.release)
		printChangelogTeaser(f.IOStreams, n.gained)
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
type notifications struct {
	release *update.ReleaseInfo
	gained  []changelog.Entry
}

// runUpdateCheck runs the update check and reports the newer release, or nil
// when there is nothing to report. It carries its own recover so a panic here
// cannot skip the changelog check that follows it on the same goroutine, and
// logs its own failures — a background check never surfaces an error to the
// user.
//
// CheckForUpdate validates currentVersion as semver (a non-release "DEV" build
// is not parseable semver and returns an error before any fetch), reads the
// freshness gate from the state facade, and persists the result there itself
// (RecordUpdateCheck). It returns (nil, nil) when not newer or TTL-fresh; a
// non-nil release only when a newer release exists. A non-nil error may
// accompany a nil release — log it, report nothing.
func runUpdateCheck(ctx context.Context, f *cmdutil.Factory, currentVersion string) *update.ReleaseInfo {
	// A recovered panic returns the zero value: the results are unnamed, so
	// nothing was assigned to them when the stack unwound.
	defer func() {
		if r := recover(); r != nil {
			if log, logErr := f.Logger(); logErr == nil {
				log.Warn().Interface("panic", r).Msg("update check panicked")
			}
		}
	}()
	rel, err := checkForUpdate(ctx, f, currentVersion, consts.GitHubRepo)
	if err != nil {
		if log, logErr := f.Logger(); logErr == nil {
			log.Debug().Err(err).Msg("update check failed")
		}
	}
	return rel
}

// runChangelogCheck runs the changelog check and reports the entries gained
// since the cursor. Like runUpdateCheck it recovers and logs on its own, so
// neither check can take down the other or the user's command.
func runChangelogCheck(ctx context.Context, f *cmdutil.Factory, currentVersion string) []changelog.Entry {
	// A recovered panic returns the zero value: the results are unnamed, so
	// nothing was assigned to them when the stack unwound.
	defer func() {
		if r := recover(); r != nil {
			if log, logErr := f.Logger(); logErr == nil {
				log.Warn().Interface("panic", r).Msg("changelog check panicked")
			}
		}
	}()
	// CheckForChanges returns gained entries even when only the cursor persist
	// fails, so the entries are kept regardless of the error.
	gained, err := checkForChanges(ctx, f, currentVersion)
	if err != nil {
		if log, logErr := f.Logger(); logErr == nil {
			log.Debug().Err(err).Msg("checking changelog for teaser")
		}
	}
	return gained
}

// checkForChanges resolves the HttpClient and CLIState nouns from the Factory and
// hands them to changelog.CheckForChanges. It is the changelog teaser's single
// entry from Main; a noun-resolution error aborts just this one background check
// and is logged by the caller, never surfaced.
func checkForChanges(ctx context.Context, f *cmdutil.Factory, currentVersion string) ([]changelog.Entry, error) {
	httpClient, err := f.HttpClient()
	if err != nil {
		return nil, err
	}
	cliState, err := f.CLIState()
	if err != nil {
		return nil, err
	}
	return changelog.CheckForChanges(ctx, httpClient, cliState, currentVersion)
}

// checkForUpdate resolves the HttpClient and CLIState nouns from the Factory and
// hands them to update.CheckForUpdate. It is the update notifier's single entry
// from Main; a noun-resolution error aborts just this one background check and is
// logged by the caller, never surfaced.
func checkForUpdate(ctx context.Context, f *cmdutil.Factory, currentVersion, repo string) (*update.ReleaseInfo, error) {
	httpClient, err := f.HttpClient()
	if err != nil {
		return nil, err
	}
	cliState, err := f.CLIState()
	if err != nil {
		return nil, err
	}
	return update.CheckForUpdate(ctx, httpClient, cliState, currentVersion, repo)
}

// notificationsSuppressed is the single gate for ALL clawker background
// notifications (the update notifier and the show-once changelog teaser). It is
// computed once in Main, up front: when true, the background goroutine that
// runs both checks is not launched, so the run does zero network I/O and no
// state writes.
func notificationsSuppressed(ios *iostreams.IOStreams) bool {
	// "CI" is the canonical cross-tool CI-detection env var (kept literal).
	return !ios.IsStderrTTY() || os.Getenv(consts.EnvNoNotifier) != "" || os.Getenv("CI") != ""
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
