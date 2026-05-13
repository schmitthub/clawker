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
		// Dot is the segment separator in the AgentFullName — a dot
		// inside the short name corrupts every downstream parser.
		{"dot rejected", "dev.bot", true, "must match"},
		// AgentFullName prefix would produce "clawker.clawker.<...>" if
		// composed inside MintAgentCert.
		{"agent-full-name prefix rejected", "clawker.dev", true, "AgentFullName"},
		// Exact prefix without trailing segment is also caught.
		{"bare clawker prefix rejected", "clawker.", true, "AgentFullName"},
		{"leading hyphen rejected", "-dev", true, "must match"},
		{"slash rejected", "dev/main", true, "must match"},
		{"space rejected", "dev main", true, "must match"},
		{"too long rejected", strings.Repeat("a", shortNameMax+1), true, "too long"},
		// Regression pin: docker.GenerateRandomName's worst-case output
		// (longest adjective "compassionate" + longest noun
		// "chandrasekhar" + hyphen = 27 chars) must validate. The cap
		// used to be 24 — random names exceeded it and CreateContainer
		// failed with "agent bootstrap: invalid agent name".
		{"docker-random worst case accepted", "compassionate-chandrasekhar", false, ""},
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
		{"agent-full-name prefix rejected", "clawker.app", true},
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

// TestMust_PanicWrapsValidation pins the contract that Must*
// constructors are pure panic-wrappers around their New* counterparts:
// any input that errors in New* must panic in Must*. The validation
// tables above cover the input space — this only proves the wrapping
// shape (one canonical invalid + one canonical valid per type) so a
// regression that silently swallows the error in Must* fails fast.
func TestMust_PanicWrapsValidation(t *testing.T) {
	assert.Panics(t, func() { MustAgentName("dot.in.name") })
	assert.NotPanics(t, func() { _ = MustAgentName("dev") })
	// Empty is valid for ProjectSlug (unscoped 2-segment case) so
	// MustProjectSlug must not panic on "".
	assert.NotPanics(t, func() { _ = MustProjectSlug("") })
	assert.Panics(t, func() { MustProjectSlug("dot.app") })
}

// TestAgentFullName_TwoVsThreeSegment confirms the rule lives in
// exactly one place and produces the expected forms for both the
// scoped (3-segment) and unscoped (2-segment) cases.
func TestAgentFullName_TwoVsThreeSegment(t *testing.T) {
	assert.Equal(t,
		"clawker.myapp.dev",
		AgentFullName(MustProjectSlug("myapp"), MustAgentName("dev")))

	assert.Equal(t,
		"clawker.solo",
		AgentFullName(ProjectSlug{}, MustAgentName("solo")))
}
