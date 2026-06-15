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
	"time"

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
			log.Close()
		}
	}()

	// Read the CLI state store synchronously, before launching the update
	// goroutine, so (a) the update freshness gate sees the prior check and
	// (b) the changelog cursor bootstrap reads current_version before the
	// goroutine rewrites it (field-merge already prevents clobber, but the
	// synchronous read keeps the bootstrap deterministic). A missing/unreadable
	// state store degrades to a nil facade: the update check proceeds with a
	// zero "never checked" time and the changelog teaser is skipped. Errors are
	// logged to the file log, never surfaced.
	var cliState *state.State
	var lastCheckedAt time.Time
	var priorCurrentVersion string
	if st, err := f.State(); err == nil {
		cliState = st
		lastCheckedAt = st.LastCheckedAt()
		// Snapshot current_version BEFORE the update goroutine overwrites it
		// via RecordUpdateCheck. The changelog cursor bootstrap needs the
		// version persisted by the *previous* binary to detect a catch-up
		// upgrade; reading it live would race the goroutine and always lose
		// (the goroutine sets current_version to the running binary, so the
		// bootstrap would see prior == cur and skip the gained-entries teaser).
		priorCurrentVersion = st.CurrentVersion()
	} else if log, logErr := f.Logger(); logErr == nil {
		log.Debug().Err(err).Msg("reading CLI state")
	}

	// Start background update check with cancellable context.
	// Pattern from gh CLI: goroutine + buffered channel + blocking read.
	// Context cancellation aborts the HTTP request when the command finishes first.
	// Buffered(1) so the goroutine can send and exit even if Main() returns
	// early (e.g. root command creation fails) without reading from the channel.
	updateCtx, updateCancel := context.WithCancel(context.Background())
	defer updateCancel()
	updateMessageChan := make(chan *update.CheckResult, 1)
	go func() {
		// Guarantee exactly one send on the buffered(1) channel on every path,
		// including a panic: the deferred func always runs, runs once, and is
		// the sole sender. A panic in this teaser goroutine must not crash the
		// user's command — recover, log, and report no update.
		var rel *update.CheckResult
		defer func() {
			if r := recover(); r != nil {
				if log, logErr := f.Logger(); logErr == nil {
					log.Debug().Interface("panic", r).Msg("update check goroutine panicked")
				}
				rel = nil
			}
			updateMessageChan <- rel
		}()
		var err error
		rel, err = checkForUpdate(updateCtx, lastCheckedAt, buildVersion)
		if err != nil {
			if log, logErr := f.Logger(); logErr == nil {
				log.Debug().Err(err).Msg("update check failed")
			}
		}
		// Persist the check result (checked_at + versions) via the storage-
		// backed state facade — a field merge that never touches the changelog
		// cursor. Best-effort: a write failure is logged, not surfaced.
		if rel != nil {
			if st, stErr := f.State(); stErr == nil {
				if wErr := st.RecordUpdateCheck(time.Now(), rel.LatestVersion, rel.CurrentVersion); wErr != nil {
					if log, logErr := f.Logger(); logErr == nil {
						log.Debug().Err(wErr).Msg("recording update check result")
					}
				}
			}
		}
	}()

	// Load the curated changelog in the background (TTL-gated, NOT force-refresh)
	// so the show-once teaser never blocks the user's command on network I/O. The
	// loader fetches CHANGELOG.md when the cache is stale, otherwise reads the
	// cache. On any error the teaser shows nothing — entries is nil. Buffered(1)
	// so the goroutine can send and exit even if Main() returns early.
	changelogChan := make(chan []changelog.Entry, 1)
	go func() {
		// Guarantee exactly one send on the buffered(1) channel on every path,
		// including a panic: the deferred func always runs, runs once, and is
		// the sole sender. A panic in this teaser goroutine must not crash the
		// user's command — recover, log, and show nothing.
		var entries []changelog.Entry
		defer func() {
			if r := recover(); r != nil {
				if log, logErr := f.Logger(); logErr == nil {
					log.Debug().Interface("panic", r).Msg("changelog goroutine panicked")
				}
				entries = nil
			}
			changelogChan <- entries
		}()
		loader, err := f.Changelog()
		if err != nil {
			if log, logErr := f.Logger(); logErr == nil {
				log.Debug().Err(err).Msg("building changelog loader")
			}
			return
		}
		entries, err = loader.Load(updateCtx, false)
		if err != nil {
			if log, logErr := f.Logger(); logErr == nil {
				log.Debug().Err(err).Msg("loading changelog for teaser")
			}
			entries = nil
			return
		}
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
		maybeShowChangelog(f, cliState, <-changelogChan, buildVersion, priorCurrentVersion)

		var exitErr *cmdutil.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.Code
		}
		return 1
	}

	// Blocking read — goroutine always sends exactly once
	printUpdateNotification(f.IOStreams, <-updateMessageChan)
	maybeShowChangelog(f, cliState, <-changelogChan, buildVersion, priorCurrentVersion)

	return 0
}

