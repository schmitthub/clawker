package shared

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAgentName_Format(t *testing.T) {
	name := GenerateAgentName()

	assert.True(t, strings.HasPrefix(name, "loop-"), "name should start with loop- prefix, got %q", name)

	// Should have 3 parts: loop, adjective, noun
	parts := strings.SplitN(name, "-", 3)
	require.Len(t, parts, 3, "name should have 3 hyphen-separated parts: loop-adj-noun, got %q", name)
	assert.Equal(t, "loop", parts[0])
	assert.NotEmpty(t, parts[1], "adjective part should not be empty")
	assert.NotEmpty(t, parts[2], "noun part should not be empty")
}

func TestGenerateAgentName_ValidDockerName(t *testing.T) {
	// Generate several names and verify all are valid Docker resource names.
	for range 50 {
		name := GenerateAgentName()
		err := docker.ValidateResourceName(name)
		assert.NoError(t, err, "generated name %q should be a valid Docker resource name", name)
	}
}

func TestGenerateAgentName_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	duplicates := 0

	// With ~20,000 possible combinations, generating 100 names
	// should produce very few (likely zero) duplicates.
	const count = 100
	for range count {
		name := GenerateAgentName()
		if seen[name] {
			duplicates++
		}
		seen[name] = true
	}

	assert.Less(t, duplicates, 5, "expected very few duplicates in %d generated names", count)
}

func TestGenerateAgentName_Prefix(t *testing.T) {
	assert.Equal(t, "loop", loopAgentPrefix)
}
