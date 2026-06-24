package agent

import "fmt"

func shouldAgentInit(res EstablishResult) (bool, error) {
	if res.HelloAck == nil {
		return false, fmt.Errorf("missing HelloAck in establishResult")
	}
	// CmdRunning is authoritative: a running CMD means CP reattached to a
	// live agent that already ran init+boot — never re-init it. (Without
	// this gate the CmdRunning && !Initialized row would wrongly dispatch
	// init against an already-running container.)
	if res.HelloAck.CmdRunning {
		return false, nil
	}
	if res.HelloAck.Initialized {
		return false, nil
	}
	return true, nil
}

// agentInitBypassed reports the structurally-impossible lifecycle state:
// the user CMD is running while init was never marked complete. The CMD
// forks only after the init plan finishes, so observing CmdRunning &&
// !Initialized means a CP lifecycle bug or a container that forked its
// CMD out-of-band (suspicious). The dialer tracks it on the trust axis
// and stays permissive; subscribers enact policy. A nil HelloAck is the
// shouldAgent* error case, handled upstream — here it is simply not a
// bypass.
func agentInitBypassed(res EstablishResult) bool {
	return res.HelloAck != nil && res.HelloAck.CmdRunning && !res.HelloAck.Initialized
}

func shouldAgentBoot(res EstablishResult) (bool, error) {
	if res.HelloAck == nil {
		return false, fmt.Errorf("missing HelloAck in establishResult")
	}
	if res.HelloAck.CmdRunning {
		return false, nil
	}
	return true, nil
}
