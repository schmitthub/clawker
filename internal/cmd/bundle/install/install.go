// Package install provides the `clawker bundle install` command.
package install

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// githubHost is the default host an owner/repo shorthand expands against — the
// shorthand is CLI-only sugar that expands to a full clone URL before the yaml
// entry is written (the persisted schema stays git-generic).
const githubHost = "https://github.com/"

// InstallOptions holds the options for the bundle install command.
type InstallOptions struct {
	IOStreams     *iostreams.IOStreams
	Config        func() (config.Config, error)
	BundleManager func() (*bundle.Manager, error)

	Source     string
	Ref        string
	SHA        string
	Subdir     string
	AutoUpdate bool

	User    bool
	Project bool
	Local   bool
}

// NewCmdInstall creates the bundle install command.
func NewCmdInstall(f *cmdutil.Factory, runF func(context.Context, *InstallOptions) error) *cobra.Command {
	opts := &InstallOptions{
		IOStreams:     f.IOStreams,
		Config:        f.Config,
		BundleManager: f.BundleManager,
		Source:        "",
		Ref:           "",
		SHA:           "",
		Subdir:        "",
		AutoUpdate:    false,
		User:          false,
		Project:       false,
		Local:         false,
	}

	cmd := &cobra.Command{
		Use:   "install [source]",
		Short: "Declare a bundle source and fetch its content",
		Long: `Declares a bundle source in a clawker.yaml 'bundles:' entry and fetches its
content into the host cache.

The source is a git clone URL (https or ssh), an owner/repo GitHub shorthand
(expanded to a URL before writing), or a local directory path (loaded in place,
the dev loop). A remote source may pin --ref or --sha; unpinned tracks the
repository's default branch. With no source, declared-but-uncached bundles are
fetched.

By default the entry is written to the user config-dir clawker.yaml; --project
writes the project clawker.yaml and --local the uncommitted project override.`,
		Example: `  # Install from a git URL pinned to a tag
  clawker bundle install https://github.com/acme/tools.git --ref v1.2.0

  # Unpinned — tracks the repository's default branch
  clawker bundle install https://github.com/acme/extras.git

  # GitHub owner/repo shorthand, into the project config
  clawker bundle install acme/tools --sha <40-hex> --project

  # A local directory (dev loop)
  clawker bundle install ./vendor/my-bundle --project`,
		Args: cmdutil.RequiresMaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Source = args[0]
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return installRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Ref, "ref", "", "Branch or tag to fetch from a remote source")
	cmd.Flags().StringVar(&opts.SHA, "sha", "", "Full 40-character commit SHA to pin a remote source")
	cmd.Flags().StringVar(&opts.Subdir, "subdir", "", "Repository subdirectory holding the bundle (monorepo)")
	cmd.Flags().BoolVar(&opts.AutoUpdate, "auto-update", false, "Refetch this bundle when its source version changes")
	cmd.Flags().BoolVar(&opts.User, "user", false, "Write to the user config-dir clawker.yaml (default)")
	cmd.Flags().BoolVar(&opts.Project, "project", false, "Write to the project clawker.yaml")
	cmd.Flags().BoolVar(&opts.Local, "local", false, "Write to the uncommitted project clawker.local.yaml")

	return cmd
}

func installRun(ctx context.Context, opts *InstallOptions) error {
	ios := opts.IOStreams

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if opts.Source == "" {
		return installDeclared(ctx, opts)
	}

	targetPath, err := resolveTarget(cfg, opts)
	if err != nil {
		return err
	}

	src, err := prepareSource(opts, targetPath)
	if err != nil {
		return err
	}

	updated, alreadyDeclared := planSource(cfg, targetPath, src)
	added := !alreadyDeclared
	if added {
		store := cfg.ProjectStore()
		if setErr := store.Set("bundles", updated); setErr != nil {
			return fmt.Errorf("setting bundles: %w", setErr)
		}
		if writeErr := store.WriteTo(targetPath); writeErr != nil {
			return fmt.Errorf("writing %s: %w", targetPath, writeErr)
		}
	}
	if added {
		fmt.Fprintf(ios.Out, "Declared bundle source in %s\n", targetPath)
	} else {
		fmt.Fprintf(ios.ErrOut, "%s bundle source already declared in %s\n", ios.ColorScheme().InfoIcon(), targetPath)
	}

	prefetch(ctx, opts, src)
	return nil
}

// installDeclared handles the no-source form: fetch every declared-but-uncached
// remote bundle into the host cache.
func installDeclared(ctx context.Context, opts *InstallOptions) error {
	ios := opts.IOStreams
	mgr, err := opts.BundleManager()
	if err != nil {
		return fmt.Errorf("loading bundle manager: %w", err)
	}
	installed, err := mgr.InstallDeclared(ctx)
	for _, id := range installed {
		fmt.Fprintf(ios.Out, "Installed %s\n", id)
	}
	if err != nil {
		return fmt.Errorf("installing declared bundles: %w", err)
	}
	if len(installed) == 0 {
		fmt.Fprintf(ios.ErrOut, "%s all declared bundles are already installed\n", ios.ColorScheme().InfoIcon())
	}
	// Installing is the moment a declaration edit strands its old entry —
	// reconcile the touched identities' cache siblings against the roots.
	printGCWarnings(ios, mgr.AutoGC(ctx, installed...))
	return nil
}

