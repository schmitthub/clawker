package loop

import "github.com/schmitthub/clawker/internal/docker"

// loopAgentPrefix is prepended to all auto-generated loop agent names.
const loopAgentPrefix = "loop"

// GenerateAgentName creates a unique agent name for a loop session.
// Format: loop-<adjective>-<noun> (e.g., "loop-brave-turing").
// The generated name is always a valid Docker resource name.
func GenerateAgentName() string {
	return loopAgentPrefix + "-" + docker.GenerateRandomName()
}
