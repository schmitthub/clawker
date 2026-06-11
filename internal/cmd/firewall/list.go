package firewall

import (
	"context"
	"fmt"
	"sort"
	"strings"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

// ListOptions holds the options for the firewall list command.
type ListOptions struct {
	IOStreams   *iostreams.IOStreams
	TUI         *tui.TUI
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
	Format      *cmdutil.FormatFlags
}

// ruleRow is the JSON/template-friendly representation of an egress rule.
type ruleRow struct {
	Domain      string    `json:"domain"`
	Proto       string    `json:"proto"`
	Port        string    `json:"port"`
	Action      string    `json:"action"`
	PathDefault string    `json:"path_default,omitempty"`
	Paths       []pathRow `json:"paths,omitempty"`
}

// pathRow is a single path-scoped rule entry under a domain.
type pathRow struct {
	Path    string   `json:"path"`
	Action  string   `json:"action"`
	Methods []string `json:"methods,omitempty"`
}

// displayPathDefault renders the catch-all action for `firewall list`. It
// defers the inference to the canonical adminv1.EffectivePathDefault, adding
// one presentation rule: a bare-domain rule (no path rules, no explicit
// default) returns "" so the table keeps suppressing the catch-all sub-row.
func displayPathDefault(r *adminv1.EgressRule) string {
	if r.GetPathDefault() == "" && len(r.GetPathRules()) == 0 {
		return ""
	}
	return adminv1.EffectivePathDefault(adminv1.EgressRuleFromProto(r))
}

// NewCmdList creates the firewall list command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams:   f.IOStreams,
		TUI:         f.TUI,
		AdminClient: f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List active egress rules",
		Long:    `List all currently active egress rules enforced by the firewall.`,
		Example: `  # List all rules
  clawker firewall list

  # Output as JSON
  clawker firewall ls --json

  # Custom Go template
  clawker firewall ls --format '{{.Domain}} {{.Proto}}'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)

	return cmd
}

func listRun(ctx context.Context, opts *ListOptions) error {
	ios := opts.IOStreams

	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	resp, err := callWithSpinner(ctx, ios, "Listing firewall rules...",
		func(rpcCtx context.Context) (*adminv1.FirewallListRulesResult, error) {
			return client.FirewallListRules(rpcCtx, &adminv1.FirewallListRulesRequest{})
		})
	if err != nil {
		return wrapRPCError("listing firewall rules", err)
	}

	rules := resp.GetRules()
	if len(rules) == 0 {
		fmt.Fprintln(ios.Out, "No active firewall rules.")
		return nil
	}

	rows := make([]ruleRow, 0, len(rules))
	for _, r := range rules {
		proto := r.GetProto()
		if proto == "" {
			proto = consts.EgressProtoHTTPS
		}
		action := r.GetAction()
		if action == "" {
			action = consts.EgressActionAllow
		}
		port := r.GetPort()

		var paths []pathRow
		if pr := r.GetPathRules(); len(pr) > 0 {
			paths = make([]pathRow, 0, len(pr))
			for _, p := range pr {
				pAction := p.GetAction()
				if pAction == "" {
					pAction = consts.EgressActionAllow
				}
				paths = append(paths, pathRow{Path: p.GetPath(), Action: pAction, Methods: p.GetMethods()})
			}
			sort.Slice(paths, func(i, j int) bool { return paths[i].Path < paths[j].Path })
		}

		rows = append(rows, ruleRow{
			Domain:      r.GetDst(),
			Proto:       proto,
			Port:        port,
			Action:      action,
			PathDefault: displayPathDefault(r),
			Paths:       paths,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Domain != rows[j].Domain {
			return rows[i].Domain < rows[j].Domain
		}
		if rows[i].Proto != rows[j].Proto {
			return rows[i].Proto < rows[j].Proto
		}
		return rows[i].Port < rows[j].Port
	})

	switch {
	case opts.Format.Quiet:
		for _, r := range rows {
			fmt.Fprintln(ios.Out, r.Domain)
		}
		return nil

	case opts.Format.IsJSON():
		return cmdutil.WriteJSON(ios.Out, rows)

	case opts.Format.IsTemplate():
		return cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny(rows))

	default:
		tp := opts.TUI.NewTable("DOMAIN", "ACTION", "PROTO", "PORT", "METHODS")
		for _, r := range rows {
			tp.AddRow(r.Domain, r.Action, r.Proto, r.Port, "")
			for _, p := range r.Paths {
				tp.AddRow("  "+p.Path, p.Action, "", "", strings.Join(p.Methods, ","))
			}
			if r.PathDefault != "" {
				tp.AddRow("  path default", r.PathDefault, "", "", "")
			}
		}
		return tp.Render()
	}
}
