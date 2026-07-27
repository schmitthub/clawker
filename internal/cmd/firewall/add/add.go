// Package add provides the firewall add command.
package add

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmd/firewall/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// Proto and path-action tokens the command validates against and stamps onto
// the outbound rule. `tls` is the legacy alias callers may still pass for
// `https`.
const (
	protoHTTPS  = "https"
	protoTLS    = "tls"
	actionAllow = "allow"
	actionDeny  = "deny"
)

const addLong = `Add a domain to the firewall allow list. The rule takes effect immediately
via hot-reload — no container restart required.

Pass --path together with --action to add a path-scoped rule onto the domain
entry instead of (or alongside) the bare-domain allow. Path rules accumulate
across calls; a repeated --path with a different --action overwrites the
prior action for that path.

Pass --methods to narrow a path rule to a set of HTTP request methods (e.g.
GET,HEAD). The path rule's --action then applies only to those methods; other
methods fall through to later rules / the path default. Empty = all methods.
HTTP-family protos only (https/http/ws/wss).

A --path is a literal prefix by default, so --path /repos/x also matches
/repos/x-evil. Prefix the path with ~ to match it as a regex instead, which is
anchored end-to-end for exact matching (e.g. ~/repos/(a|b)/? matches only those
two repos, with or without a trailing slash). Quote regex paths — the shell
expands ~/ and treats ( | ? as special.`

const addExample = `  # Allow HTTPS traffic to a domain
  clawker firewall add registry.npmjs.org

  # Allow SSH traffic on a custom port
  clawker firewall add git.example.com --proto ssh --port 22

  # Allow plain TCP traffic
  clawker firewall add api.example.com --proto tcp --port 8080

  # Add a path-scoped allow rule onto a domain entry
  clawker firewall add api.example.com --path /v1 --action allow

  # Make a host read-only: allow GET/HEAD on all paths, deny the rest
  clawker firewall add api.github.com --path / --action allow --methods GET,HEAD

  # Deny mutating methods on a path prefix (reads still fall through)
  clawker firewall add api.github.com --path /repos/ --action deny --methods POST,PUT,PATCH,DELETE

  # Allow only two repos exactly (regex, anchored) — blocks /repos/clawker-evil
  clawker firewall add api.github.com --path '~/repos/(clawker|anthropic)/?' --action allow`

// AddOptions holds the options for the firewall add command.
type AddOptions struct {
	IOStreams   *iostreams.IOStreams
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
	Domain      string
	Proto       string
	Port        string
	Path        string
	Action      string
	Methods     []string
}

