package agent_test

import (
	"crypto/sha256"

	clawkerdv1mocks "github.com/schmitthub/clawker/api/clawkerd/v1/mocks"
	"github.com/schmitthub/clawker/controlplane/agent"
)

// happyEstablishResult is the fixture for a Match-classified peer.
func happyEstablishResult(stream *clawkerdv1mocks.FakeSessionStream, peerCN string, peerThumb [sha256.Size]byte) agent.EstablishResult {
	return agent.EstablishResult{
		Stream:   stream,
		Agent:    "dev",
		Project:  "myapp",
		Addr:     "10.0.0.1:7700",
		Attempt:  1,
		Outcome:  agent.OutcomeSuccess,
		PeerInfo: agent.PeerInfo{PeerAgentFullName: peerCN, PeerThumbprint: peerThumb},
	}
}
