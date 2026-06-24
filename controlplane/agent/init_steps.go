package agent

import (
	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

func configStep() ShellStep {
	return ShellStep{
		Name: "config",
		Shell: &clawkerdv1.ShellCommand{
			Stages:         []*clawkerdv1.PipeStage{userStage(ConfigSeedScript)},
			TimeoutSeconds: execStepTimeoutDefault,
			ExitOnNonZero:  true,
		},
	}
}

func gitStep() ShellStep {
	return ShellStep{
		Name: "git",
		Shell: &clawkerdv1.ShellCommand{
			Stages:         []*clawkerdv1.PipeStage{userStage(gitconfigFilterScript())},
			TimeoutSeconds: execStepTimeoutDefault,
			ExitOnNonZero:  true,
		},
	}
}

func gitCredentialsStep() ShellStep {
	return ShellStep{
		Name: "git-credentials",
		Shell: &clawkerdv1.ShellCommand{
			Stages:         []*clawkerdv1.PipeStage{userStage(GitCredentialsScript)},
			TimeoutSeconds: execStepTimeoutDefault,
			ExitOnNonZero:  true,
		},
	}
}

func sshStep() ShellStep {
	return ShellStep{
		Name: "ssh",
		Shell: &clawkerdv1.ShellCommand{
			Stages:         []*clawkerdv1.PipeStage{userStage(SshKnownHostsScript)},
			InitialStdin:   []byte(defaultKnownHosts),
			TimeoutSeconds: execStepTimeoutDefault,
			ExitOnNonZero:  true,
		},
	}
}

func postInitStep() ShellStep {
	return ShellStep{
		Name: consts.HookPostInit,
		Shell: &clawkerdv1.ShellCommand{
			Stages:         []*clawkerdv1.PipeStage{userStage(PostInitScript)},
			TimeoutSeconds: execStepTimeoutPostInit,
			ExitOnNonZero:  true,
			PrintOutput:    true,
		},
	}
}

// InitPlan returns the one-time init step list (config/git/credentials/ssh/
// post-init) the Executor runs once per container.
func InitPlan() []Step {
	return []Step{
		dockerSocketStep(),
		configStep(),
		gitStep(),
		gitCredentialsStep(),
		sshStep(),
		postInitStep(),
		AgentInitializedStep{Name: "agent-initialized"},
	}
}
