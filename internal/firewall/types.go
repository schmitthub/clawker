package firewall

import (
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/storage"
)

// EgressRulesFile is the top-level document type for storage.Store[T].
// It persists the active set of project-level egress rules to disk.
type EgressRulesFile struct {
	Rules []config.EgressRule `yaml:"rules" label:"Rules" desc:"Active egress firewall rules"`
}

// Fields implements [storage.Schema] for EgressRulesFile.
func (f EgressRulesFile) Fields() storage.FieldSet {
	return storage.NormalizeFields(f)
}
