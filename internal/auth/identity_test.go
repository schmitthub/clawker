package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewAgentName_Sanity pins the only runtime check on AgentName:
// empty input errors with "agent name required". Charset/length rules
// were removed — downstream consumers (Docker, x509, gRPC
// IdentityInterceptor) enforce their own constraints.
func TestNewAgentName_Sanity(t *testing.T) {
	t.Parallel()

	got, err := NewAgentName("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent name required")
	assert.True(t, got.IsZero())

	got, err = NewAgentName("dev")
	require.NoError(t, err)
	assert.Equal(t, "dev", got.String())
	assert.False(t, got.IsZero())

	// Inputs that the old validator rejected (dots, AgentFullName
	// prefix, spaces, etc.) are now accepted. They round-trip
	// faithfully through the wrapper; downstream layers decide what
	// to do with them.
	for _, exotic := range []string{"dot.in.name", "clawker.dev", "with space", "compassionate-chandrasekhar"} {
		got, err = NewAgentName(exotic)
		require.NoError(t, err, "input %q must round-trip", exotic)
		assert.Equal(t, exotic, got.String())
	}
}

// TestNewProjectSlug_Sanity pins that ProjectSlug never errors and
// preserves the empty/unscoped case as the zero value.
func TestNewProjectSlug_Sanity(t *testing.T) {
	t.Parallel()

	got, err := NewProjectSlug("")
	require.NoError(t, err)
	assert.True(t, got.IsEmpty(), "empty input is the unscoped signal")
	assert.Equal(t, "", got.String())

	got, err = NewProjectSlug("myapp")
	require.NoError(t, err)
	assert.False(t, got.IsEmpty())
	assert.Equal(t, "myapp", got.String())

	// Inputs the old validator rejected pass through faithfully.
	for _, exotic := range []string{"foo.bar", "clawker.foo", "with space"} {
		got, err = NewProjectSlug(exotic)
		require.NoError(t, err)
		assert.Equal(t, exotic, got.String())
		assert.False(t, got.IsEmpty())
	}
}

// TestMust_Wrappers pins the panic/non-panic shape of the Must*
// constructors. MustAgentName still panics on empty (the only error
// NewAgentName returns). MustProjectSlug never panics.
func TestMust_Wrappers(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() { _ = MustAgentName("dev") })
	assert.Panics(t, func() { _ = MustAgentName("") })

	assert.NotPanics(t, func() { _ = MustProjectSlug("") })
	assert.NotPanics(t, func() { _ = MustProjectSlug("myapp") })
	assert.NotPanics(t, func() { _ = MustProjectSlug("foo.bar") })
}

// TestAgentFullName_TwoVsThreeSegment confirms the AgentFullName
// composer still produces the documented forms for scoped (3-segment)
// and unscoped (2-segment) cases.
func TestAgentFullName_TwoVsThreeSegment(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"clawker.myapp.dev",
		AgentFullName(MustProjectSlug("myapp"), MustAgentName("dev")))
	assert.Equal(t,
		"clawker.solo",
		AgentFullName(ProjectSlug{}, MustAgentName("solo")))
}