// NewCmdAdd creates the firewall add command.
func NewCmdAdd(f *cmdutil.Factory, runF func(context.Context, *AddOptions) error) *cobra.Command {
	opts := &AddOptions{
		IOStreams:   f.IOStreams,
		AdminClient: f.AdminClient,
		Domain:      "",
		Proto:       "",
		Port:        "",
		Path:        "",
		Action:      "",
		Methods:     nil,
	}

	cmd := &cobra.Command{
		Use:     "add <domain>",
		Short:   "Add an egress rule",
		Long:    addLong,
		Example: addExample,
		Args:    cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Domain = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return addRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().
		StringVar(&opts.Proto, "proto", protoHTTPS, "Protocol: https (default), http, ssh, tcp, or any opaque protocol name")
	cmd.Flags().
		StringVar(&opts.Port, "port", "", "Destination port: a single port (443) or an inclusive range (9000-9100); default: protocol-specific")
	cmd.Flags().
		StringVar(&opts.Path, "path", "", "URL path for a path-scoped rule: a literal prefix (e.g. /v1), or an RE2 regex if prefixed with ~ for exact matching (e.g. ~/repos/(a|b)/?); requires --action")
	cmd.Flags().StringVar(&opts.Action, "action", "", "Action for the path rule: allow or deny (requires --path)")
	cmd.Flags().
		StringSliceVar(&opts.Methods, "methods", nil, "HTTP methods the path rule applies to (e.g. GET,HEAD); empty = all methods. Requires --path/--action; https/http/ws/wss only")
	cmd.MarkFlagsRequiredTogether("path", "action")

	return cmd
}

// validateAddFlags normalizes the legacy proto alias in place and rejects flag
// combinations that can never produce an enforceable rule. Returns a FlagError
// so Cobra renders usage alongside the message.
func validateAddFlags(opts *AddOptions) error {
	// Rewrite the legacy `tls` alias to `https` before validation (mirrors
	// NormalizeRule server-side) so downstream sees only real proto tokens — the
	// proto gate and the stored rule both get `https`, not the L5/6 `tls` non-token.
	if strings.EqualFold(opts.Proto, protoTLS) {
		opts.Proto = protoHTTPS
	}

	if err := shared.ValidatePortFlag(opts.Port); err != nil {
		//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
		return err
	}
	if opts.Path != "" {
		if opts.Action != actionAllow && opts.Action != actionDeny {
			//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
			return cmdutil.FlagErrorf("--action must be \"allow\" or \"deny\", got %q", opts.Action)
		}
		// Path and method rules need an L7 HTTP request line to enforce against.
		// On opaque protos (ssh/tcp/udp) they are silently ignored at generation,
		// so reject here rather than accept a rule that can never take effect.
		if !adminv1.IsHTTPFamilyProto(opts.Proto) {
			//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
			return cmdutil.FlagErrorf("--path/--methods are only supported on https/http/ws/wss, not %q", opts.Proto)
		}
	}
	// --methods narrows a path rule, so it needs one. MarkFlagsRequiredTogether
	// can't express the one-way dependency (path/action are valid without methods).
	if len(opts.Methods) > 0 && opts.Path == "" {
		//nolint:wrapcheck // FlagError reaches cobra typed for usage display, never wrapped (repo convention)
		return cmdutil.FlagErrorf("--methods requires --path and --action")
	}

	return nil
}

func addRun(ctx context.Context, opts *AddOptions) error {
	ios := opts.IOStreams

	if err := validateAddFlags(opts); err != nil {
		return err
	}

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	rule := &adminv1.EgressRule{
		Dst:       opts.Domain,
		Proto:     opts.Proto,
		Port:      opts.Port,
		Action:    actionAllow,
		PathRules: nil,
		// The CLI never sets a path default — an explicit one would flip the
		// whole entry into allowlist/blocklist mode behind the user's back.
		PathDefault: "",
		// Accepting an untrusted upstream cert is a yaml-only opt-in
		// (security.firewall.rules); the CLI deliberately offers no flag.
		InsecureSkipTlsVerify: false,
	}
	if opts.Path != "" {
		rule.PathRules = []*adminv1.PathRule{{Path: opts.Path, Action: opts.Action, Methods: opts.Methods}}
	}

	resp, err := shared.CallWithSpinner(ctx, ios, fmt.Sprintf("Adding firewall rule %s...", opts.Domain),
		func(rpcCtx context.Context) (*adminv1.FirewallAddRulesResult, error) {
			return client.FirewallAddRules(rpcCtx, &adminv1.FirewallAddRulesRequest{Rules: []*adminv1.EgressRule{rule}})
		})
	if err != nil {
		//nolint:wrapcheck // WrapRPCError already carries the header plus per-sentinel remediation
		return shared.WrapRPCError("adding firewall rule", err)
	}

	statuses := resp.GetStatuses()
	if len(statuses) != 1 {
		return fmt.Errorf("adding firewall rule: server returned %d statuses, want 1", len(statuses))
	}

	return printAddResult(ios, opts, statuses[0], resp.GetStackRestarted())
}

// printAddResult renders the per-status outcome line for the single rule the
// command sent, plus the stack-restarted note on the mutating statuses.
func printAddResult(
	ios *iostreams.IOStreams,
	opts *AddOptions,
	status adminv1.AddRuleStatus,
	stackRestarted bool,
) error {
	switch status {
	case adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED:
		printAddChangeLine(ios, opts, "Added")
		shared.PrintStackRestartedNote(ios, stackRestarted, "rule persisted")
	case adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED:
		printAddChangeLine(ios, opts, "Updated")
		shared.PrintStackRestartedNote(ios, stackRestarted, "rule persisted")
	case adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED:
		printAddUnchangedLine(ios, opts)
	case adminv1.AddRuleStatus_ADD_RULE_STATUS_UNSPECIFIED:
		return unknownAddStatusError(status)
	default:
		return unknownAddStatusError(status)
	}

	return nil
}

// printAddChangeLine renders the success line for a status that mutated the
// store. verb is the past-tense outcome ("Added" or "Updated").
func printAddChangeLine(ios *iostreams.IOStreams, opts *AddOptions, verb string) {
	cs := ios.ColorScheme()
	if opts.Path != "" {
		fmt.Fprintf(ios.Out, "%s %s path rule %s (%s) on %s\n",
			cs.SuccessIcon(), verb, opts.Path, opts.Action, opts.Domain)
		return
	}
	fmt.Fprintf(ios.Out, "%s %s rule: %s (%s)\n", cs.SuccessIcon(), verb, opts.Domain, opts.Proto)
}

// printAddUnchangedLine renders the no-op line for a merge that found the rule
// already present, so the operator never reads a "Added" line for a no-change.
func printAddUnchangedLine(ios *iostreams.IOStreams, opts *AddOptions) {
	cs := ios.ColorScheme()
	if opts.Path != "" {
		fmt.Fprintf(ios.Out, "%s Path rule already exists: %s (%s) on %s — no change\n",
			cs.InfoIcon(), opts.Path, opts.Action, opts.Domain)
		return
	}
	fmt.Fprintf(ios.Out, "%s Rule already exists: %s (%s) — no change\n", cs.InfoIcon(), opts.Domain, opts.Proto)
}

// unknownAddStatusError names a status the CLI has no rendering for. A server
// that returns one has drifted from this client; surfacing it beats printing a
// success line for an outcome we did not understand.
func unknownAddStatusError(status adminv1.AddRuleStatus) error {
	return fmt.Errorf("adding firewall rule: server returned unknown status %v", status)
}
