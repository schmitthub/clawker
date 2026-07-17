package bundle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/testenv"
)

// sweepTestManager wires a Manager with an empty-but-present roots provider
// (GC on, nothing declared) and a no-op component validator — the staging
// sweep never loads components.
func sweepTestManager() *Manager {
	cfg := configmocks.NewBlankConfig()
	cfg.ProjectRootFunc = func() string { return "" }
	return NewManager(cfg, func(Component) error { return nil },
		WithRegisteredRoots(func(context.Context) ([]string, error) { return nil, nil }))
}

// ageStaged backdates a staging dir past the abandonment threshold.
func ageStaged(t *testing.T, dir string) {
	t.Helper()
	old := time.Now().Add(-2 * stagingSweepAge)
	require.NoError(t, os.Chtimes(dir, old, old))
}

// makeHolding builds a retire holding dir: the origin sidecar (when origin is
// non-empty) and optionally the retired entry tree.
func makeHolding(t *testing.T, stage, name, origin string, withEntry bool) string {
	t.Helper()
	holding := filepath.Join(stage, name)
	require.NoError(t, os.MkdirAll(holding, 0o750))
	if origin != "" {
		require.NoError(t, os.WriteFile(filepath.Join(holding, retiredOriginFile), []byte(origin), 0o600))
	}
	if withEntry {
		require.NoError(t, os.MkdirAll(filepath.Join(holding, retiredName), 0o750))
		require.NoError(t, os.WriteFile(
			filepath.Join(holding, retiredName, "marker.txt"), []byte("retired"), 0o600))
	}
	return holding
}

// Prune reclaims abandoned staging trees under .tmp: a retired entry whose
// origin slot is still empty is RESTORED (it may be the only copy of a
// previously serving entry), a superseded retired copy and crash debris are
// discarded, and anything younger than the abandonment threshold — a live
// install's staging — is never touched.
func TestPrune_SweepsAbandonedStaging(t *testing.T) {
	testenv.New(t)
	root, err := cacheRoot()
	require.NoError(t, err)
	stage := filepath.Join(root, tmpDir)
	require.NoError(t, os.MkdirAll(stage, 0o750))

	// (i) Restorable: sidecar + entry tree, origin slot absent.
	srcRestore := Source{URL: "https://x/tools.git", Ref: "v1", SHA: "", Path: ""}
	originRestore := filepath.Join(root, "acme", "tools", srcRestore.Key())
	restorable := makeHolding(t, stage, retireStagePrefix+"restore", originRestore, true)
	ageStaged(t, restorable)

	// (ii) Superseded: origin slot occupied by a fresher entry.
	srcCurrent := Source{URL: "https://x/other.git", Ref: "v1", SHA: "", Path: ""}
	originCurrent := filepath.Join(root, "acme", "other", srcCurrent.Key())
	require.NoError(t, os.MkdirAll(originCurrent, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(originCurrent, "current.txt"), []byte("current"), 0o600))
	superseded := makeHolding(t, stage, retireStagePrefix+"superseded", originCurrent, true)
	ageStaged(t, superseded)

	// (iii) Crash debris: sidecar with no entry tree.
	debris := makeHolding(t, stage, retireStagePrefix+"debris", filepath.Join(root, "x", "y", "z"), false)
	ageStaged(t, debris)

	// A sidecar pointing outside the cache is not acted on — neither restored
	// nor deleted.
	foreign := makeHolding(t, stage, retireStagePrefix+"foreign", t.TempDir(), true)
	ageStaged(t, foreign)

	// Abandoned clone/content stages are discarded; fresh ones are a live
	// install's working set and stay.
	oldClone := filepath.Join(stage, cloneStagePrefix+"old")
	require.NoError(t, os.MkdirAll(oldClone, 0o750))
	ageStaged(t, oldClone)
	oldContent := filepath.Join(stage, contentStagePrefix+"old")
	require.NoError(t, os.MkdirAll(oldContent, 0o750))
	ageStaged(t, oldContent)
	freshClone := filepath.Join(stage, cloneStagePrefix+"fresh")
	require.NoError(t, os.MkdirAll(freshClone, 0o750))
	freshRetire := makeHolding(t, stage, retireStagePrefix+"fresh", originRestore, true)

	report, err := sweepTestManager().Prune(context.Background())
	require.NoError(t, err)

	// (i) restored into its entry slot, holding dir gone.
	assert.FileExists(t, filepath.Join(originRestore, "marker.txt"))
	assert.NoDirExists(t, restorable)
	assert.Contains(t, joinWarningMessages(report.Warnings), "restored")

	// (ii) superseded copy discarded, the serving entry untouched.
	assert.NoDirExists(t, superseded)
	assert.FileExists(t, filepath.Join(originCurrent, "current.txt"))

	// (iii) debris discarded.
	assert.NoDirExists(t, debris)

	// Foreign origin: left in place, surfaced.
	assert.DirExists(t, foreign)
	assert.Contains(t, joinWarningMessages(report.Warnings), "outside the bundle cache")

	// Old clone/content stages discarded; fresh staging untouched.
	assert.NoDirExists(t, oldClone)
	assert.NoDirExists(t, oldContent)
	assert.DirExists(t, freshClone)
	assert.DirExists(t, freshRetire)
}

// joinWarningMessages flattens warning messages for substring assertions
// (the external test helper lives in the bundle_test package).
func joinWarningMessages(warnings []Warning) string {
	msgs := make([]string, 0, len(warnings))
	for _, w := range warnings {
		msgs = append(msgs, w.Message)
	}
	return strings.Join(msgs, "\n")
}