// checkForUpdate wraps update.CheckForUpdate. update is pure (no persistence):
// the caller supplies the last-checked timestamp for freshness gating and
// persists the result itself via f.State.
func checkForUpdate(ctx context.Context, lastCheckedAt time.Time, currentVersion string) (*update.CheckResult, error) {
	return update.CheckForUpdate(ctx, lastCheckedAt, currentVersion, consts.GitHubRepo)
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

// maybeShowChangelog runs the cursor-driven show-once teaser after the command
// completes, surfacing curated changelog entries gained since the last shown
// version. It mirrors the update-notifier discipline (stderr, TTY-only,
// suppressible) and implements the design brief's cursor algorithm:
//
//	cursor = state.LastSeenChangelog
//	if cursor == "":                       # first changelog-aware run
//	    prior = priorCurrentVersion        # snapshot from Main() pre-goroutine
//	    if prior != "" and prior < cur: cursor = prior   # bootstrap catch-up
//	    else: SetLastSeenChangelog(cur); return          # seed cursor silently
//	if entries == nil: return              # background load failed / empty — retry next run
//	gained = changelog.Between(entries, cursor, cur)
//	if gained and not suppressed: show teaser; SetLastSeenChangelog(cur)
//	elif not gained:              SetLastSeenChangelog(cur)   # sync silently
//	# else suppressed: leave cursor — retry next interactive run
//
// priorCurrentVersion is the current_version snapshotted in Main() BEFORE the
// update goroutine launched; the bootstrap uses it (not a live read of
// st.CurrentVersion()) so the catch-up detection can't race the goroutine's
// RecordUpdateCheck write. The state facade may be nil (state store
// unavailable) — then this is a no-op. All persistence is best-effort: a write
// failure is logged, never surfaced.
func maybeShowChangelog(f *cmdutil.Factory, st *state.State, entries []changelog.Entry, currentVersion, priorCurrentVersion string) {
	if st == nil || currentVersion == consts.DevVersion {
		return
	}
	cur := strings.TrimPrefix(currentVersion, "v")
	ios := f.IOStreams
	suppressed := changelogSuppressed(ios)

	logWrite := func(err error) {
		if err == nil {
			return
		}
		if log, logErr := f.Logger(); logErr == nil {
			log.Debug().Err(err).Msg("persisting changelog cursor")
		}
	}

	cursor := st.LastSeenChangelog()
	if cursor == "" {
		// First run of a changelog-aware binary. Bootstrap the cursor from the
		// version the previous binary recorded (snapshotted in Main() before
		// the update goroutine could overwrite it) so an upgrade across a
		// changelog-blind binary still surfaces the gained entries.
		prior := strings.TrimPrefix(priorCurrentVersion, "v")
		if prior != "" && update.IsNewer(cur, prior) {
			cursor = prior
		} else {
			// No catch-up to show: seed the cursor at the current version
			// silently.
			logWrite(st.SetLastSeenChangelog(cur))
			return
		}
	}

	// entries were loaded in the background (TTL-gated network fetch + cache) in
	// Main(); a nil slice means the load failed or there was nothing to show —
	// the teaser stays silent and the cursor is left for the next run to retry.
	if entries == nil {
		return
	}

	gained := changelog.Between(entries, cursor, cur)
	switch {
	case len(gained) == 0:
		// Nothing new in the curated changelog — advance the cursor silently.
		logWrite(st.SetLastSeenChangelog(cur))
	case !suppressed:
		printChangelogTeaser(ios, gained)
		logWrite(st.SetLastSeenChangelog(cur))
	default:
		// Suppressed (non-TTY / CI / opt-out): leave the cursor so the teaser
		// retries on the next interactive run.
	}
}

// printChangelogTeaser lists the titles of the entries gained since the last
// shown version, surfacing each entry's docs URL as the "learn more" pointer.
func printChangelogTeaser(ios *iostreams.IOStreams, entries []changelog.Entry) {
	cs := ios.ColorScheme()
	fmt.Fprintf(ios.ErrOut, "\n%s What's new in clawker:\n", cs.Info("📣"))
	for _, e := range entries {
		title := e.Title
		if title == "" {
			title = e.Version
		}
		fmt.Fprintf(ios.ErrOut, "  %s %s %s\n", cs.Muted("•"), cs.Bold("v"+e.Version), title)
		if e.Docs != "" {
			fmt.Fprintf(ios.ErrOut, "    %s\n", cs.Muted("learn more: "+e.Docs))
		}
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
