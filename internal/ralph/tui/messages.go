// Package tui provides the Ralph TUI dashboard implementation.
package tui

// Message types for Ralph TUI.
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
// - agentsDiscoveredMsg: list of discovered ralph agents
// - agentStatusMsg: status update for a specific agent
// - logChunkMsg: log output from an agent
// - sessionUpdateMsg: ralph session state changes
