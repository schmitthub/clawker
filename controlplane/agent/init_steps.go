package agent

import (
	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

var configStep shellStep = shellStep{
	Name: "config",
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(configSeedScript)},
		TimeoutSeconds: execStepTimeoutDefault,
		ExitOnNonZero:  true,
	},
}

var gitStep shellStep = shellStep{
	Name: "git",
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(gitconfigFilterScript)},
		TimeoutSeconds: execStepTimeoutDefault,
		ExitOnNonZero:  true,
	},
}

var gitCredentialsStep shellStep = shellStep{
	Name: "git-credentials",
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(gitCredentialsScript)},
		TimeoutSeconds: execStepTimeoutDefault,
		ExitOnNonZero:  true,
	},
}

var sshStep shellStep = shellStep{
	Name: "ssh",
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(sshKnownHostsScript)},
		InitialStdin:   []byte(defaultKnownHosts),
		TimeoutSeconds: execStepTimeoutDefault,
		ExitOnNonZero:  true,
	},
}

var postInitStep shellStep = shellStep{
	Name: consts.HookPostInit,
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(postInitScript)},
		TimeoutSeconds: execStepTimeoutPostInit,
		ExitOnNonZero:  true,
		PrintOutput:    true,
	},
}

var initPlan = []step{
	dockerSocketStep,
	configStep,
	gitStep,
	gitCredentialsStep,
	sshStep,
	postInitStep,
	agentInitializedStep{Name: "agent-initialized"},
}
