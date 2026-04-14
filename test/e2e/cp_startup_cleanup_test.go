package e2e

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// TestCPStartupCleanup_E2E verifies INV-B2-013: the CP clears orphaned
// eBPF state at startup so a previous crashed CP can't leak
// enforcement-relevant state (stale bypass flags) across restarts.
//
// Scenario:
//  1. Start the CP so eBPF programs are loaded + pinned.
//  2. Seed bypass_map with an entry whose cgroup id has no container_map
//     row — this is exactly the orphan case CleanupStaleBypass targets
//     (a dead-container bypass that a re-used cgroup id could inherit).
//  3. Restart the CP.
//  4. Assert the seeded entry is gone after /healthz reports ready.
//
// Seeding goes through the `ebpf-manager` break-glass CLI baked into
// the CP image — no other tool can mutate pinned maps safely.
//
// Runs only on a host with a real Docker daemon outside the clawker
// agent container.
func TestCPStartupCleanup_E2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := configmocks.NewIsolatedTestConfig(t)
	log := logger.Nop()

	dc, err := docker.NewClient(ctx, cfg, log)
	require.NoError(t, err, "constructing docker client")
	t.Cleanup(func() { _ = dc.Close() })

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := controlplane.Stop(cleanupCtx, dc); err != nil {
			t.Logf("cleanup: controlplane.Stop: %v", err)
		}
	})

	require.NoError(t, controlplane.EnsureRunning(ctx, dc, cfg, log), "first EnsureRunning")

	// Pick a cgroup id that is guaranteed to have no corresponding
	// container_map entry — 0xDEAD is an obvious sentinel, large enough
	// to never collide with a real inode. Writing to bypass_map directly
	// goes through the break-glass binary shipped inside the CP image.
	const orphanCgroupID = "57005" // 0xDEAD
	seedOut, seedErr, err := execInCP(ctx, "ebpf-manager", "bypass", orphanCgroupID)
	require.NoError(t, err, "seeding orphan bypass entry (stderr=%s)", seedErr)
	_ = seedOut

	// Restart the CP: stop + re-run EnsureRunning. The stop path tears
	// the container down cleanly; the next EnsureRunning reloads the
	// pinned maps via Manager.Load and then — by main.go wiring —
	// invokes CleanupStaleBypass, which must remove the 0xDEAD entry.
	require.NoError(t, controlplane.Stop(ctx, dc), "restarting CP: Stop")
	require.NoError(t, controlplane.EnsureRunning(ctx, dc, cfg, log), "restarting CP: EnsureRunning")

	// Give the CP a moment to settle post-/healthz.
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(2 * time.Second):
	}

	// Query bypass state through the break-glass CLI. The seeded id
	// must no longer be present.
	checkOut, checkErr, err := execInCP(ctx, "ebpf-manager", "bypass-status", orphanCgroupID)
	require.NoError(t, err, "inspecting bypass state after restart (stderr=%s)", checkErr)
	assert.NotContains(t, strings.ToLower(checkOut), "bypass=true",
		"orphan bypass entry must be cleared at startup (INV-B2-013)")
}

// execInCP runs `docker exec <clawker-controlplane> <cmd...>` and
// returns stdout, stderr, and the underlying exec error. Uses the CLI
// rather than the Docker SDK to keep the test hermetic — the SDK's exec
// flow differs by platform (see Docker Desktop socket-mount gotchas in
// the repo root CLAUDE.md).
func execInCP(ctx context.Context, cmd ...string) (string, string, error) {
	args := append([]string{"exec", consts.ContainerCP}, cmd...)
	c := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return stdout.String(), stderr.String(), err
}
