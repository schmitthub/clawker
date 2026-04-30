package agentdial

import (
	"crypto/sha256"
	"time"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// Event types published by the agentdial component for each
// CP→clawkerd Session lifecycle transition. Each is its own Go type;
// no shared "Lifecycle" string field collides with dockerevents'
// ContainerStarted/Stopped vocabulary (Y5 dies structurally).
//
// The four states form a state machine:
//
//	(start) → SessionConnecting → SessionConnected → SessionBroken → (re-dial loop)
//	          SessionConnecting → SessionFailed (terminal — retry budget exhausted)
//
// All events implement the Overseer applier interface (ApplyTo) so
// the bus's worldview State.AgentSessions reflects current dial state
// after each publish.

// SessionConnecting fires when a dial attempt starts (first
// successful inspect of the container in a cycle). Carries the
// agent identity labels and the address being dialed.
type SessionConnecting struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	At          time.Time
}

func (e SessionConnecting) EventName() string     { return "agentdial.session.connecting" }
func (e SessionConnecting) OccurredAt() time.Time { return e.At }
func (e SessionConnecting) ApplyTo(s *overseer.State) {
	view := s.AgentSessions[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.Status = overseer.SessionStatusConnecting
	view.UpdatedAt = e.At
	view.LastError = ""
	s.AgentSessions[e.ContainerID] = view
}

// RegistryOutcome classifies the result of cross-checking the peer
// certificate against the agentregistry row keyed by container_id.
// Exactly one value is set per Provenance: the typed enum replaces an
// earlier four-boolean shape where mutual exclusion lived only in a
// doc comment. Subscribers switch on the value to enact policy.
type RegistryOutcome string

const (
	// RegistryOutcomeNotQueried is the zero value. Set when the registry
	// could not be queried at all (lookup error, registry not wired);
	// Provenance.Reason carries the detail. The name is deliberately
	// loud at every reader site: a Provenance{} literal silently means
	// "not queried", which would be wrong if the dialer simply forgot
	// to populate the field. Distinct from Miss, which means the
	// registry was queried successfully and returned no row.
	RegistryOutcomeNotQueried RegistryOutcome = ""
	// RegistryOutcomeMatch — row exists, thumbprint AND canonical_cn
	// agree with the peer cert. The happy path.
	RegistryOutcomeMatch RegistryOutcome = "match"
	// RegistryOutcomeMiss — no row for this container_id. Container
	// started outside the CLI bootstrap path (raw `docker start`,
	// manual `docker create`, or registry corruption).
	RegistryOutcomeMiss RegistryOutcome = "miss"
	// RegistryOutcomeThumbprintMismatch — row exists but its thumbprint
	// disagrees with the peer cert thumbprint. Possible cert theft or
	// wiring corruption.
	RegistryOutcomeThumbprintMismatch RegistryOutcome = "thumbprint_mismatch"
	// RegistryOutcomeCNMismatch — row exists, thumbprints agree, but
	// the row's canonical_cn does not match the peer's CN. Structural
	// drift between the CLI's registry write and the cert subject.
	RegistryOutcomeCNMismatch RegistryOutcome = "cn_mismatch"
)

// Provenance carries connection-time identity outcomes determined
// by the dialer at the moment of TLS handshake + post-handshake
// inspection. The dialer is a sensor: every check produces a typed
// data point here, the connection NEVER aborts on cert/identity
// grounds (CP-overlord asymmetric trust — see package doc + dialer.go
// header). Subscribers consume these fields to enact policy
// (containment, alerting, eviction). The dialer holds no policy.
//
// RegistryOutcome is the load-bearing field for registry-vs-peer
// cross-check. ChainVerified, CNPinMatch, PeerCN, PeerThumbprint are
// independent data points that ride alongside.
type Provenance struct {
	// ChainVerified reports whether the peer's leaf certificate chains
	// up to the CLI CA pool. False on parse failure, missing chain,
	// self-signed, or expired cert. The dialer connects regardless.
	ChainVerified bool
	// PeerCN is the Subject CommonName extracted from the peer's leaf
	// certificate. Empty if the leaf could not be parsed.
	PeerCN string
	// CNPinMatch reports whether PeerCN equals the canonical agent CN
	// derived from (project, agent_name) for this container. False
	// implies the peer is not who we expected; subscribers may treat
	// as suspicious.
	CNPinMatch bool
	// PeerThumbprint is the SHA-256 fingerprint of the peer's leaf
	// certificate (raw bytes, no encoding). Zero array on parse
	// failure. Fixed-size value type matches agentregistry.Entry.Thumbprint
	// — no aliasing risk when a published event hands subscribers the
	// payload, no length-equals-32 guard scattered through readers.
	PeerThumbprint [sha256.Size]byte
	// RegistryOutcome classifies the registry cross-check result.
	// Exactly one value per event; structural mutual exclusion replaces
	// the prior four-boolean shape.
	RegistryOutcome RegistryOutcome
	// Reason is a free-form note for outcomes the typed fields don't
	// cleanly describe (e.g. "leaf parse failed", "registry lookup
	// error: <io>"). Empty when RegistryOutcome speaks for itself.
	Reason string
}

// SessionConnected fires when a Session establishes (mTLS dial +
// Hello handshake completes). Attempts records how many tries the
// dial cycle took to land. Provenance carries the connection-time
// identity outcomes (chain verify, CN pin, registry cross-check) as
// data fields — the dialer never aborts on these; subscribers decide
// what to do.
type SessionConnected struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	Attempts    int
	Provenance  Provenance
	At          time.Time
}

