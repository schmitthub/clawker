package clawkerd

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// TestRun_MissingAgentEnv_ReturnsConfigExit verifies run() fails fast with
// the deterministic config exit code (exitCodeConfig, not the generic 1)
// when the required CLAWKER_AGENT env is unset. The distinction is
// load-bearing: an operator running `restart: on-failure:max-retries=N`
// wires trip-and-stop on this code instead of restart-looping a container
// that can never come up. The guard is the first thing run() checks, so the
// test exercises it without reading bootstrap or starting the listener.
func TestRun_MissingAgentEnv_ReturnsConfigExit(t *testing.T) {
	// Clear the agent env (this process runs inside a clawker container
	// where CLAWKER_AGENT is set); t.Setenv restores it after the test.
	t.Setenv(consts.EnvAgent, "")

	code, err := run(context.Background(), logger.Nop())
	if err == nil {
		t.Fatal("run with unset CLAWKER_AGENT: want error, got nil")
	}
	if code != exitCodeConfig {
		t.Fatalf("run exit code = %d, want exitCodeConfig (%d)", code, exitCodeConfig)
	}
}
