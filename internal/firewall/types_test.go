package firewall_test

import (
	"testing"

	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/stretchr/testify/assert"
)

func TestEgressRulesFileFields_AllFieldsHaveDescriptions(t *testing.T) {
	fs := firewall.EgressRulesFile{}.Fields()
	for _, f := range fs.All() {
		assert.NotEmptyf(t, f.Description(), "field %q has no desc tag", f.Path())
	}
}
