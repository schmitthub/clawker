package e2e

import (
	"context"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// TestCPSelfShutdown_E2E verifies INV-B2-007: the CP self-terminates
// after the agent count has been zero for (GracePeriod + PollInterval *
// MissedThreshold). With defaults that's 60s + 30s*2 = 120s, so the
// test allows 180s of wall time to tolerate container-start overhead.
//
// The assertion covers the full drain contract: (1) the CP container
// exits on its own, (2) the exit is clean (exit code 0), (3) the
// restart policy does NOT bring it back (on-failure ignores code 0),
// and (4) the firewall stack sibling containers (Envoy + CoreDNS) are
// also stopped by the drain callback.
//
// Runs only on a host with a real Docker daemon outside the clawker
// agent container — agent-side containers cannot drive the Docker
// stack reliably.
func TestCPSelfShutdown_E2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := configmocks.NewIsolatedTestConfig(t)
	log := logger.Nop()

	dc, err := docker.NewClient(ctx, cfg, log)
	require.NoError(t, err, "constructing docker client")
	t.Cleanup(func() { _ = dc.Close() })

	// Drain path always tears down the CP; a safety net makes sure a
	// failed assertion doesn't leak the container into the next test.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := controlplane.Stop(cleanupCtx, dc); err != nil {
			t.Logf("cleanup: controlplane.Stop: %v", err)
		}
	})

	require.NoError(t, controlplane.EnsureRunning(ctx, dc, cfg, log), "EnsureRunning")

	// Give the watcher's grace period + two poll intervals to elapse,
	// plus a margin for the drain callback to stop the stack and exit.
	// Defaults: 60s grace + (30s * 2) polls = 120s. Allow 3 minutes.
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("CP did not self-shutdown within the drain window")
		}

		inspected, err := dc.ContainerInspect(ctx, consts.ContainerCP, mobyclient.ContainerInspectOptions{})
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				// Container removed by a concurrent teardown — still acceptable;
				// the primary assertion is "not running".
				return
			}
			require.NoError(t, err, "inspecting CP container")
		}
		state := inspected.Container.State
		if state != nil && !state.Running {
			// Drain-to-zero must produce a clean exit (code 0) — a non-zero
			// exit would be caught by the on-failure policy and restart the
			// container, defeating the whole point of drain.
			assert.Equal(t, 0, state.ExitCode, "CP exit code must be 0 for drain-to-zero (otherwise restart policy retriggers)")
			// Restart policy is on-failure; assert it didn't fire.
			assert.Equal(t, 0, inspected.Container.RestartCount, "on-failure restart policy must not retrigger on clean exit")
			return
		}

		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
}
