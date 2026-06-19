package agent

import (
	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

var dockerSocketStep shellStep = shellStep{
	Name: "docker-socket",
	Shell: &clawkerdv1.ShellCommand{
		Stages: []*clawkerdv1.PipeStage{{
			Argv: []string{"sh", "-c", `[ -S /var/run/docker.sock ] && chgrp docker /var/run/docker.sock || true`},
			Uid:  0,
			Gid:  0,
		}},
		TimeoutSeconds: execStepTimeoutDefault,
		ExitOnNonZero:  true,
	},
}

var preRunStep shellStep = shellStep{
	Name: consts.HookPreRun,
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(preRunScript)},
		TimeoutSeconds: execStepTimeoutPostInit,
		ExitOnNonZero:  true,
		PrintOutput:    true,
	},
}

// bootPlanPost is the fixed boot tail: pre_run (the last user hook before
// the CMD) then agent-ready (releases the CMD, must be terminal so no step
// races the CMD past the entrypoint fifo). New boot steps prepend to
// bootPlan's head; this pair stays last, in this order. Split out and named
// so the ordering invariant survives future edits. Pinned by
// TestBootPlan_PreRunShape.
var bootPlanPost = []step{
	preRunStep,
	agentReadyStep{Name: "agent-ready"},
}

var bootPlan = append([]step{
	dockerSocketStep,
}, bootPlanPost...)
