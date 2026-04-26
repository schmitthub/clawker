package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewAgentName_Validation pins the contract so a future relaxation
// surfaces here rather than as silently-malformed cert subjects or
// segment-counting bugs downstream.
func TestNewAgentName_Validation(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
		errSub  string
	}{
		{"valid simple", "dev", false, ""},
		{"valid alnum", "dev1", false, ""},
		{"valid with underscore", "dev_main", false, ""},
		{"valid with hyphen", "dev-main", false, ""},
		{"valid mixed case", "DevAgent", false, ""},
		{"empty rejected", "", true, "agent name required"},
		// Dot is the segment separator in the canonical CN — a dot inside
		// the short name corrupts every downstream parser.
		{"dot rejected", "dev.bot", true, "must match"},
		// Canonical-form prefix would produce "clawker.clawker.<...>" if
		// composed inside MintAgentCert.
		{"canonical prefix rejected", "clawker.dev", true, "canonical"},
		// Exact prefix without trailing segment is also caught.
		{"bare clawker prefix rejected", "clawker.", true, "canonical"},
		{"leading hyphen rejected", "-dev", true, "must match"},
		{"slash rejected", "dev/main", true, "must match"},
		{"space rejected", "dev main", true, "must match"},
		{"too long rejected", strings.Repeat("a", shortNameMax+1), true, "too long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewAgentName(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSub)
				assert.True(t, got.IsZero(), "error path must return the zero value")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.input, got.String())
			assert.False(t, got.IsZero())
		})
	}
}

// TestNewProjectSlug_Validation mirrors AgentName but allows empty
// (the unscoped 2-segment naming case).
func TestNewProjectSlug_Validation(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty allowed", "", false},
		{"valid simple", "myapp", false},
		{"valid alnum", "myapp1", false},
		{"valid with hyphen", "my-app", false},
		{"dot rejected", "my.app", true},
		{"canonical prefix rejected", "clawker.app", true},
		{"slash rejected", "my/app", true},
		{"too long rejected", strings.Repeat("p", shortNameMax+1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewProjectSlug(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				assert.True(t, got.IsEmpty(), "error path must return the zero value")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.input, got.String())
			assert.Equal(t, tc.input == "", got.IsEmpty())
		})
	}
}

// TestMustAgentName_PanicsOnInvariantViolation locks the loud-failure
// posture: a caller that asserts a string is valid via MustAgentName
// must surface a typo or unvalidated input as a panic at the call
// site, not as silently-malformed identity.
func TestMustAgentName_PanicsOnInvariantViolation(t *testing.T) {
	assert.Panics(t, func() { MustAgentName("") })
	assert.Panics(t, func() { MustAgentName("dot.in.name") })
	assert.Panics(t, func() { MustAgentName("clawker.dev") })
	assert.NotPanics(t, func() { _ = MustAgentName("dev") })
}

// TestMustProjectSlug_PanicsOnInvariantViolation. Empty input is
// valid for ProjectSlug (unscoped case) so MustProjectSlug must NOT
// panic on "" — symmetric with NewProjectSlug.
func TestMustProjectSlug_PanicsOnInvariantViolation(t *testing.T) {
	assert.NotPanics(t, func() { _ = MustProjectSlug("") })
	assert.NotPanics(t, func() { _ = MustProjectSlug("myapp") })
	assert.Panics(t, func() { MustProjectSlug("dot.app") })
	assert.Panics(t, func() { MustProjectSlug("clawker.app") })
}

// TestCanonicalAgentCN_TwoVsThreeSegment confirms the rule lives in
// exactly one place and produces the expected forms for both the
// scoped (3-segment) and unscoped (2-segment) cases.
func TestCanonicalAgentCN_TwoVsThreeSegment(t *testing.T) {
	assert.Equal(t,
		"clawker.myapp.dev",
		CanonicalAgentCN(MustProjectSlug("myapp"), MustAgentName("dev")))

	assert.Equal(t,
		"clawker.solo",
		CanonicalAgentCN(ProjectSlug{}, MustAgentName("solo")))
}
