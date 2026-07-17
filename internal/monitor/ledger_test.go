package monitor_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/monitor"
)

// resolvedSrc builds a ResolvedUnit fixture for ledger-merge tests with an
// explicit source key. The backing unit carries a one-lane manifest so the
// snapshot recorded in the ledger is non-trivial; Merge never walks the
// artifact FS. Source keys are opaque to the ledger — tests use plain strings.
func resolvedSrc(name, root, hash, sourceKey string, qualified bool) monitor.ResolvedUnit {
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
		Source:         "test",
		SourceKey:      sourceKey,
		ProjectRoot:    root,
		ContentHash:    hash,
		ClusterObjects: nil,
	}
}

// resolved builds a bare-unit fixture whose source key follows the project
// root — the loose-project-tier shape, where each project root is its own
// content source.
func resolved(name, root, hash string) monitor.ResolvedUnit {
	return resolvedSrc(name, root, hash, "src:"+root, false)
}

func TestLedger_MergeAddsAndNoOps(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()

	require.NoError(t, l.Merge([]monitor.ResolvedUnit{resolved("claude-code", "/proj/a", "h1")}, now))
	require.Len(t, l.Union(), 1)

	// Same content hash re-seed is a no-op (no error, no duplicate).
	require.NoError(t, l.Merge([]monitor.ResolvedUnit{resolved("claude-code", "/proj/a", "h1")}, now))
	require.Len(t, l.Union(), 1)
}

func TestLedger_SameRootDifferentHashUpdatesSilently(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()
	require.NoError(t, l.Merge([]monitor.ResolvedUnit{resolved("acme", "/proj/a", "h1")}, now))

	// Same project edits its own loose unit: different hash, same root → update,
	// no collision.
	require.NoError(t, l.Merge([]monitor.ResolvedUnit{resolved("acme", "/proj/a", "h2")}, now))
	require.Len(t, l.Union(), 1)
	assert.Equal(t, "h2", l.Union()[0].ContentHash)
}

// TestLedger_SameRootDifferentSourceUpdates pins the local-override flow: a
// project that seeded the floor unit and then shadows it with its own loose
// copy re-seeds from a different source but the same root — an intentional
// local action, not a cross-project clobber.
func TestLedger_SameRootDifferentSourceUpdates(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()
	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("claude-code", "/proj/a", "h1", "floor", false)}, now))

	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("claude-code", "/proj/a", "h2", "project:/proj/a", false)}, now))
	require.Len(t, l.Union(), 1)
	assert.Equal(t, "h2", l.Union()[0].ContentHash)
	assert.Equal(t, "project:/proj/a", l.Union()[0].SourceKey)
}

// TestLedger_SharedSourceUpgradesAcrossProjects: a bare unit backed by a
// host-global source (the embedded floor, the shared user dir) is the SAME
// unit no matter which project seeds it. A content change from a different
// project root — a CLI upgrade rolling new floor artifacts, a user editing the
// shared dir — is an in-place update, never a collision.
func TestLedger_SharedSourceUpgradesAcrossProjects(t *testing.T) {
	t.Run("floor upgrade re-seeded from another project", func(t *testing.T) {
		l := monitor.NewLedger()
		now := time.Unix(1, 0).UTC()
		require.NoError(t, l.Merge(
			[]monitor.ResolvedUnit{resolvedSrc("claude-code", "/proj/a", "h-old", "floor", false)}, now))

		// CLI upgraded; project B reloads first.
		require.NoError(t, l.Merge(
			[]monitor.ResolvedUnit{resolvedSrc("claude-code", "/proj/b", "h-new", "floor", false)}, now))
		union := l.Union()
		require.Len(t, union, 1)
		assert.Equal(t, "h-new", union[0].ContentHash)
		assert.Equal(t, "/proj/b", union[0].ProjectRoot)
	})

	t.Run("no-project seed then in-project seed", func(t *testing.T) {
		l := monitor.NewLedger()
		now := time.Unix(1, 0).UTC()
		require.NoError(t, l.Merge(
			[]monitor.ResolvedUnit{resolvedSrc("claude-code", "", "h-old", "floor", false)}, now))
		require.NoError(t, l.Merge(
			[]monitor.ResolvedUnit{resolvedSrc("claude-code", "/proj/a", "h-new", "floor", false)}, now))
		require.Len(t, l.Union(), 1)
		assert.Equal(t, "h-new", l.Union()[0].ContentHash)
	})

	t.Run("shared user dir edited between projects", func(t *testing.T) {
		l := monitor.NewLedger()
		now := time.Unix(1, 0).UTC()
		userSrc := "user:/home/u/.config/clawker/monitoring/acme"
		require.NoError(t, l.Merge(
			[]monitor.ResolvedUnit{resolvedSrc("acme", "/proj/a", "h1", userSrc, false)}, now))
		require.NoError(t, l.Merge(
			[]monitor.ResolvedUnit{resolvedSrc("acme", "/proj/b", "h2", userSrc, false)}, now))
		require.Len(t, l.Union(), 1)
		assert.Equal(t, "h2", l.Union()[0].ContentHash)
	})
}

