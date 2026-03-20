package firewall

import (
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/storage"
)

// NewRulesStore creates a storage.Store[EgressRulesFile] for egress-rules.yaml.
// The store uses the firewall data subdirectory for file discovery.
func NewRulesStore(cfg config.Config) (*storage.Store[EgressRulesFile], error) {
	dataDir, err := cfg.FirewallDataSubdir()
	if err != nil {
		return nil, fmt.Errorf("firewall: resolving data dir: %w", err)
	}
	return storage.NewStore[EgressRulesFile](
		storage.WithFilenames(cfg.EgressRulesFileName()),
		storage.WithPaths(dataDir),
		storage.WithLock(), // Cross-process flock — multiple CLI/daemon instances share this file.
	)
}

// normalizeRule fills in missing fields before storage so rules are explicit and
// unambiguous. Empty proto defaults to "tls", empty action to "allow", and TLS
// rules with no port default to 443. Existing non-zero values are never overridden.
// Users should set full rules — this is a storage safety net, not a feature.
func normalizeRule(r config.EgressRule) config.EgressRule {
	if r.Proto == "" {
		r.Proto = "tls"
	}
	if r.Action == "" {
		r.Action = "allow"
	}
	if r.Port == 0 && strings.ToLower(r.Proto) == "tls" {
		r.Port = 443
	}
	return r
}

// ruleKey returns the dedup key for an egress rule: dst:proto:port.
func ruleKey(r config.EgressRule) string {
	return fmt.Sprintf("%s:%s:%d", r.Dst, r.Proto, r.Port)
}

// normalizeAndDedup normalizes all rules and removes duplicates.
// This handles legacy store files that contain port:0 rules written before
// normalizeRule defaulted TLS to 443 — after normalization those become
// duplicates of the correctly-ported entries.
func normalizeAndDedup(rules []config.EgressRule) []config.EgressRule {
	seen := make(map[string]struct{}, len(rules))
	out := make([]config.EgressRule, 0, len(rules))
	for _, r := range rules {
		r = normalizeRule(r)
		key := ruleKey(r)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	return out
}
