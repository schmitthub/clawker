package agent

import (
	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

var configStep ShellStep = ShellStep{
	Name: "config",
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(ConfigSeedScript)},
		TimeoutSeconds: execStepTimeoutDefault,
		ExitOnNonZero:  true,
	},
}

var gitStep ShellStep = ShellStep{
	Name: "git",
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(gitconfigFilterScript)},
		TimeoutSeconds: execStepTimeoutDefault,
		ExitOnNonZero:  true,
	},
}

var gitCredentialsStep ShellStep = ShellStep{
	Name: "git-credentials",
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(GitCredentialsScript)},
		TimeoutSeconds: execStepTimeoutDefault,
		ExitOnNonZero:  true,
	},
}

var sshStep ShellStep = ShellStep{
	Name: "ssh",
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(SshKnownHostsScript)},
		InitialStdin:   []byte(defaultKnownHosts),
		TimeoutSeconds: execStepTimeoutDefault,
		ExitOnNonZero:  true,
	},
}

var postInitStep ShellStep = ShellStep{
	Name: consts.HookPostInit,
	Shell: &clawkerdv1.ShellCommand{
		Stages:         []*clawkerdv1.PipeStage{userStage(PostInitScript)},
		TimeoutSeconds: execStepTimeoutPostInit,
		ExitOnNonZero:  true,
		PrintOutput:    true,
	},
}

var InitPlan = []Step{
	dockerSocketStep,
	configStep,
	gitStep,
	gitCredentialsStep,
	sshStep,
	postInitStep,
	AgentInitializedStep{Name: "agent-initialized"},
}
