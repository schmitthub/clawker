package e2e_test

import (
	"fmt"
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

// TestMonitorSeedUnionAndCollision_E2E proves the monitoring seed model across
// two projects sharing one host: `monitor up` is bring-up only — it seeds the
// cwd's projection on bring-up and short-circuits untouched when the stack is
// already running; applying a projection to a running stack is `monitor
// reload`'s job. A second project selecting a same-named bare extension with
// DIFFERENT content is a hard seed-collision error at reload (the seed is refused), while
// an identical-content re-seed from another project is a no-op. `monitor down
// --volumes` resets the ledger.
//
// The two projects deliberately share ONE isolated data dir (the host ledger and
// rendered collector config are host-global, not per-project) — that shared
// state IS the host-global seed-union surface. Runs at host UAT only; excluded from
// `make test` by directory.
func TestMonitorSeedUnionAndCollision_E2E(t *testing.T) {
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
	// different content hash — the exact seed-collision condition.
	projB := filepath.Join(setup.Env.Dirs.Base, "monitor-proj-b")
	require.NoError(t, os.MkdirAll(projB, 0o755))
	materializeTweakedClaudeCodeExtension(t, projB)
	setup.Chdir(t, projB)

	initB := h.Run("project", "init", "monitor-proj-b", "--yes", "--preset", "Bare", "--vcs", "github")
	require.NoError(t, initB.Err, "init B failed\nstdout: %s\nstderr: %s", initB.Stdout, initB.Stderr)
	selectClaudeCodeExtension(t, projB)

	// up is bring-up only: against the running stack it short-circuits — B's
	// divergent copy is invisible to bring-up. This is the strong variant of
	// the assertion: were up to fall through and seed, B's projection would
	// turn the accidental seed into a seed-collision error.
	upB := h.Run("monitor", "up")
	require.NoError(t, upB.Err, "monitor up (B) on a running stack failed\nstdout: %s\nstderr: %s",
		upB.Stdout, upB.Stderr)
	assert.Contains(t, upB.Stdout, "already up",
		"a running stack must short-circuit regardless of the cwd projection")

	// Collision: B's same-named, different-content claude-code refuses to seed at
	// reload — typed hard error, collector untouched (the refusal fires
	// before the disruptive apply).
	beforeID := collectorContainerID(t)
	require.NotEmpty(t, beforeID, "collector must be running before the refused reload")
	reloadB := h.Run("monitor", "reload")
	var collErr *internalmonitor.SeedCollisionError
	require.ErrorAs(t, reloadB.Err, &collErr, "monitor reload (B) must refuse with a seed-collision error")
	assert.NotEmpty(t, reloadB.Stderr, "the collision error must be user-visible")
	assert.Equal(t, beforeID, collectorContainerID(t),
		"a refused reload must not touch the running collector")

	// Recovery: drop B's divergent loose copy — the selection falls back to the
	// floor claude-code, whose content hash matches A's seed, so the re-seed
	// from a different project root is an identical-content no-op. The same
	// reload is the explicit disruptive apply: it recreates the collector
	// against the re-rendered union config while the rest of the stack stays up.
	require.NoError(t, os.RemoveAll(
		filepath.Join(projB, consts.DotClawkerDir, bundle.ComponentMonitoring.Dir(), "claude-code")))
	reloadRes := h.Run("monitor", "reload")
	require.NoError(
		t,
		reloadRes.Err,
		"monitor reload (B) after removing the divergent copy failed\nstdout: %s\nstderr: %s",
		reloadRes.Stdout,
		reloadRes.Stderr,
	)
	afterID := collectorContainerID(t)
	require.NotEmpty(t, afterID, "collector must be running after reload")
	assert.NotEqual(t, beforeID, afterID, "reload must recreate the collector container")

	// Union: give B its OWN loose extension and select ONLY it — after this
	// reload, claude-code routing in the rendered collector config can come
	// from nothing but the ledger union (B's cwd projection never selects
	// it). This is the seed-union discriminator: one project's seed survives
	// another project's reload.
	materializeMinimalExtension(t, projB, "uatunion")
	rewriteExtensionSelection(t, projB, "[claude-code]", "[uatunion]")
	reloadUnion := h.Run("monitor", "reload")
	require.NoError(t, reloadUnion.Err, "monitor reload (B, uatunion) failed\nstdout: %s\nstderr: %s",
		reloadUnion.Stdout, reloadUnion.Stderr)

	monitorDir, err := consts.MonitorSubdir()
	require.NoError(t, err)
	otelCfg, err := os.ReadFile(filepath.Join(monitorDir, internalmonitor.OtelConfigFileName))
	require.NoError(t, err, "rendered collector config must exist after reload")
	assert.Contains(t, string(otelCfg), "uatunion",
		"the collector config must route the extension B just seeded")
	assert.Contains(
		t,
		string(otelCfg),
		"claude-code",
		"the collector config must keep routing A's claude-code seed — the ledger union, not B's projection, is the render source",
	)

	// down --volumes resets the seeded-unit ledger.
	downRes := h.Run("monitor", "down", "--volumes")
	require.NoError(t, downRes.Err, "monitor down --volumes failed\nstderr: %s", downRes.Stderr)
	assert.NoFileExists(t, filepath.Join(monitorDir, internalmonitor.UnitsLedgerFile),
		"down --volumes must reset the units ledger")
}

// materializeTweakedClaudeCodeExtension copies the embedded floor claude-code
// monitoring extension into projectDir's loose convention dir, tweaking the
// manifest description so its content hash differs from the floor's — making a
// bare-name reseed from this project a genuine same-name/different-content collision rather than an
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

	// Fail at the source if the tweak target drifted — an unapplied tweak
	// makes the copy hash-identical to the floor and silently degrades the
	// collision leg into a no-op three commands later.
	manifest, err := os.ReadFile(filepath.Join(dst, "monitoring.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(manifest), "Team-tweaked",
		"the description tweak must have applied")
}

// materializeMinimalExtension writes a minimal valid loose monitoring
// extension named name into projectDir's convention dir: one log lane plus
// the lane's index template. The template carries mappings — the bootstrap
// container hard-fails a template that leaves its pre-created index unmapped.
func materializeMinimalExtension(t *testing.T, projectDir, name string) {
	t.Helper()
	dir := filepath.Join(projectDir, consts.DotClawkerDir, bundle.ComponentMonitoring.Dir(), name)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, internalmonitor.MonitoringDirIndexTemplates), 0o755))
	manifest := fmt.Sprintf(
		"description: e2e union extension\n\nlogs:\n  - index: %s\n    service_names: [%s]\n",
		name, name,
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, internalmonitor.MonitoringUnitManifestFile), []byte(manifest), 0o600))
	tpl := fmt.Sprintf(
		`{"index_patterns":[%q],"template":{"settings":{"number_of_shards":1},"mappings":{"properties":{"@timestamp":{"type":"date"},"message":{"type":"text"}}}}}`,
		name,
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, internalmonitor.MonitoringDirIndexTemplates, name+".json"), []byte(tpl), 0o600))
}

// rewriteExtensionSelection swaps the monitor.extensions value previously
// appended by selectClaudeCodeExtension — appending a second monitor: block
// would be a duplicate yaml key, so the selection is edited in place.
func rewriteExtensionSelection(t *testing.T, projectDir, from, to string) {
	t.Helper()
	path := filepath.Join(projectDir, "."+consts.ProjectConfigFile)
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read project config for selection rewrite")
	updated := strings.Replace(string(raw), "extensions: "+from, "extensions: "+to, 1)
	require.NotEqual(t, string(raw), updated, "selection rewrite must change the config")
	require.NoError(t, os.WriteFile(path, []byte(updated), 0o600))
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
		"docker", "ps", "-q", "--filter", "name=^"+consts.MonitoringServiceOtelCollector+"$",
	).Output()
	require.NoError(t, err, "docker ps for otel-collector")
	return strings.TrimSpace(string(out))
}