func (e SessionConnected) EventName() string     { return "agentdial.session.connected" }
func (e SessionConnected) OccurredAt() time.Time { return e.At }
func (e SessionConnected) ApplyTo(s *overseer.State) {
	view := s.AgentSessions[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.Status = overseer.SessionStatusConnected
	view.Attempts = e.Attempts
	view.UpdatedAt = e.At
	view.LastError = ""
	s.AgentSessions[e.ContainerID] = view
}

// SessionFailed fires when the retry budget for a dial cycle exhausts
// before any attempt established a Session. Reason carries a short
// classification ("connect_total_timeout", "container_not_running");
// the underlying dial error is in the log line, not on the event.
type SessionFailed struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	Reason      string
	Attempts    int
	At          time.Time
}

func (e SessionFailed) EventName() string     { return "agentdial.session.failed" }
func (e SessionFailed) OccurredAt() time.Time { return e.At }
func (e SessionFailed) ApplyTo(s *overseer.State) {
	view := s.AgentSessions[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.Status = overseer.SessionStatusFailed
	view.Attempts = e.Attempts
	view.LastError = e.Reason
	view.UpdatedAt = e.At
	s.AgentSessions[e.ContainerID] = view
}

// SessionBroken fires when an established Session terminates. Reason
// classifies the cause (peer EOF, transport break, error string).
// Not published on intentional teardown (CP shutdown / ctx cancel) —
// see runDial for the suppression rationale.
type SessionBroken struct {
	ContainerID string
	AgentName   string
	Project     string
	Address     string
	Reason      string
	At          time.Time
}

func (e SessionBroken) EventName() string     { return "agentdial.session.broken" }
func (e SessionBroken) OccurredAt() time.Time { return e.At }
func (e SessionBroken) ApplyTo(s *overseer.State) {
	view := s.AgentSessions[e.ContainerID]
	view.ContainerID = e.ContainerID
	view.AgentName = e.AgentName
	view.Project = e.Project
	view.Address = e.Address
	view.Status = overseer.SessionStatusBroken
	view.LastError = e.Reason
	view.UpdatedAt = e.At
	s.AgentSessions[e.ContainerID] = view
}