func TestLedger_CollisionRefusesSeedAcrossProjects(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()
	require.NoError(t, l.Merge([]monitor.ResolvedUnit{resolved("acme", "/proj/a", "h1")}, now))

	// A different project ships a different-content bare unit of the same name
	// from its OWN source (loose dir): hard error, ledger untouched.
	err := l.Merge([]monitor.ResolvedUnit{resolved("acme", "/proj/b", "h2")}, now)
	var collision *monitor.SeedCollisionError
	require.ErrorAs(t, err, &collision)
	assert.Equal(t, "acme", collision.Name)
	assert.Equal(t, "/proj/a", collision.PrevRoot)
	assert.Equal(t, "/proj/b", collision.NewRoot)
	assert.Contains(t, err.Error(), "monitor down --volumes")
	assert.Contains(
		t,
		err.Error(),
		"loose extension",
		"the remedy must be followable — with host-global sources updating in place, a collision always involves a loose extension that can be renamed or removed",
	)
	assert.Equal(t, "/proj/a", l.Union()[0].ProjectRoot, "the refused seed must not land")
	assert.Equal(t, "h1", l.Union()[0].ContentHash)
}

func TestLedger_CollisionLeavesWholeBatchUnapplied(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()
	require.NoError(t, l.Merge([]monitor.ResolvedUnit{resolved("acme", "/proj/a", "h1")}, now))

	// A batch mixing a fresh unit with a colliding one is refused atomically —
	// the fresh unit must not be half-applied before the collision is noticed.
	err := l.Merge([]monitor.ResolvedUnit{
		resolved("fresh", "/proj/b", "h9"),
		resolved("acme", "/proj/b", "h2"),
	}, now)
	var collision *monitor.SeedCollisionError
	require.ErrorAs(t, err, &collision)
	require.Len(t, l.Union(), 1, "no unit from the refused batch may land")
}

// TestLedger_QualifiedPinsCoexist: one qualified address carrying two
// different contents on one host (two projects pinning one repository
// differently) seeds SIBLING ledger entries — one project's re-pin or
// divergent pin never overwrites (unresolves) another project's routing.
func TestLedger_QualifiedPinsCoexist(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()
	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("acme.tools.alpha", "/proj/a", "h1", "bundle:/cache/k1", true)}, now))

	// Project B pins a different value of the same address.
	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("acme.tools.alpha", "/proj/b", "h2", "bundle:/cache/k2", true)}, now))

	union := l.Union()
	require.Len(t, union, 2, "both pins' contents stay seeded")
	assert.Equal(t, "acme.tools.alpha", union[0].Name)
	assert.Equal(t, "acme.tools.alpha", union[1].Name)
	hashes := []string{union[0].ContentHash, union[1].ContentHash}
	assert.ElementsMatch(t, []string{"h1", "h2"}, hashes,
		"project A's pinned content survives project B's different-value seed")
}

// TestLedger_QualifiedRePinReplacesOwnStaleEntry pins the upgrade path: a
// project changing its own pin replaces ITS prior entry instead of leaving a
// stale sibling that would fight the new pin over unchanged index names.
func TestLedger_QualifiedRePinReplacesOwnStaleEntry(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()
	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("acme.tools.alpha", "/proj/a", "h1", "bundle:/cache/k1", true)}, now))
	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("acme.tools.alpha", "/proj/a", "h2", "bundle:/cache/k2", true)}, now))

	union := l.Union()
	require.Len(t, union, 1, "the project's stale pin must not linger")
	assert.Equal(t, "h2", union[0].ContentHash)

	// A foreign project's sibling pin is NOT replaced by this project's re-pin.
	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("acme.tools.alpha", "/proj/b", "h3", "bundle:/cache/k3", true)}, now))
	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("acme.tools.alpha", "/proj/a", "h4", "bundle:/cache/k4", true)}, now))
	union = l.Union()
	require.Len(t, union, 2)
	hashes := []string{union[0].ContentHash, union[1].ContentHash}
	assert.ElementsMatch(t, []string{"h3", "h4"}, hashes)
}