// printGCWarnings writes the cache-maintenance advisories (removed stale
// entries, skipped maintenance) to stderr.
func printGCWarnings(ios *iostreams.IOStreams, warnings []bundle.Warning) {
	for _, w := range warnings {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", ios.ColorScheme().InfoIcon(), w.Message)
	}
}

// prefetch fetches the just-declared source into the cache. A fetch failure
// never undoes the successful declaration write — it is reported so the user
// knows to retry the fetch (the yaml entry stands).
func prefetch(ctx context.Context, opts *InstallOptions, src config.BundleSource) {
	ios := opts.IOStreams
	mgr, err := opts.BundleManager()
	if err != nil {
		fmt.Fprintf(ios.ErrOut, "%s loading bundle manager: %v\n", ios.ColorScheme().WarningIcon(), err)
		return
	}
	id, fetchErr := mgr.Install(ctx, src)
	if fetchErr != nil {
		fmt.Fprintf(ios.ErrOut, "%s declared, but fetch failed: %v\n", ios.ColorScheme().WarningIcon(), fetchErr)
		return
	}
	if !bundle.SourceFromConfig(src).IsLocal() {
		fmt.Fprintf(ios.Out, "Fetched %s into the cache\n", id)
		printGCWarnings(ios, mgr.AutoGC(ctx, id))
	}
}

// prepareSource classifies the source argument, re-anchors a local path to the
// target file's directory (a stored relative path resolves against its
// declaring file), and validates the result at the write front door.
func prepareSource(opts *InstallOptions, targetPath string) (config.BundleSource, error) {
	src, err := classifySource(opts)
	if err != nil {
		return config.BundleSource{}, err
	}
	if src.URL == "" {
		if src.Path, err = rewriteLocalPath(src.Path, targetPath); err != nil {
			return config.BundleSource{}, err
		}
	}
	if valErr := config.ValidateBundleSource(src); valErr != nil {
		return config.BundleSource{}, fmt.Errorf("invalid bundle source: %w", valErr)
	}
	return src, nil
}

// classifySource turns the source argument and flags into a typed BundleSource:
// a local directory path (loaded in place), a git clone URL, or an owner/repo
// GitHub shorthand expanded to a URL. ref/sha/subdir are meaningful only for a
// remote source.
func classifySource(opts *InstallOptions) (config.BundleSource, error) {
	arg := opts.Source
	switch {
	case isLocalPathArg(arg):
		if opts.Ref != "" || opts.SHA != "" || opts.Subdir != "" {
			return config.BundleSource{}, errors.New(
				"--ref/--sha/--subdir are not valid for a local path source")
		}
		return config.BundleSource{URL: "", Ref: "", SHA: "", Path: arg, AutoUpdate: false}, nil
	case isURLArg(arg):
		return remoteSource(arg, opts), nil
	case isOwnerRepoArg(arg):
		return remoteSource(githubHost+arg+".git", opts), nil
	default:
		return config.BundleSource{}, fmt.Errorf(
			"unrecognized bundle source %q — expected a git URL, an owner/repo shorthand, or a local path", arg)
	}
}

// remoteSource builds a remote BundleSource from a resolved URL and the flags.
func remoteSource(url string, opts *InstallOptions) config.BundleSource {
	return config.BundleSource{
		URL:        url,
		Ref:        opts.Ref,
		SHA:        opts.SHA,
		Path:       opts.Subdir,
		AutoUpdate: opts.AutoUpdate,
	}
}

// rewriteLocalPath re-anchors a local path argument (cwd-relative from the
// user's shell) to the config file it is written into: a stored relative path
// resolves against the declaring file's directory, so the entry is rewritten
// relative to that directory — committed project files stay portable. When no
// relative form exists (e.g. across volumes) the absolute path is written
// instead. ~ and $VAR spellings expand first.
func rewriteLocalPath(arg, targetPath string) (string, error) {
	expanded, err := config.ExpandHostPath(arg)
	if err != nil {
		return "", fmt.Errorf("expanding path %q: %w", arg, err)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolving path %q: %w", arg, err)
	}
	rel, relErr := filepath.Rel(filepath.Dir(targetPath), abs)
	if relErr != nil {
		return abs, nil //nolint:nilerr // an inexpressible relative form falls back to the absolute path by design
	}
	return rel, nil
}

// isLocalPathArg reports whether the argument is a local directory path — an
// explicit relative or absolute path spelling.
func isLocalPathArg(arg string) bool {
	return strings.HasPrefix(arg, "./") ||
		strings.HasPrefix(arg, "../") ||
		strings.HasPrefix(arg, "/") ||
		strings.HasPrefix(arg, "~")
}

// isURLArg reports whether the argument is a git clone URL (scheme form or scp
// ssh form).
func isURLArg(arg string) bool {
	return strings.Contains(arg, "://") || strings.HasPrefix(arg, "git@")
}

// isOwnerRepoArg reports whether the argument is a bare owner/repo shorthand —
// exactly one slash with non-empty halves, no whitespace. It assumes the URL
// and local-path forms were already rejected (classifySource checks those arms
// first).
func isOwnerRepoArg(arg string) bool {
	const ownerRepoSegments = 2
	parts := strings.Split(arg, "/")
	if len(parts) != ownerRepoSegments {
		return false
	}
	return parts[0] != "" && parts[1] != "" && !strings.ContainsAny(arg, " \t")
}
