package firewall

import adminv1 "github.com/schmitthub/clawker/api/admin/v1"

// addStatus is the package-internal per-rule outcome of addRulesToStore.
// Maps 1:1 to adminv1.AddRuleStatus via toProtoAddStatus so handler logic
// stays free of generated proto enum types.
type addStatus uint8

const (
	addStatusUnspecified addStatus = iota
	addStatusAdded
	addStatusModified
	addStatusUnchanged
)

// removeStatus is the package-internal outcome of FirewallRemoveRule.
type removeStatus uint8

const (
	removeStatusUnspecified removeStatus = iota
	removeStatusRemoved
	removeStatusPathRemoved
	removeStatusNotFound
)

func toProtoAddStatus(s addStatus) adminv1.AddRuleStatus {
	switch s {
	case addStatusAdded:
		return adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED
	case addStatusModified:
		return adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED
	case addStatusUnchanged:
		return adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED
	default:
		return adminv1.AddRuleStatus_ADD_RULE_STATUS_UNSPECIFIED
	}
}

func toProtoAddStatuses(in []addStatus) []adminv1.AddRuleStatus {
	out := make([]adminv1.AddRuleStatus, len(in))
	for i, s := range in {
		out[i] = toProtoAddStatus(s)
	}
	return out
}

func toProtoRemoveStatus(s removeStatus) adminv1.RemoveRuleStatus {
	switch s {
	case removeStatusRemoved:
		return adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED
	case removeStatusPathRemoved:
		return adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_PATH_REMOVED
	case removeStatusNotFound:
		return adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND
	default:
		return adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_UNSPECIFIED
	}
}

// anyAddChange reports whether any per-rule addStatus represents a store
// mutation (Added or Modified). Pure Unchanged batches skip both
// store.Write and the stack reconcile.
func anyAddChange(statuses []addStatus) bool {
	for _, s := range statuses {
		if s == addStatusAdded || s == addStatusModified {
			return true
		}
	}
	return false
}
