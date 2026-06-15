// Package changelog implements the `clawker changelog` command: it renders the
// curated changelog entries (fetched from CHANGELOG.md over the network by
// internal/changelog and parsed) to stdout with colored, emoji-tagged headers.
//
// No args     → the running binary's version entry (changelog.ForVersion).
// --version vX → a specific version's entry instead of the running binary's.
// --all       → the full curated history.
// --since vX  → entries after vX up to current (changelog.Between).
//
// Entries come from the curated CHANGELOG.md, fetched at runtime via the
// f.Changelog loader. The command force-refreshes (always tries the network),
// falling back to the on-disk cache when offline; a load failure degrades to a
// brief stderr note and a zero exit, so a network blip never fails the command.
//
// The show-once upgrade teaser lives in internal/clawker (Main), not here — this
// command is the explicit, on-demand surface the teaser points users to.
package changelog

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/changelog"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// ChangelogOptions carries the resolved dependencies and flags for a single
// `clawker changelog` invocation. Dependencies are injected from the Factory so
// the run function needs no test seams in its signature.
type ChangelogOptions struct {
	IO      *iostreams.IOStreams
	Loader  *changelog.Loader
	Version string

	All   bool
	Since string
	Flag  string // the --version flag (selects a specific entry; overrides Version)
}

// NewCmdChangelog creates the `changelog` command. Passing a non-nil runF
// overrides the default run function (the command-test injection seam).
func NewCmdChangelog(f *cmdutil.Factory, runF func(context.Context, *ChangelogOptions) error) *cobra.Command {
	opts := &ChangelogOptions{
		IO:      f.IOStreams,
		Version: f.Version,
	}

	cmd := &cobra.Command{
		Use:   "changelog",
		Short: "Show curated, user-facing changelog entries",
		Long: `Show the curated changelog — the handful of releases that changed the user surface.

With no arguments, shows the entry for the running version. Use --version to
select a specific release, --all for the full history, or --since to show
everything released after a given version.

The changelog is fetched from GitHub; when offline, the last cached copy is used.`,
		Example: `  # The current version's changelog entry
  clawker changelog

  # A specific version's entry
  clawker changelog --version v0.12.3

  # The full curated history
  clawker changelog --all

  # Everything since v0.10.0
  clawker changelog --since v0.10.0`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFlags(opts); err != nil {
				return err
			}
			// --version selects a specific entry, overriding the running binary's
			// version. It accepts a leading "v" (normalized by internal/changelog).
			if opts.Flag != "" {
				opts.Version = opts.Flag
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			loader, err := f.Changelog()
			if err != nil {
				return err
			}
			opts.Loader = loader
			return changelogRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Flag, "version", "", "Show the entry for a specific version (e.g. v0.12.3)")
	cmd.Flags().BoolVar(&opts.All, "all", false, "Show the full curated changelog history")
	cmd.Flags().StringVar(&opts.Since, "since", "", "Show entries released after the given version (e.g. v0.10.0)")

	return cmd
}

// validateFlags rejects unsupported flag combinations. --all and --since are
// mutually exclusive.
func validateFlags(opts *ChangelogOptions) error {
	if opts.All && opts.Since != "" {
		return cmdutil.FlagErrorf("--all and --since are mutually exclusive")
	}
	return nil
}

// changelogRun loads the curated entries (force-refresh: always try the network,
// fall back to cache when offline) and renders the requested selection to
// stdout. A load failure degrades to a brief stderr note and a nil error — a
// network blip should not fail `clawker changelog`.
func changelogRun(ctx context.Context, opts *ChangelogOptions) error {
	io := opts.IO
	cs := io.ColorScheme()

	entries, err := opts.Loader.Load(ctx, true)
	if err != nil {
		fmt.Fprintf(io.ErrOut, "%s could not load changelog\n", cs.WarningIcon())
		//nolint:nilerr // intentional degrade: a network/cache failure surfaces a stderr note, never fails `clawker changelog`
		return nil
	}

	selected := selectEntries(opts, entries)
	if len(selected) == 0 {
		fmt.Fprintf(io.ErrOut, "%s No curated changelog entries to show.\n", cs.InfoIcon())
		return nil
	}

	for i, e := range selected {
		if i > 0 {
			fmt.Fprintln(io.Out)
		}
		renderEntry(io, cs, e)
	}
	return nil
}

// selectEntries maps the flag combination to the changelog query over the
// already-loaded entries. --all and --since are validated as mutually exclusive
// by the caller.
func selectEntries(opts *ChangelogOptions, entries []changelog.Entry) []changelog.Entry {
	switch {
	case opts.All:
		return entries
	case opts.Since != "":
		return changelog.Between(entries, opts.Since, opts.Version)
	default:
		if entry, ok := changelog.ForVersion(entries, opts.Version); ok {
			return []changelog.Entry{entry}
		}
		return nil
	}
}

// renderEntry prints a single changelog entry: a tagged version header followed
// by the verbatim markdown body and an optional docs link.
func renderEntry(io *iostreams.IOStreams, cs *iostreams.ColorScheme, e changelog.Entry) {
	header := fmt.Sprintf("%s v%s", tagBadge(cs, e.Tag), e.Version)
	if e.Date != "" {
		header += cs.Muted(" - " + e.Date)
	}
	fmt.Fprintf(io.Out, "%s\n", cs.Bold(header))

	if body := strings.TrimRight(e.Body, "\n"); body != "" {
		fmt.Fprintf(io.Out, "%s\n", body)
	}
	if e.Docs != "" {
		fmt.Fprintf(io.Out, "%s %s\n", cs.Muted("Docs:"), cs.Cyan(e.Docs))
	}
}

// tagBadge maps a changelog tag to a colored, emoji-prefixed badge for the
// entry header. An unrecognized tag falls back to a neutral badge.
func tagBadge(cs *iostreams.ColorScheme, tag string) string {
	switch tag {
	case changelog.TagFeature:
		return cs.Success(tagEmojiFeature + " feature")
	case changelog.TagFix:
		return cs.Info(tagEmojiFix + " fix")
	case changelog.TagBreaking:
		return cs.Error(tagEmojiBreaking + " breaking")
	case changelog.TagPerf:
		return cs.Warning(tagEmojiPerf + " perf")
	case changelog.TagChanged:
		return cs.Primary(tagEmojiChanged + " changed")
	default:
		return cs.Muted(tagEmojiDefault + " " + tag)
	}
}
