package firewall

import adminv1 "github.com/schmitthub/clawker/api/admin/v1"

// AddStatus is the per-rule outcome of EgressRulesStore.AddRules. Exported
// because it appears in that interface's method set — the enum itself stays a
// firewall-domain type, mapped 1:1 onto adminv1.AddRuleStatus by
// toProtoAddStatus so handler logic stays free of generated proto enum types.
type AddStatus uint8

const (
	AddStatusUnspecified AddStatus = iota
	AddStatusAdded
	AddStatusModified
	AddStatusUnchanged
)

// removeStatus is the package-internal outcome of FirewallRemoveRule.
type removeStatus uint8

const (
	removeStatusUnspecified removeStatus = iota
	removeStatusRemoved
	removeStatusPathRemoved
	removeStatusNotFound
)

func toProtoAddStatus(s AddStatus) adminv1.AddRuleStatus {
	switch s {
	case AddStatusAdded:
		return adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED
	case AddStatusModified:
		return adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED
	case AddStatusUnchanged:
		return adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED
	default:
		return adminv1.AddRuleStatus_ADD_RULE_STATUS_UNSPECIFIED
	}
}

func toProtoAddStatuses(in []AddStatus) []adminv1.AddRuleStatus {
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

// anyAddChange reports whether any per-rule AddStatus represents a store
// mutation (Added or Modified). Pure Unchanged batches skip both the store
// write and the stack reconcile.
func anyAddChange(statuses []AddStatus) bool {
	for _, s := range statuses {
		if s == AddStatusAdded || s == AddStatusModified {
			return true
		}
	}
	return false
}
