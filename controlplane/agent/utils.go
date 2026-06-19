package agent

import "fmt"

func shouldAgentInit(res establishResult) (bool, error) {
	if res.HelloAck == nil {
		return false, fmt.Errorf("missing HelloAck in establishResult")
	}
	if res.HelloAck.Initialized {
		return false, nil
	}
	return true, nil
}

func shouldAgentBoot(res establishResult) (bool, error) {
	if res.HelloAck == nil {
		return false, fmt.Errorf("missing HelloAck in establishResult")
	}
	if res.HelloAck.CmdRunning {
		return false, nil
	}
	return true, nil
}
