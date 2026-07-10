// Package install provides the `clawker bundle install` command.
package install

import (
	"context"
	"errors"
	"fmt"
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
the dev loop). With no source, declared-but-uncached bundles are fetched.

By default the entry is written to the user config-dir clawker.yaml; --project
writes the project clawker.yaml and --local the uncommitted project override.`,
		Example: `  # Install from a git URL pinned to a tag
  clawker bundle install https://github.com/acme/tools.git --ref v1.2.0

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

func installRun(_ context.Context, opts *InstallOptions) error {
	ios := opts.IOStreams

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if opts.Source == "" {
		return installDeclared(opts)
	}

	targetPath, configDirLayer, err := resolveTarget(cfg, opts)
	if err != nil {
		return err
	}

	src, err := classifySource(opts)
	if err != nil {
		return err
	}
	if valErr := config.ValidateBundleSource(src, configDirLayer); valErr != nil {
		return fmt.Errorf("invalid bundle source: %w", valErr)
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

	prefetch(opts, src)
	return nil
}

// installDeclared handles the no-source form (fetch declared-but-missing). The
// fetch subsystem is not yet wired, so it reports that rather than silently
// doing nothing.
func installDeclared(opts *InstallOptions) error {
	fmt.Fprintf(opts.IOStreams.ErrOut, "%s %v\n", opts.IOStreams.ColorScheme().InfoIcon(), bundle.ErrNotWired)
	return cmdutil.SilentError
}

// prefetch attempts to fetch the just-declared source. The fetch subsystem is
// stubbed (ErrNotWired); a fetch failure never undoes the successful
// declaration write — it is reported so the user knows content is not yet
// present.
func prefetch(opts *InstallOptions, src config.BundleSource) {
	mgr, err := opts.BundleManager()
	if err != nil {
		fmt.Fprintf(opts.IOStreams.ErrOut, "%s loading bundle manager: %v\n",
			opts.IOStreams.ColorScheme().WarningIcon(), err)
		return
	}
	if fetchErr := mgr.Install(src); fetchErr != nil {
		fmt.Fprintf(opts.IOStreams.ErrOut, "%s %v\n",
			opts.IOStreams.ColorScheme().InfoIcon(), fetchErr)
	}
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
		return remoteSource(githubHost+strings.TrimSuffix(arg, ".git")+".git", opts), nil
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
