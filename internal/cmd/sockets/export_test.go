package sockets

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/db"
)

// NewCmdListForTest exposes the list command with a store test seam.
func NewCmdListForTest(f *cmdutil.Factory, grants func() (db.SocketGrantStore, error)) *cobra.Command {
	return newCmdList(f, func(ctx context.Context, opts *ListOptions) error {
		opts.SocketGrants = socketGrantStoreFunc(grants)
		return listRun(ctx, opts)
	})
}

// NewCmdInfoForTest exposes the info command with a store test seam.
func NewCmdInfoForTest(f *cmdutil.Factory, grants func() (db.SocketGrantStore, error)) *cobra.Command {
	return newCmdInfo(f, func(ctx context.Context, opts *InfoOptions) error {
		opts.SocketGrants = socketGrantStoreFunc(grants)
		return infoRun(ctx, opts)
	})
}

// NewCmdRevokeForTest exposes the revoke command with a store test seam.
func NewCmdRevokeForTest(f *cmdutil.Factory, grants func() (db.SocketGrantStore, error)) *cobra.Command {
	return newCmdRevoke(f, func(ctx context.Context, opts *RevokeOptions) error {
		opts.SocketGrants = socketGrantStoreFunc(grants)
		return revokeRun(ctx, opts)
	})
}

// NewCmdPruneForTest exposes the prune command with a store test seam.
func NewCmdPruneForTest(f *cmdutil.Factory, grants func() (db.SocketGrantStore, error)) *cobra.Command {
	return newCmdPrune(f, func(ctx context.Context, opts *PruneOptions) error {
		opts.SocketGrants = socketGrantStoreFunc(grants)
		return pruneRun(ctx, opts)
	})
}

// GrantIDCompletionsForTest exposes grant ID completion.
func GrantIDCompletionsForTest(grants func() (db.SocketGrantStore, error)) cobra.CompletionFunc {
	return grantIDCompletions(socketGrantStoreFunc(grants))
}

// HarnessCompletionsForTest exposes harness name completion.
func HarnessCompletionsForTest(grants func() (db.SocketGrantStore, error)) cobra.CompletionFunc {
	return harnessCompletions(socketGrantStoreFunc(grants))
}
