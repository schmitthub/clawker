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

	// Ensure logs and OTEL provider are flushed on exit.
	// TODO: give logger a Shutdown(ctx) for bounded flush (ctx as flush deadline,
	// not a replacement for Close).
	defer func() {
		if log, err := f.Logger(); err == nil {
			log.Close()
		}
	}()

	// Construct the CLI runtime-state facade directly — it is used only here in
	// Main (the background update check and the changelog teaser), so it is not a
	// Factory noun. A missing/unreadable state store degrades to a nil facade: the
	// update check proceeds with a zero "never checked" time and the changelog
	// teaser is a silent no-op. Errors are logged to the file log, never surfaced.
	var cliState state.StateStore
	if st, err := state.New(); err == nil {
		cliState = st
	} else if log, logErr := f.Logger(); logErr == nil {
		log.Debug().Err(err).Msg("reading CLI state")
	}

	// Single root context for the process; every cancellable child derives from
	// it directly (do NOT chain — signal.NotifyContext reassignment would clobber
	// the update/changelog cancels).
	ctx := context.Background()

	// notificationsSuppressed is the single gate for BOTH background notifications
	// (update notifier + changelog teaser). When it is true we launch NEITHER
	// goroutine, so a suppressed run does ZERO network I/O and no cursor persist —
	// a conscious, accepted behavior change. The env/CI opt-out now lives here in
	// the caller: internal/update and internal/changelog no longer enforce it.
	suppressed := notificationsSuppressed(f.IOStreams)

	// Background update check + changelog teaser, both gated by `suppressed`.
	// Pattern from gh CLI: goroutine + buffered channel + blocking read. Context
	// cancellation aborts in-flight I/O when the command finishes first. The
	// buffered(1) channels let each goroutine send and exit even if Main() returns
	// early (e.g. root command creation fails) without reading from the channel.
	updateCtx, updateCancel := context.WithCancel(ctx)
	defer updateCancel()
	changelogCtx, changelogCancel := context.WithCancel(ctx)
	defer changelogCancel()

	updateMessageChan := make(chan *update.ReleaseInfo, 1)
	changelogChan := make(chan []changelog.Entry, 1)

	if !suppressed {
		go func() {
			// Guarantee exactly one send on the buffered(1) channel on every path,
			// including a panic: the deferred func always runs, runs once, and is
			// the sole sender. A panic in this teaser goroutine must not crash the
			// user's command — recover, log, and report no update.
			var rel *update.ReleaseInfo
			defer func() {
				if r := recover(); r != nil {
					if log, logErr := f.Logger(); logErr == nil {
						// TODO: CLAWKER_DEBUG env → stderr ConsoleWriter sink so devs
						// see logs live.
						log.Warn().Interface("panic", r).Msg("update check goroutine panicked")
					}
					rel = nil
				}
				updateMessageChan <- rel
			}()
			var err error
			// CheckForUpdate reads the freshness gate from cliState and persists the
			// result there itself (RecordUpdateCheck). It returns (nil, nil) when not
			// newer or TTL-fresh; a non-nil rel only when a newer release exists. A
			// non-nil err may accompany a nil rel — log it, report nothing.
			rel, err = update.CheckForUpdate(updateCtx, cliState, buildVersion, consts.GitHubRepo)
			if err != nil {
				if log, logErr := f.Logger(); logErr == nil {
					log.Debug().Err(err).Msg("update check failed")
				}
			}
		}()

		go func() {
			// Guarantee exactly one send on the buffered(1) channel on every path,
			// including a panic: the deferred func always runs, runs once, and is the
			// sole sender. A panic in this teaser goroutine must not crash the user's
			// command — recover, log, and show nothing.
			var g []changelog.Entry
			defer func() {
				if r := recover(); r != nil {
					if log, logErr := f.Logger(); logErr == nil {
						// TODO: CLAWKER_DEBUG env → stderr ConsoleWriter sink so devs
						// see logs live.
						log.Warn().Interface("panic", r).Msg("changelog goroutine panicked")
					}
					g = nil
				}
				changelogChan <- g
			}()
			// build.Version is overwritten from build info at startup. On a non-release
			// build whose version is not a parseable semver there is no range to diff,
			// so show nothing — the parse failure is the signal, not an explicit
			// dev-build gate. semver.NewVersion tolerates a leading "v" via its regex.
			current, err := semver.NewVersion(buildVersion)
			if err != nil {
				if log, logErr := f.Logger(); logErr == nil {
					log.Debug().Err(err).Str("version", buildVersion).Msg("unparseable build version; skipping changelog teaser")
				}
				return
			}
			// CheckForChanges always advances the cursor; it is only called on a
			// non-suppressed run (gated above).
			entries, err := changelog.CheckForChanges(changelogCtx, cliState, current)
			if err != nil {
				if log, logErr := f.Logger(); logErr == nil {
					log.Debug().Err(err).Msg("checking changelog for teaser")
				}
				return
			}
			g = entries
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

	cmd, err := rootCmd.ExecuteC()

	// Don't cancel the update/changelog contexts here — the goroutines need to
	// complete so they can persist their state. The drain below waits for them,
	// and each I/O client has its own timeout. The deferred cancels handle cleanup
	// on exit.

	// drainNotifications blocks on both channels (only when goroutines were
	// launched) and renders both notifications. printUpdateNotification and
	// printChangelogTeaser each self-guard on nil/empty, so calling them
	// unconditionally on a suppressed run is a safe no-op.
	drainNotifications := func() {
		var updateInfo *update.ReleaseInfo
		var gained []changelog.Entry
		if !suppressed {
			updateInfo = <-updateMessageChan
			gained = <-changelogChan
		}
		printUpdateNotification(f.IOStreams, updateInfo)
		printChangelogTeaser(f.IOStreams, gained)
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

// notificationsSuppressed is the single gate for ALL clawker background
// notifications (the update notifier and the show-once changelog teaser). It is
// computed once in Main, up front: when true, neither background goroutine is
// launched, so the run does zero network I/O and no state writes.
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
	fmt.Fprintf(ios.ErrOut, "  %s\n", cs.Bold("curl -fsSL "+consts.RawGitHubBaseURL+"/"+consts.GitHubRepo+"/main/scripts/install.sh | bash"))
	fmt.Fprintf(ios.ErrOut, "%s\n", cs.Yellow(info.ReleaseURL))
	fmt.Fprintf(ios.ErrOut, "\n%s After upgrading, run %s in each project to apply security fixes and avoid breaking changes.\n",
		cs.WarningIcon(), cs.Bold("clawker build"))
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
