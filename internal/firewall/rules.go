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

// normalizeRule fills in defaults so stored rules are explicit and unambiguous.
// TLS/empty → port 443, SSH → port 22, TCP → port 0 stays (means "any port").
func normalizeRule(r config.EgressRule) config.EgressRule {
	if r.Proto == "" {
		r.Proto = "tls"
	}
	if r.Action == "" {
		r.Action = "allow"
	}
	if r.Port == 0 {
		switch strings.ToLower(r.Proto) {
		case "tls":
			r.Port = 443
		case "ssh":
			r.Port = 22
		}
		// tcp: port 0 = any port, intentional
	}
	return r
}

// ruleKey returns the dedup key for an egress rule: dst:proto:port.
func ruleKey(r config.EgressRule) string {
	return fmt.Sprintf("%s:%s:%d", r.Dst, r.Proto, r.Port)
}
