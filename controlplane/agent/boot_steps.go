package agent

import (
	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

func dockerSocketStep() ShellStep {
	return ShellStep{
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
}

func preRunStep() ShellStep {
	return ShellStep{
		Name: consts.HookPreRun,
		Shell: &clawkerdv1.ShellCommand{
			Stages:         []*clawkerdv1.PipeStage{userStage(PreRunScript)},
			TimeoutSeconds: execStepTimeoutPostInit,
			ExitOnNonZero:  true,
			PrintOutput:    true,
		},
	}
}

// bootPlanPost is the fixed boot tail: pre_run (the last user hook before
// the CMD) then agent-ready (releases the CMD, must be terminal so no Step
// races the CMD past the entrypoint fifo). New boot steps prepend to
// BootPlan's head; this pair stays last, in this order. Split out and named
// so the ordering invariant survives future edits. Pinned by
// TestBootPlan_PreRunShape.
func bootPlanPost() []Step {
	return []Step{
		preRunStep(),
		AgentReadyStep{Name: "agent-ready"},
	}
}

// BootPlan returns the every-start boot step list the Executor runs on each
// start of a container.
func BootPlan() []Step {
	return append([]Step{
		dockerSocketStep(),
	}, bootPlanPost()...)
}
