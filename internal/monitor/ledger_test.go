package monitor_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/monitor"
)

// resolved builds a ResolvedUnit fixture for ledger-merge tests. The backing
// unit carries a one-lane manifest so the snapshot recorded in the ledger is
// non-trivial; Merge never walks the artifact FS.
func resolved(name, root, hash string, qualified bool) monitor.ResolvedUnit {
	return monitor.ResolvedUnit{
		Name:      name,
		Qualified: qualified,
		Unit: &monitor.MonitoringUnit{
			Name: name,
			Manifest: config.MonitoringUnitManifest{
				Description: "",
				Logs:        []config.MonitoringLogLane{{Index: name, ServiceNames: []string{name}, Retention: ""}},
				Metrics:     nil,
			},
		},
		Source:      "test",
		ProjectRoot: root,
		ContentHash: hash,
	}
}

func TestLedger_MergeAddsAndNoOps(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()

	warns := l.Merge([]monitor.ResolvedUnit{resolved("claude-code", "/proj/a", "h1", false)}, now)
	assert.Empty(t, warns)
	require.Len(t, l.Union(), 1)

	// Same content hash re-seed is a no-op (no warning, no duplicate).
	warns = l.Merge([]monitor.ResolvedUnit{resolved("claude-code", "/proj/a", "h1", false)}, now)
	assert.Empty(t, warns)
	require.Len(t, l.Union(), 1)
}

func TestLedger_SameRootDifferentHashUpdatesSilently(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()
	l.Merge([]monitor.ResolvedUnit{resolved("acme", "/proj/a", "h1", false)}, now)

	// Same project edits its own loose unit: different hash, same root → update,
	// no clobber warning.
	warns := l.Merge([]monitor.ResolvedUnit{resolved("acme", "/proj/a", "h2", false)}, now)
	assert.Empty(t, warns)
	require.Len(t, l.Union(), 1)
	assert.Equal(t, "h2", l.Union()[0].ContentHash)
}

func TestLedger_C5ClobberWarnsAcrossProjects(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()
	l.Merge([]monitor.ResolvedUnit{resolved("acme", "/proj/a", "h1", false)}, now)

	// A different project ships a different-content bare unit of the same name.
	warns := l.Merge([]monitor.ResolvedUnit{resolved("acme", "/proj/b", "h2", false)}, now)
	require.Len(t, warns, 1)
	assert.Equal(t, "acme", warns[0].Name)
	assert.Equal(t, "/proj/a", warns[0].PrevRoot)
	assert.Equal(t, "/proj/b", warns[0].NewRoot)
	// Overwrite proceeds (current-project-wins).
	assert.Equal(t, "/proj/b", l.Union()[0].ProjectRoot)
}

func TestLedger_QualifiedNeverClobbers(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()
	l.Merge([]monitor.ResolvedUnit{resolved("acme.tools.foo", "/proj/a", "h1", true)}, now)

	// A qualified (bundled) address is collision-proof by construction — even a
	// different-root different-hash re-seed does not warn.
	warns := l.Merge([]monitor.ResolvedUnit{resolved("acme.tools.foo", "/proj/b", "h2", true)}, now)
	assert.Empty(t, warns)
}

func TestLedger_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l := monitor.NewLedger()
	l.Merge([]monitor.ResolvedUnit{
		resolved("claude-code", "/proj/a", "h1", false),
		resolved("acme.tools.foo", "/proj/a", "h2", true),
	}, time.Unix(42, 0).UTC())
	require.NoError(t, l.Save(dir))

	reloaded, err := monitor.LoadLedger(dir)
	require.NoError(t, err)
	union := reloaded.Union()
	require.Len(t, union, 2)
	// Sorted by name.
	assert.Equal(t, "acme.tools.foo", union[0].Name)
	assert.Equal(t, "claude-code", union[1].Name)
	assert.Equal(t, "/proj/a", union[1].ProjectRoot)
	require.Len(t, union[1].Manifest.Logs, 1, "manifest snapshot survives the round trip")
	assert.Equal(t, "claude-code", union[1].Manifest.Logs[0].Index)
}

func TestLedger_LoadMissingIsEmpty(t *testing.T) {
	l, err := monitor.LoadLedger(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, l.Union())
}

// TestSeedLedger_MergesIntoCurrentDiskState pins the lost-update guarantee:
// SeedLedger folds into whatever is on disk AT SAVE TIME, so a seed persisted
// by another `monitor up` between this process's read and its write survives.
// A naive "save the in-memory ledger loaded at prepare time" implementation
// fails this test.
func TestSeedLedger_MergesIntoCurrentDiskState(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1, 0).UTC()

	seedA := []monitor.ResolvedUnit{resolved("from-proj-a", "/proj/a", "h1", false)}
	require.NoError(t, monitor.SeedLedger(t.Context(), dir, seedA, now))
	// Simulates a concurrent up from another project landing its seed after
	// this process loaded its render-time view.
	seedB := []monitor.ResolvedUnit{resolved("from-proj-b", "/proj/b", "h2", false)}
	require.NoError(t, monitor.SeedLedger(t.Context(), dir, seedB, now))

	l, err := monitor.LoadLedger(dir)
	require.NoError(t, err)
	union := l.Union()
	require.Len(t, union, 2)
	assert.Equal(t, "from-proj-a", union[0].Name)
	assert.Equal(t, "from-proj-b", union[1].Name)
}
