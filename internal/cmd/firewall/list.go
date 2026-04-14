package firewall

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
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
	Domain string `json:"domain"`
	Proto  string `json:"proto"`
	Port   string `json:"port"`
	Action string `json:"action"`
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

	resp, err := client.FirewallListRules(ctx, &adminv1.FirewallListRulesRequest{})
	if err != nil {
		return fmt.Errorf("listing firewall rules: %w", err)
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
			proto = "tls"
		}
		action := r.GetAction()
		if action == "" {
			action = "allow"
		}
		port := ""
		if r.GetPort() > 0 {
			port = strconv.FormatUint(uint64(r.GetPort()), 10)
		}
		rows = append(rows, ruleRow{
			Domain: r.GetDst(),
			Proto:  proto,
			Port:   port,
			Action: action,
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
		tp := opts.TUI.NewTable("DOMAIN", "PROTO", "PORT", "ACTION")
		for _, r := range rows {
			tp.AddRow(r.Domain, r.Proto, r.Port, r.Action)
		}
		return tp.Render()
	}
}
