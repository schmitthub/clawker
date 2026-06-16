package mocks

import (
	"github.com/schmitthub/clawker/internal/state"
)

// NewBlankConfig returns an in-memory *ConfigMock seeded with defaults.
// It is the default test double for consumers that don't care about specific config values.
func NewBlankState() *StateStoreMock {
	st, err := state.New()
	if err != nil {
		panic(err)
	}
	return newMockFrom(st)
}

// NewFromString creates an in-memory *StateMock from YAML.
// Panics on invalid YAML to match test-stub ergonomics.
func NewFromString(yaml string) *StateStoreMock {
	st, err := state.NewFromString(yaml)
	if err != nil {
		panic(err)
	}
	return newMockFrom(st)
}

// newMockFrom returns a *StateMock with all reads wired to zero values and all
// writes wired to succeed (and record calls). Consumer tests override only the
// funcs they assert on; the recorded *Calls() expose what production wrote.
func newMockFrom(st state.StateStore) *StateStoreMock {
	mock := &StateStoreMock{}
	mock.RecordUpdateCheckFunc = st.RecordUpdateCheck
	mock.SetLastSeenChangelogFunc = st.SetLastSeenChangelog
	return mock
}
