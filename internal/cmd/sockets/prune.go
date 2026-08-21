package sockets

import (
	"context"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
)

// PruneOptions holds dependencies and flags for the socket grant prune command.
type PruneOptions struct {
	IOStreams    *iostreams.IOStreams
	Config       func() (config.Config, error)
	SocketGrants socketGrantStoreFunc
	Prompter     func() *prompter.Prompter
	All          bool
	Yes          bool
}

func newCmdPrune(f *cmdutil.Factory, runF func(context.Context, *PruneOptions) error) *cobra.Command {
	opts := &PruneOptions{
		IOStreams:    f.IOStreams,
		Config:       f.Config,
		SocketGrants: socketGrants(f),
		Prompter:     f.Prompter,
		All:          false,
		Yes:          false,
	}
	cmd := &cobra.Command{ //nolint:exhaustruct_v5 // Cobra command fields use their documented zero-value defaults.
		Use:   "prune",
		Short: "Remove socket grants that no declaration uses",
		Long: `Resolve each stored harness and its current socket declarations. Remove
rows for harnesses that no longer resolve and rows for host sockets that the
resolved manifest no longer declares. With --all, remove every socket grant.`,
		Example: `  # Remove grants that no current harness socket uses
  clawker sockets prune

  # Remove every socket grant without a prompt
  clawker sockets prune --all --yes`,
		Args: cmdutil.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return pruneRun(cmd.Context(), opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Remove every socket grant")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Do not prompt for confirmation")
	return cmd
}

func pruneRun(_ context.Context, opts *PruneOptions) error {
	confirmed, confirmErr := confirmSocketPrune(opts)
	if confirmErr != nil {
		return confirmErr
	}
	if !confirmed {
		if _, writeErr := fmt.Fprintln(opts.IOStreams.ErrOut, "Aborted."); writeErr != nil {
			return fmt.Errorf("write socket grant prune result: %w", writeErr)
		}
		return nil
	}
	store, storeErr := opts.SocketGrants()
	if storeErr != nil {
		return fmt.Errorf("prune socket grants: open database: %w", storeErr)
	}
	if opts.All {
		if revokeErr := store.RevokeAllSockets(); revokeErr != nil {
			return fmt.Errorf("prune all socket grants: %w", revokeErr)
		}
		return printPruneResult(opts)
	}
	cfg, configErr := opts.Config()
	if configErr != nil {
		return fmt.Errorf("prune socket grants: load config: %w", configErr)
	}
	grants, listErr := store.ListSocketGrants()
	if listErr != nil {
		return fmt.Errorf("prune socket grants: list grants: %w", listErr)
	}
	if pruneErr := pruneHarnessGroups(cfg, store, grants); pruneErr != nil {
		return pruneErr
	}
	return printPruneResult(opts)
}

func pruneHarnessGroups(cfg config.Config, store db.SocketGrantStore, grants []db.SocketGrant) error {
	groups := groupGrantsByPrincipal(grants)
	principals := make([]string, 0, len(groups))
	for principal := range groups {
		principals = append(principals, principal)
	}
	sort.Strings(principals)
	for _, principal := range principals {
		rows := groups[principal]
		if pruneErr := pruneHarnessGroup(cfg, store, principal, rows[0].HarnessName); pruneErr != nil {
			return pruneErr
		}
	}
	return nil
}

func pruneHarnessGroup(cfg config.Config, store db.SocketGrantStore, principal, harnessName string) error {
	harness, loadErr := bundler.LoadHarness(cfg, harnessName)
	if loadErr != nil {
		if revokeErr := store.RevokeHarnessSockets(principal); revokeErr != nil {
			return fmt.Errorf("prune unresolved harness %q: %w", harnessName, revokeErr)
		}
		return nil
	}
	currentPrincipal, resolveErr := cmdutil.ResolveHostPath(harness.Provenance.Dir)
	if resolveErr != nil {
		return fmt.Errorf("prune harness %q: resolve principal: %w", harnessName, resolveErr)
	}
	if currentPrincipal != principal {
		if revokeErr := store.RevokeHarnessSockets(principal); revokeErr != nil {
			return fmt.Errorf("prune replaced harness %q: %w", harnessName, revokeErr)
		}
		return nil
	}
	declared := resolveDeclaredSocketPaths(harness.Manifest.Sockets)
	if pruneErr := store.PruneHarnessSockets(principal, declared); pruneErr != nil {
		return fmt.Errorf("prune harness %q grants: %w", harnessName, pruneErr)
	}
	return nil
}

func groupGrantsByPrincipal(grants []db.SocketGrant) map[string][]db.SocketGrant {
	groups := make(map[string][]db.SocketGrant)
	for _, grant := range grants {
		groups[grant.HarnessPath] = append(groups[grant.HarnessPath], grant)
	}
	return groups
}

func resolveDeclaredSocketPaths(declarations []config.HarnessSocket) []string {
	paths := make([]string, 0, len(declarations))
	for _, declaration := range declarations {
		path, err := cmdutil.ResolveHostPath(declaration.Source)
		if err != nil {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

func confirmSocketPrune(opts *PruneOptions) (bool, error) {
	if opts.Yes {
		return true, nil
	}
	if !opts.IOStreams.CanPrompt() {
		return false, fmt.Errorf(
			"validate socket grant prune flags: %w",
			cmdutil.FlagErrorf("--yes is required to prune grants in a non-interactive session"),
		)
	}
	message := opts.IOStreams.ColorScheme().
		WarningIcon() +
		" Remove socket grants that current harness declarations do not use?"
	if opts.All {
		message = opts.IOStreams.ColorScheme().WarningIcon() + " Remove all stored socket grants?"
	}
	confirmed, err := opts.Prompter().Confirm(message, false)
	if err != nil {
		return false, fmt.Errorf("confirm socket grant prune: %w", err)
	}
	return confirmed, nil
}

func printPruneResult(opts *PruneOptions) error {
	if _, err := fmt.Fprintf(
		opts.IOStreams.Out,
		"%s Socket grants pruned. %s\n",
		opts.IOStreams.ColorScheme().SuccessIcon(),
		runningBridgeNote,
	); err != nil {
		return fmt.Errorf("write socket grant prune result: %w", err)
	}
	return nil
}
