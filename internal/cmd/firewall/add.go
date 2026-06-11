package firewall

import (
	"context"
	"fmt"
	"strings"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

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
	}

	cmd := &cobra.Command{
		Use:   "add <domain>",
		Short: "Add an egress rule",
		Long: `Add a domain to the firewall allow list. The rule takes effect immediately
via hot-reload — no container restart required.

Pass --path together with --action to add a path-scoped rule onto the domain
entry instead of (or alongside) the bare-domain allow. Path rules accumulate
across calls; a repeated --path with a different --action overwrites the
prior action for that path.

Pass --methods to narrow a path rule to a set of HTTP request methods (e.g.
GET,HEAD). The path rule's --action then applies only to those methods; other
methods fall through to later rules / the path default. Empty = all methods.
HTTP-family protos only (https/http/ws/wss).`,
		Example: `  # Allow HTTPS traffic to a domain
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
  clawker firewall add api.github.com --path /repos/ --action deny --methods POST,PUT,PATCH,DELETE`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Domain = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return addRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Proto, "proto", consts.EgressProtoHTTPS, "Protocol: https (default), http, ssh, tcp, or any opaque protocol name")
	cmd.Flags().StringVar(&opts.Port, "port", "", "Destination port: a single port (443) or an inclusive range (9000-9100); default: protocol-specific")
	cmd.Flags().StringVar(&opts.Path, "path", "", "URL path prefix for a path-scoped rule, matched as a prefix at request time (requires --action)")
	cmd.Flags().StringVar(&opts.Action, "action", "", "Action for the path rule: allow or deny (requires --path)")
	cmd.Flags().StringSliceVar(&opts.Methods, "methods", nil, "HTTP methods the path rule applies to (e.g. GET,HEAD); empty = all methods. Requires --path/--action; https/http/ws/wss only")
	cmd.MarkFlagsRequiredTogether("path", "action")

	return cmd
}

func addRun(ctx context.Context, opts *AddOptions) error {
	ios := opts.IOStreams

	// Rewrite the legacy `tls` alias to `https` before validation (mirrors
	// NormalizeRule server-side) so downstream sees only real proto tokens — the
	// proto gate and the stored rule both get `https`, not the L5/6 `tls` non-token.
	if strings.EqualFold(opts.Proto, consts.EgressProtoLegacyTLS) {
		opts.Proto = consts.EgressProtoHTTPS
	}

	if err := validatePortFlag(opts.Port); err != nil {
		return err
	}
	if opts.Path != "" {
		if opts.Action != consts.EgressActionAllow && opts.Action != consts.EgressActionDeny {
			return cmdutil.FlagErrorf("--action must be \"allow\" or \"deny\", got %q", opts.Action)
		}
		// Path and method rules need an L7 HTTP request line to enforce against.
		// On opaque protos (ssh/tcp/udp) they are silently ignored at generation,
		// so reject here rather than accept a rule that can never take effect.
		if !adminv1.IsHTTPFamilyProto(opts.Proto) {
			return cmdutil.FlagErrorf("--path/--methods are only supported on https/http/ws/wss, not %q", opts.Proto)
		}
	}
	// --methods narrows a path rule, so it needs one. MarkFlagsRequiredTogether
	// can't express the one-way dependency (path/action are valid without methods).
	if len(opts.Methods) > 0 && opts.Path == "" {
		return cmdutil.FlagErrorf("--methods requires --path and --action")
	}

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	rule := &adminv1.EgressRule{
		Dst:    opts.Domain,
		Proto:  opts.Proto,
		Port:   opts.Port,
		Action: consts.EgressActionAllow,
	}
	if opts.Path != "" {
		rule.PathRules = []*adminv1.PathRule{{Path: opts.Path, Action: opts.Action, Methods: opts.Methods}}
	}

	resp, err := callWithSpinner(ctx, ios, fmt.Sprintf("Adding firewall rule %s...", opts.Domain),
		func(rpcCtx context.Context) (*adminv1.FirewallAddRulesResult, error) {
			return client.FirewallAddRules(rpcCtx, &adminv1.FirewallAddRulesRequest{Rules: []*adminv1.EgressRule{rule}})
		})
	if err != nil {
		return wrapRPCError("adding firewall rule", err)
	}

	cs := ios.ColorScheme()
	statuses := resp.GetStatuses()
	if len(statuses) != 1 {
		return fmt.Errorf("adding firewall rule: server returned %d statuses, want 1", len(statuses))
	}
	switch statuses[0] {
	case adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED:
		if opts.Path != "" {
			fmt.Fprintf(ios.Out, "%s Added path rule %s (%s) on %s\n", cs.SuccessIcon(), opts.Path, opts.Action, opts.Domain)
		} else {
			fmt.Fprintf(ios.Out, "%s Added rule: %s (%s)\n", cs.SuccessIcon(), opts.Domain, opts.Proto)
		}
		printStackRestartedNote(ios, resp.GetStackRestarted(), "rule persisted")
	case adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED:
		if opts.Path != "" {
			fmt.Fprintf(ios.Out, "%s Updated path rule %s (%s) on %s\n", cs.SuccessIcon(), opts.Path, opts.Action, opts.Domain)
		} else {
			fmt.Fprintf(ios.Out, "%s Updated rule: %s (%s)\n", cs.SuccessIcon(), opts.Domain, opts.Proto)
		}
		printStackRestartedNote(ios, resp.GetStackRestarted(), "rule persisted")
	case adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED:
		if opts.Path != "" {
			fmt.Fprintf(ios.Out, "%s Path rule already exists: %s (%s) on %s — no change\n", cs.InfoIcon(), opts.Path, opts.Action, opts.Domain)
		} else {
			fmt.Fprintf(ios.Out, "%s Rule already exists: %s (%s) — no change\n", cs.InfoIcon(), opts.Domain, opts.Proto)
		}
	default:
		return fmt.Errorf("adding firewall rule: server returned unknown status %v", statuses[0])
	}

	return nil
}
