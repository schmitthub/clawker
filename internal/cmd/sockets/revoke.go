package sockets

import (
	"context"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
)

const runningBridgeNote = "Running containers keep their socket bridges until they stop or restart."

// RevokeOptions holds dependencies and selectors for the revoke command.
type RevokeOptions struct {
	IOStreams    *iostreams.IOStreams
	SocketGrants socketGrantStoreFunc
	Prompter     func() *prompter.Prompter
	ID           int64
	Harness      string
	All          bool
	Yes          bool
}

func newCmdRevoke(f *cmdutil.Factory, runF func(context.Context, *RevokeOptions) error) *cobra.Command {
	opts := &RevokeOptions{
		IOStreams:    f.IOStreams,
		SocketGrants: socketGrants(f),
		Prompter:     f.Prompter,
		ID:           0,
		Harness:      "",
		All:          false,
		Yes:          false,
	}
	cmd := &cobra.Command{
		Use:   "revoke <id> | --harness <name> | --all",
		Short: "Revoke stored socket bridge grants",
		Long: `Delete one stored grant, all grants for a harness name, or all socket
grant rows. Both approvals and denials are deleted. A deleted grant can cause a
new authorization prompt on the next container start.`,
		Example: `  # Revoke one grant
  clawker sockets revoke 7

  # Revoke all grants for each harness named acme
  clawker sockets revoke --harness acme

  # Revoke all socket grants without a prompt
  clawker sockets revoke --all --yes`,
		Args: func(_ *cobra.Command, args []string) error {
			return validateRevokeSelectors(args, opts)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				id, err := parseGrantID(args[0])
				if err != nil {
					return err
				}
				opts.ID = id
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return revokeRun(cmd.Context(), opts)
		},
	}
	cmd.ValidArgsFunction = grantIDCompletions(opts.SocketGrants)
	cmd.Flags().StringVar(&opts.Harness, "harness", "", "Revoke all grants for this harness name")
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Revoke all socket grants")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Do not prompt for confirmation")
	cmd.RegisterFlagCompletionFunc("harness", harnessCompletions(opts.SocketGrants)) //nolint:errcheck,gosec // the flag is defined above
	return cmd
}

func validateRevokeSelectors(args []string, opts *RevokeOptions) error {
	if len(args) > 1 {
		return cmdutil.FlagErrorf("revoke accepts one grant ID")
	}
	selectors := len(args)
	if opts.Harness != "" {
		selectors++
	}
	if opts.All {
		selectors++
	}
	if selectors != 1 {
		return cmdutil.FlagErrorf("select exactly one grant ID, --harness, or --all")
	}
	return nil
}

func revokeRun(_ context.Context, opts *RevokeOptions) error {
	store, err := opts.SocketGrants()
	if err != nil {
		return fmt.Errorf("revoke socket grants: open database: %w", err)
	}
	if opts.ID != 0 {
		if err := store.RevokeSocket(opts.ID); err != nil {
			return fmt.Errorf("revoke socket grant %d: %w", opts.ID, err)
		}
		return printRevokeResult(opts)
	}
	if opts.Harness != "" {
		grants, err := store.ListSocketGrants()
		if err != nil {
			return fmt.Errorf("list grants for harness %q: %w", opts.Harness, err)
		}
		for _, principal := range distinctHarnessPrincipals(grants, opts.Harness) {
			if err := store.RevokeHarnessSockets(principal); err != nil {
				return fmt.Errorf("revoke socket grants for harness %q: %w", opts.Harness, err)
			}
		}
		return printRevokeResult(opts)
	}
	confirmed, err := confirmAllRevoke(opts)
	if err != nil {
		return err
	}
	if !confirmed {
		if _, err := fmt.Fprintln(opts.IOStreams.ErrOut, "Aborted."); err != nil {
			return fmt.Errorf("write socket grant revoke result: %w", err)
		}
		return nil
	}
	if err := store.RevokeAllSockets(); err != nil {
		return fmt.Errorf("revoke all socket grants: %w", err)
	}
	return printRevokeResult(opts)
}

func distinctHarnessPrincipals(grants []db.SocketGrant, harness string) []string {
	seen := make(map[string]bool, len(grants))
	var principals []string
	for _, grant := range grants {
		if grant.HarnessName != harness || seen[grant.HarnessPath] {
			continue
		}
		seen[grant.HarnessPath] = true
		principals = append(principals, grant.HarnessPath)
	}
	sort.Strings(principals)
	return principals
}

func confirmAllRevoke(opts *RevokeOptions) (bool, error) {
	if opts.Yes {
		return true, nil
	}
	if !opts.IOStreams.CanPrompt() {
		return false, cmdutil.FlagErrorf("--yes is required to revoke all grants in a non-interactive session")
	}
	warning := opts.IOStreams.ColorScheme().WarningIcon() + " Revoke all stored socket grants?"
	confirmed, err := opts.Prompter().Confirm(warning, false)
	if err != nil {
		return false, fmt.Errorf("confirm revoke of all socket grants: %w", err)
	}
	return confirmed, nil
}

func printRevokeResult(opts *RevokeOptions) error {
	cs := opts.IOStreams.ColorScheme()
	if _, err := fmt.Fprintf(opts.IOStreams.Out, "%s Socket grants revoked. %s\n", cs.SuccessIcon(), runningBridgeNote); err != nil {
		return fmt.Errorf("write socket grant revoke result: %w", err)
	}
	return nil
}
