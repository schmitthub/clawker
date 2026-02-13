// Package tui provides the loop TUI dashboard implementation.
package tui

// Message types for loop TUI.
// Phase 1 defines minimal messages; additional messages will be added in later phases.

// errMsg wraps errors for display in the TUI.
type errMsg struct {
	err error
}

// Error returns the error message.
func (e errMsg) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return ""
}

// Future phases will add:
// - agentsDiscoveredMsg: list of discovered loop agents
// - agentStatusMsg: status update for a specific agent
// - logChunkMsg: log output from an agent
// - sessionUpdateMsg: loop session state changes
