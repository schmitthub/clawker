package mocks

import (
	"time"

	"github.com/schmitthub/clawker/internal/state"
)

// NewBlankState returns an in-memory *StateStoreMock over blank state. It is the
// default test double for consumers that don't care about specific state values.
func NewBlankState() *StateStoreMock {
	return NewFromString("")
}

// NewFromString returns an in-memory *StateStoreMock seeded from YAML. The YAML
// is parsed through the real schema, but state.NewFromString is the option-free
// in-memory seam (no WithStateDir), so the store discovers nothing on disk and
// touches no real XDG state dir. Read accessors delegate to that seeded store;
// writes are record-only no-ops, since a seam store has no write path and its
// real Write() errors by design. moq records every call's args automatically, so
// consumer tests assert what production wrote via RecordUpdateCheckCalls() /
// SetLastSeenChangelogCalls(). Legacy-key migrations do NOT run on the seam —
// that path is covered by intra-package New()+testenv tests. Panics on invalid
// YAML to match test-stub ergonomics.
func NewFromString(yaml string) *StateStoreMock {
	st, err := state.NewFromString(yaml)
	if err != nil {
		panic(err)
	}
	return &StateStoreMock{
		CheckedAtFunc:         st.CheckedAt,
		LatestVersionFunc:     st.LatestVersion,
		LastSeenChangelogFunc: st.LastSeenChangelog,
		RecordUpdateCheckFunc: func(time.Time, string) error {
			return nil
		},
		SetLastSeenChangelogFunc: func(string) error {
			return nil
		},
	}
}
