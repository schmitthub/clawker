package firewall

import "github.com/schmitthub/clawker/internal/config"

// EgressRulesFile is the top-level document type for storage.Store[T].
// It persists the active set of project-level egress rules to disk.
type EgressRulesFile struct {
	Rules []config.EgressRule `yaml:"rules"`
}
