package sockets

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/db"
)

type socketGrantStoreFunc func() (db.SocketGrantStore, error)

func grantIDCompletions(socketGrants socketGrantStoreFunc) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		store, err := socketGrants()
		if err != nil {
			cobra.CompDebugln("clawker sockets completion: open grant store: "+err.Error(), false)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		grants, err := store.ListSocketGrants()
		if err != nil {
			cobra.CompDebugln("clawker sockets completion: list grants: "+err.Error(), false)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		excluded := make(map[string]bool, len(args))
		for _, arg := range args {
			excluded[arg] = true
		}
		completions := make([]cobra.Completion, 0, len(grants))
		for _, grant := range grants {
			id := strconv.FormatInt(grant.ID, 10)
			if excluded[id] {
				continue
			}
			completions = append(completions, fmt.Sprintf(
				"%d\t%s → %s (%s)",
				grant.ID,
				grant.HarnessName,
				grant.HostPath,
				grant.Purpose,
			))
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

func harnessCompletions(socketGrants socketGrantStoreFunc) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		store, err := socketGrants()
		if err != nil {
			cobra.CompDebugln("clawker sockets completion: open grant store: "+err.Error(), false)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		grants, err := store.ListSocketGrants()
		if err != nil {
			cobra.CompDebugln("clawker sockets completion: list grants: "+err.Error(), false)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		seen := make(map[string]bool, len(grants))
		var completions []cobra.Completion
		for _, grant := range grants {
			if seen[grant.HarnessName] || !strings.HasPrefix(grant.HarnessName, toComplete) {
				continue
			}
			seen[grant.HarnessName] = true
			completions = append(completions, grant.HarnessName)
		}
		sort.Slice(completions, func(i, j int) bool { return completions[i] < completions[j] })
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}
