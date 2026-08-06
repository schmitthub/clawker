// Package clawker holds the CLI's own domain types and interface declarations.
// It is a leaf: nothing here imports another internal package. The entry point
// that builds and runs the command tree lives in internal/clawkercmd.
package clawker

// Session is process-scoped, in-memory state for one CLI invocation — never
// persisted (internal/state owns anything that outlives the process). It
// carries what this invocation has decided about itself, so a command can
// change the runtime's behavior for the rest of its own run.
//
// Both flags default to enabled. A command that runs elevated turns them off so
// the run leaves nothing root-owned behind in the invoking user's home. See
// internal/clawker/CLAUDE.md.
type Session interface {
	SetFileLogging(enabled bool)
	FileLogging() bool
	SetNotifications(enabled bool)
	Notifications() bool
}

type session struct {
	fileLogging   bool
	notifications bool
}

// NewSession returns a Session with both flags enabled — the default posture a
// command opts out of. It returns the interface deliberately: the concrete type
// stays unexported so callers depend on the contract, not the struct.
//
//nolint:ireturn // the interface is the seam; see above.
func NewSession() Session {
	return &session{
		fileLogging:   true,
		notifications: true,
	}
}

func (s *session) SetFileLogging(enabled bool) {
	s.fileLogging = enabled
}

func (s *session) FileLogging() bool {
	return s.fileLogging
}

func (s *session) SetNotifications(enabled bool) {
	s.notifications = enabled
}

func (s *session) Notifications() bool {
	return s.notifications
}
