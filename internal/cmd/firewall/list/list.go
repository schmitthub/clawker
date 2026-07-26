// Package list provides the firewall list command.
package list

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmd/firewall/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// ListOptions holds the options for the firewall list command.
type ListOptions struct {
	IOStreams   *iostreams.IOStreams
	TUI         *tui.TUI
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
	Format      *cmdutil.FormatFlags
}

// ruleRow is the JSON/template-friendly representation of an egress rule.
//
//nolint:tagliatelle // snake_case keys are the published `firewall list --json` contract
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
		Format:      nil,
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

	resp, err := shared.CallWithSpinner(ctx, ios, "Listing firewall rules...",
		func(rpcCtx context.Context) (*adminv1.FirewallListRulesResult, error) {
			return client.FirewallListRules(rpcCtx, &adminv1.FirewallListRulesRequest{})
		})
	if err != nil {
		//nolint:wrapcheck // WrapRPCError already wraps err with the header and remediation hints
		return shared.WrapRPCError("listing firewall rules", err)
	}

	rules := resp.GetRules()
	if len(rules) == 0 {
		fmt.Fprintln(ios.Out, "No active firewall rules.")
		return nil
	}

	return renderRules(opts, buildRuleRows(rules))
}

// buildRuleRows converts the wire rules into display rows — filling the
// protocol/action defaults the server leaves implicit — sorted by
// (domain, proto, port) so output is stable across calls.
func buildRuleRows(rules []*adminv1.EgressRule) []ruleRow {
	rows := make([]ruleRow, 0, len(rules))
	for _, r := range rules {
		proto := r.GetProto()
		if proto == "" {
			proto = config.EgressProtoHTTPS
		}
		action := r.GetAction()
		if action == "" {
			action = config.EgressActionAllow
		}

		rows = append(rows, ruleRow{
			Domain:      r.GetDst(),
			Proto:       proto,
			Port:        r.GetPort(),
			Action:      action,
			PathDefault: displayPathDefault(r),
			Paths:       buildPathRows(r.GetPathRules()),
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

	return rows
}

// buildPathRows converts a rule's path-scoped entries into display sub-rows
// sorted by path string. Returns nil for a rule with no path rules so the
// JSON contract keeps omitting the key.
func buildPathRows(pr []*adminv1.PathRule) []pathRow {
	if len(pr) == 0 {
		return nil
	}
	paths := make([]pathRow, 0, len(pr))
	for _, p := range pr {
		pAction := p.GetAction()
		if pAction == "" {
			pAction = config.EgressActionAllow
		}
		paths = append(paths, pathRow{Path: p.GetPath(), Action: pAction, Methods: p.GetMethods()})
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].Path < paths[j].Path })
	return paths
}

// renderRules dispatches the sorted rows to the requested output format.
func renderRules(opts *ListOptions, rows []ruleRow) error {
	ios := opts.IOStreams

	switch {
	case opts.Format.Quiet:
		for _, r := range rows {
			fmt.Fprintln(ios.Out, r.Domain)
		}
		return nil

	case opts.Format.IsJSON():
		if err := cmdutil.WriteJSON(ios.Out, rows); err != nil {
			return fmt.Errorf("writing json: %w", err)
		}
		return nil

	case opts.Format.IsTemplate():
		if err := cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny(rows)); err != nil {
			return fmt.Errorf("executing template: %w", err)
		}
		return nil

	default:
		return renderRulesTable(opts, rows)
	}
}

// renderRulesTable renders the default table: one row per rule, with the
// rule's path entries and catch-all default as indented sub-rows.
func renderRulesTable(opts *ListOptions, rows []ruleRow) error {
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
	if err := tp.Render(); err != nil {
		return fmt.Errorf("rendering table: %w", err)
	}
	return nil
}
