// Package sockets provides commands that manage host socket bridge grants.
package sockets

import (
	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/db"
)

// NewCmdSockets creates the socket grant management command group.
func NewCmdSockets(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sockets <command>",
		Short: "Manage host socket bridge grants",
		Long: `List, inspect, revoke, and prune approvals and denials for host
socket bridges that harnesses request. Socket access is granted only during
container start; this command does not add grants.`,
	}

	cmd.AddCommand(
		newCmdList(f, nil),
		newCmdInfo(f, nil),
		newCmdRevoke(f, nil),
		newCmdPrune(f, nil),
	)
	return cmd
}

func socketGrants(f *cmdutil.Factory) socketGrantStoreFunc {
	return func() (db.SocketGrantStore, error) {
		database, err := f.DB()
		if err != nil {
			return nil, err
		}
		return db.NewSocketGrantStore(database), nil
	}
}
