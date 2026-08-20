package cmdutil

import "github.com/spf13/cobra"

// FlagApproveGrants is the shared host socket approval flag name.
const FlagApproveGrants = "approve-grants"

// AddApproveGrantsFlag adds the shared non-interactive socket approval flag.
func AddApproveGrantsFlag(cmd *cobra.Command, approve *bool) {
	cmd.Flags().BoolVar(
		approve,
		FlagApproveGrants,
		false,
		"Approve this start's declared host socket requests without prompting",
	)
}