// TestLedger_QualifiedIdenticalContentSiblingsBothRecorded: two declared
// values that fetch identical content (an ssh vs https spelling of one repo)
// each keep their OWN ledger entry. Deduping them would make one project's
// entry the other's silent proxy — the owner's later re-pin would retire it
// and unroute the dependent project's lanes (the exact silent-lane-loss the
// value-keyed ledger exists to prevent). Identical-content siblings cost
// nothing: every lane fingerprint and object digest matches, so validation
// passes and routing dedupes.
func TestLedger_QualifiedIdenticalContentSiblingsBothRecorded(t *testing.T) {
	l := monitor.NewLedger()
	now := time.Unix(1, 0).UTC()
	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("acme.tools.alpha", "/proj/a", "h1", "bundle:/cache/ssh", true)}, now))
	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("acme.tools.alpha", "/proj/b", "h1", "bundle:/cache/https", true)}, now))

	union := l.Union()
	require.Len(t, union, 2, "each declared value owns its entry")
	require.NoError(t, monitor.ValidateSeededSet(union),
		"identical-content siblings must validate trivially")

	// The decisive scenario: A re-pins to new content. B's identical-content
	// entry must survive — B's agents keep routing.
	require.NoError(t, l.Merge(
		[]monitor.ResolvedUnit{resolvedSrc("acme.tools.alpha", "/proj/a", "h2", "bundle:/cache/k2", true)}, now))
	union = l.Union()
	require.Len(t, union, 2, "A's re-pin must not unroute B")
	byHash := map[string]string{}
	for _, u := range union {
		byHash[u.ContentHash] = u.ProjectRoot
	}
	assert.Equal(t, "/proj/b", byHash["h1"], "the surviving h1 entry is B's")
	assert.Equal(t, "/proj/a", byHash["h2"])
}

