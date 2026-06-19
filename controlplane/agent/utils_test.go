package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
)

// TestPlanGates_LifecycleTable pins the init/boot dispatch decision across
// the full (Initialized, CmdRunning) space. CmdRunning is authoritative: a
// running CMD short-circuits both plans (CP reattached to a live agent).
// The CmdRunning && !Initialized row is structurally impossible — neither
// plan runs and agentInitBypassed flags it for trust-axis tracking.
func TestPlanGates_LifecycleTable(t *testing.T) {
	cases := []struct {
		name        string
		initialized bool
		cmdRunning  bool
		wantInit    bool
		wantBoot    bool
		wantBypass  bool
	}{
		{"live agent (init+cmd)", true, true, false, false, false},
		{"fresh start", false, false, true, true, false},
		{"restart (init, no cmd)", true, false, false, true, false},
		{"impossible (cmd, no init)", false, true, false, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := establishResult{HelloAck: &clawkerdv1.HelloAck{
				Initialized: tc.initialized,
				CmdRunning:  tc.cmdRunning,
			}}

			gotInit, err := shouldAgentInit(res)
			require.NoError(t, err)
			assert.Equal(t, tc.wantInit, gotInit, "shouldAgentInit")

			gotBoot, err := shouldAgentBoot(res)
			require.NoError(t, err)
			assert.Equal(t, tc.wantBoot, gotBoot, "shouldAgentBoot")

			assert.Equal(t, tc.wantBypass, agentInitBypassed(res), "agentInitBypassed")
		})
	}
}

// TestPlanGates_NilHelloAck: a missing HelloAck is an error for the dispatch
// gates (can't decide) and not a bypass (the gates already surface the
// error upstream; agentInitBypassed must not double-report it).
func TestPlanGates_NilHelloAck(t *testing.T) {
	res := establishResult{HelloAck: nil}

	_, err := shouldAgentInit(res)
	assert.Error(t, err, "shouldAgentInit must error on nil HelloAck")

	_, err = shouldAgentBoot(res)
	assert.Error(t, err, "shouldAgentBoot must error on nil HelloAck")

	assert.False(t, agentInitBypassed(res), "nil HelloAck is not a bypass")
}
