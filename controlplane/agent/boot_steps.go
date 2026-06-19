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

var bootPlan = []step{
	preRunStep,
	dockerSocketStep,
	agentReadyStep{Name: "agent-ready"},
}