// TestLedger_SourcelessEntriesGetNoSpecialTreatment: no released clawker ever
// wrote a ledger without source keys (the ledger itself ships in this branch),
// so a source_key-less entry is hand-edited or corrupt state — never a
// migration case. It must NOT be an escape hatch past the safety rules: the
// empty key matches no real source, so the project-root comparison governs
// exactly as it does for any foreign-source entry.
func TestLedger_SourcelessEntriesGetNoSpecialTreatment(t *testing.T) {
	load := func(t *testing.T) *monitor.Ledger {
		t.Helper()
		dir := t.TempDir()
		sourceless := `
units:
  acme:
    name: acme
    source: project (/proj/a/.clawker/monitoring/acme)
    project_root: /proj/a
    content_hash: h-old
    manifest:
      logs:
        - index: acme
          service_names: [acme]
    seeded_at: 2026-01-01T00:00:00Z
  acme.tools.alpha:
    name: acme.tools.alpha
    source: bundle acme.tools
    project_root: /proj/a
    content_hash: h-old
    manifest:
      logs:
        - index: alpha
          service_names: [alpha]
    seeded_at: 2026-01-01T00:00:00Z
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, monitor.UnitsLedgerFile), []byte(sourceless), 0o644))
		l, err := monitor.LoadLedger(dir)
		require.NoError(t, err)
		return l
	}
	now := time.Unix(2, 0).UTC()

	t.Run("bare from another project is refused, not silently overwritten", func(t *testing.T) {
		l := load(t)
		err := l.Merge([]monitor.ResolvedUnit{
			resolvedSrc("acme", "/proj/b", "h-new", "project:/proj/b/.clawker/monitoring/acme", false),
		}, now)
		var collision *monitor.SeedCollisionError
		require.ErrorAs(t, err, &collision)
		assert.Equal(t, "h-old", unionByName(l, "acme")[0].ContentHash,
			"the sourceless entry must not be clobbered")
	})

	t.Run("bare from the same root updates and stamps the source", func(t *testing.T) {
		l := load(t)
		require.NoError(t, l.Merge([]monitor.ResolvedUnit{
			resolvedSrc("acme", "/proj/a", "h-new", "project:/proj/a/.clawker/monitoring/acme", false),
		}, now))
		entries := unionByName(l, "acme")
		require.Len(t, entries, 1)
		assert.Equal(t, "h-new", entries[0].ContentHash)
		assert.NotEmpty(t, entries[0].SourceKey)
	})

	t.Run("qualified from another project coexists, never silently retired", func(t *testing.T) {
		l := load(t)
		require.NoError(t, l.Merge([]monitor.ResolvedUnit{
			resolvedSrc("acme.tools.alpha", "/proj/b", "h-new", "bundle:/cache/k1", true),
		}, now))
		assert.Len(t, unionByName(l, "acme.tools.alpha"), 2,
			"the sourceless entry survives a foreign project's seed")
	})

	t.Run("qualified from the same root is retired as a stale own pin", func(t *testing.T) {
		l := load(t)
		require.NoError(t, l.Merge([]monitor.ResolvedUnit{
			resolvedSrc("acme.tools.alpha", "/proj/a", "h-new", "bundle:/cache/k1", true),
		}, now))
		entries := unionByName(l, "acme.tools.alpha")
		require.Len(t, entries, 1, "the same project's sourceless pin is replaced")
		assert.Equal(t, "h-new", entries[0].ContentHash)
	})
}

// unionByName filters the ledger union to entries of one name.
func unionByName(l *monitor.Ledger, name string) []monitor.SeededUnit {
	var out []monitor.SeededUnit
	for _, u := range l.Union() {
		if u.Name == name {
			out = append(out, u)
		}
	}
	return out
}

func TestLedger_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l := monitor.NewLedger()
	require.NoError(t, l.Merge([]monitor.ResolvedUnit{
		resolved("claude-code", "/proj/a", "h1"),
		resolvedSrc("acme.tools.foo", "/proj/a", "h2", "bundle:/cache/k1", true),
	}, time.Unix(42, 0).UTC()))
	require.NoError(t, l.Save(dir))

	reloaded, err := monitor.LoadLedger(dir)
	require.NoError(t, err)
	union := reloaded.Union()
	require.Len(t, union, 2)
	// Sorted by name.
	assert.Equal(t, "acme.tools.foo", union[0].Name)
	assert.Equal(t, "bundle:/cache/k1", union[0].SourceKey, "source identity survives the round trip")
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

	seedA := []monitor.ResolvedUnit{resolved("from-proj-a", "/proj/a", "h1")}
	require.NoError(t, monitor.SeedLedger(t.Context(), dir, seedA, now))
	// Simulates a concurrent up from another project landing its seed after
	// this process loaded its render-time view.
	seedB := []monitor.ResolvedUnit{resolved("from-proj-b", "/proj/b", "h2")}
	require.NoError(t, monitor.SeedLedger(t.Context(), dir, seedB, now))

	l, err := monitor.LoadLedger(dir)
	require.NoError(t, err)
	union := l.Union()
	require.Len(t, union, 2)
	assert.Equal(t, "from-proj-a", union[0].Name)
	assert.Equal(t, "from-proj-b", union[1].Name)
}

// TestSeedLedger_RefusesResourceConflictAtAuthoritativeMerge: the flock-guarded
// authoritative merge re-validates the post-merge union, so a resource conflict
// seeded by a concurrent up between the caller's pre-render check and this
// merge is refused rather than persisted.
func TestSeedLedger_RefusesResourceConflictAtAuthoritativeMerge(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1, 0).UTC()

	// Another project seeded a unit claiming index "shared-idx".
	first := resolvedSrc("acme.tools.alpha", "/proj/a", "h1", "bundle:/cache/k1", true)
	first.Unit.Manifest.Logs = []config.MonitoringLogLane{
		{Index: "shared-idx", ServiceNames: []string{"svc-a"}, Retention: ""},
	}
	require.NoError(t, monitor.SeedLedger(t.Context(), dir, []monitor.ResolvedUnit{first}, now))

	// This process's projection claims the same index under a different unit.
	second := resolvedSrc("evil.pkg.alpha", "/proj/b", "h2", "bundle:/cache/k2", true)
	second.Unit.Manifest.Logs = []config.MonitoringLogLane{
		{Index: "shared-idx", ServiceNames: []string{"svc-b"}, Retention: ""},
	}
	err := monitor.SeedLedger(t.Context(), dir, []monitor.ResolvedUnit{second}, now)
	require.ErrorContains(t, err, "shared-idx")

	l, loadErr := monitor.LoadLedger(dir)
	require.NoError(t, loadErr)
	require.Len(t, l.Union(), 1, "the conflicting seed must not persist")
}
