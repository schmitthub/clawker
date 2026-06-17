package mocks

import (
	"time"

	"github.com/schmitthub/clawker/internal/state"
)

// NewBlankState returns an in-memory *StateStoreMock seeded with a blank state
// snapshot. It is the default test double for consumers that don't care about
// specific state values.
func NewBlankState() *StateStoreMock {
	return NewFromString("")
}

// NewFromString returns an in-memory *StateStoreMock seeded from YAML. The YAML
// is parsed through the real schema, but state.NewFromString is the option-free
// in-memory seam (no WithStateDir), so the store discovers nothing on disk and
// touches no real XDG state dir: reads return the in-memory snapshot and writes
// are record-only. Legacy-key migrations do NOT run on the seam — that path is
// covered by intra-package New()+testenv tests. Panics on invalid YAML to match
// test-stub ergonomics.
func NewFromString(yaml string) *StateStoreMock {
	st, err := state.NewFromString(yaml)
	if err != nil {
		panic(err)
	}
	// state.NewFromString wires no path options, so this read discovers no file
	// and touches no real XDG state dir — the snapshot is purely the seed.
	snap := st.State()
	return newMock(snap)
}

// newMock returns a *StateStoreMock whose read getter returns the given
// snapshot and whose writes are record-only no-ops. moq records every call's
// args automatically, so consumer tests assert what production wrote via
// RecordUpdateCheckCalls() / SetLastSeenChangelogCalls() without any disk-backed
// store behind the stub.
func newMock(snap *state.State) *StateStoreMock {
	return &StateStoreMock{
		StateFunc: func() *state.State { return snap },
		RecordUpdateCheckFunc: func(time.Time, string) error {
			return nil
		},
		SetLastSeenChangelogFunc: func(string) error {
			return nil
		},
	}
}
