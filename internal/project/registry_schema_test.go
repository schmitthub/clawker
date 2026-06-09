package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProjectRegistryFields_AllFieldsHaveDescriptions(t *testing.T) {
	fs := ProjectRegistry{}.Fields()
	for _, f := range fs.All() {
		assert.NotEmptyf(t, f.Description(), "field %q has no desc tag", f.Path())
	}
}
