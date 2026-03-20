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
