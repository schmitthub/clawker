package firewall

import (
	"fmt"

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

// ruleKey returns the dedup key for an egress rule: dst:proto:port.
func ruleKey(r config.EgressRule) string {
	proto := r.Proto
	if proto == "" {
		proto = "tls"
	}
	return fmt.Sprintf("%s:%s:%d", r.Dst, proto, r.Port)
}
