package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComponentType_Dir(t *testing.T) {
	cases := []struct {
		t   ComponentType
		dir string
		str string
	}{
		{ComponentHarness, "harnesses", "harness"},
		{ComponentStack, "stacks", "stack"},
		{ComponentMonitoring, "monitoring", "monitoring"},
	}
	for _, c := range cases {
		t.Run(c.str, func(t *testing.T) {
			assert.True(t, c.t.Valid())
			assert.Equal(t, c.dir, c.t.Dir())
			assert.Equal(t, c.str, c.t.String())
			// The reverse map round-trips.
			got, ok := componentTypeForDir(c.dir)
			assert.True(t, ok)
			assert.Equal(t, c.t, got)
		})
	}
}

func TestComponentType_InvalidAndReverse(t *testing.T) {
	var bogus ComponentType = 99
	assert.False(t, bogus.Valid())
	assert.Empty(t, bogus.Dir())

	_, ok := componentTypeForDir("assets")
	assert.False(t, ok, "a non-convention dir has no component type")
	_, ok = componentTypeForDir(".clawker-bundle")
	assert.False(t, ok)
}
