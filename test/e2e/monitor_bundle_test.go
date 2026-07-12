package e2e_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/consts"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

// TestMonitorOptionD_SeedUnionAndC5_E2E proves the monitoring seed model across
// two projects sharing one host: `monitor up` idempotently seeds each cwd's
// projection into the host units ledger and regenerates the collector config
// over the ledger union, and a second project selecting a same-named bare
// extension with different content C5-clobbers it (current-project-wins) with a
// user-visible warning. `monitor down --volumes` resets the ledger.
//
// The two projects deliberately share ONE isolated data dir (the host ledger and
// rendered collector config are host-global, not per-project) — that shared
// state IS the option-D surface. Runs at host UAT only; excluded from
// `make test` by directory.
func TestMonitorOptionD_SeedUnionAndC5_E2E(t *testing.T) {
	h := &harness.Harness{
		T:       t,
		Opts:    bundleHarnessOpts(),
		Cleanup: nil,
	}
	// Project A: explicitly selects the floor claude-code extension (there is
	// no default selection — extensions are opt-in).
	setup := h.NewIsolatedFS(&harness.FSOptions{ProjectDir: "monitor-proj-a"})

	initA := h.Run("project", "init", "monitor-proj-a", "--yes", "--preset", "Bare", "--vcs", "github")
	require.NoError(t, initA.Err, "init A failed\nstdout: %s\nstderr: %s", initA.Stdout, initA.Stderr)
	selectClaudeCodeExtension(t, filepath.Join(setup.Env.Dirs.Base, "monitor-proj-a"))

	upA := h.Run("monitor", "up")
	require.NoError(t, upA.Err, "monitor up (A) failed\nstdout: %s\nstderr: %s", upA.Stdout, upA.Stderr)
	t.Cleanup(func() { _ = h.Run("monitor", "down", "--volumes") })

	// Project B: a loose claude-code extension of DIFFERENT content (a tweaked
	// copy of the floor) — a same bare name from a different project root with a
	// different content hash, the exact C5 condition.
	projB := filepath.Join(setup.Env.Dirs.Base, "monitor-proj-b")
	require.NoError(t, os.MkdirAll(projB, 0o755))
	materializeTweakedClaudeCodeExtension(t, projB)
	setup.Chdir(t, projB)

	initB := h.Run("project", "init", "monitor-proj-b", "--yes", "--preset", "Bare", "--vcs", "github")
	require.NoError(t, initB.Err, "init B failed\nstdout: %s\nstderr: %s", initB.Stdout, initB.Stderr)
	selectClaudeCodeExtension(t, projB)

	upB := h.Run("monitor", "up")
	require.NoError(t, upB.Err, "monitor up (B) failed\nstdout: %s\nstderr: %s", upB.Stdout, upB.Stderr)

	// C5: B's up warns that its claude-code overwrites the same-named unit A
	// seeded, and proceeds (current-project-wins).
	assert.Contains(t, upB.Stderr, "overwrites the same-named unit",
		"a same-named bare extension from another project must C5-warn")

	// Union: the host collector config is regenerated over the ledger union and
	// still serves the claude-code routing.
	monitorDir, err := consts.MonitorSubdir()
	require.NoError(t, err)
	otelCfg, err := os.ReadFile(filepath.Join(monitorDir, internalmonitor.OtelConfigFileName))
	require.NoError(t, err, "rendered collector config must exist after monitor up")
	assert.Contains(t, string(otelCfg), "claude-code",
		"the collector config must route the seeded claude-code extension")

	// reload: the explicit disruptive apply — recreates the collector against
	// the re-rendered config while the rest of the stack stays up.
	beforeID := collectorContainerID(t)
	require.NotEmpty(t, beforeID, "collector must be running before reload")
	reloadRes := h.Run("monitor", "reload")
	require.NoError(t, reloadRes.Err, "monitor reload failed\nstdout: %s\nstderr: %s",
		reloadRes.Stdout, reloadRes.Stderr)
	afterID := collectorContainerID(t)
	require.NotEmpty(t, afterID, "collector must be running after reload")
	assert.NotEqual(t, beforeID, afterID, "reload must recreate the collector container")

	// down --volumes resets the seeded-unit ledger.
	downRes := h.Run("monitor", "down", "--volumes")
	require.NoError(t, downRes.Err, "monitor down --volumes failed\nstderr: %s", downRes.Stderr)
	assert.NoFileExists(t, filepath.Join(monitorDir, internalmonitor.UnitsLedgerFile),
		"down --volumes must reset the units ledger")
}

// materializeTweakedClaudeCodeExtension copies the embedded floor claude-code
// monitoring extension into projectDir's loose convention dir, tweaking the
// manifest description so its content hash differs from the floor's — making a
// bare-name reseed from this project a genuine C5 clobber rather than an
// identical no-op.
func materializeTweakedClaudeCodeExtension(t *testing.T, projectDir string) {
	t.Helper()
	floor, err := bundle.FloorFS(bundle.ComponentMonitoring, "claude-code")
	require.NoError(t, err)

	dst := filepath.Join(projectDir, consts.DotClawkerDir, bundle.ComponentMonitoring.Dir(), "claude-code")
	require.NoError(t, os.MkdirAll(dst, 0o755))

	walkErr := fs.WalkDir(floor, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		target := filepath.Join(dst, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, readErr := fs.ReadFile(floor, path)
		if readErr != nil {
			return readErr
		}
		if path == "monitoring.yaml" {
			content = []byte(strings.Replace(string(content),
				"description: Claude Code telemetry",
				"description: Team-tweaked Claude Code telemetry", 1))
		}
		return os.WriteFile(target, content, 0o600)
	})
	require.NoError(t, walkErr, "materializing loose claude-code extension")
}

// selectClaudeCodeExtension appends the opt-in claude-code selection to the
// project's .clawker.yaml — extensions have no default selection, so each
// project under test declares its own.
func selectClaudeCodeExtension(t *testing.T, projectDir string) {
	t.Helper()
	path := filepath.Join(projectDir, "."+consts.ProjectConfigFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err, "open project config for extension selection")
	defer func() {
		require.NoError(t, f.Close())
	}()
	_, err = f.WriteString("monitor:\n  extensions: [claude-code]\n")
	require.NoError(t, err, "append monitor.extensions selection")
}

// collectorContainerID returns the running otel-collector container's ID —
// the discriminator for "reload recreated the collector" (new ID) vs "it kept
// the old container" (same ID).
func collectorContainerID(t *testing.T) string {
	t.Helper()
	out, err := exec.Command(
		"docker", "ps", "-q", "--filter", "name="+consts.MonitoringServiceOtelCollector,
	).Output()
	require.NoError(t, err, "docker ps for otel-collector")
	return strings.TrimSpace(string(out))
}
