//go:build linux

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/logger"
)

// TestSpawnState_PrivilegeDrop_Linux verifies that the child runs
// under the requested uid/gid by spawning `id -u` as a non-root
// user and asserting the printed UID matches. Skips unless the test
// process is root — only root can setuid-up to a synthetic UID, and
// most CI / dev hosts run as a non-root user.
func TestSpawnState_PrivilegeDrop_Linux(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("privilege drop test requires root (current uid=", os.Getuid(), ")")
	}
	const targetUID = uint32(65534) // nobody on most distros; existence not required for this assertion since we skip the resolver
	stdout := &lockedBuf{}
	s := newSpawnState(logger.Nop())
	cfg := spawnConfig{
		argv:   []string{"/usr/bin/id", "-u"},
		stdout: stdout,
		stderr: &lockedBuf{},
		stdin:  bytes.NewReader(nil),
		log:    logger.Nop(),
		user:   &ExecUser{uid: targetUID, gid: targetUID},
	}
	if err := s.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// BeginOrphanDrain releases the phase-2 reaper gate so Wait can
	// unblock. Without it, Wait hangs to test timeout — which the
	// non-root skip above masked, hiding the deadlock.
	s.BeginOrphanDrain()
	if code := s.Wait(); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got := strings.TrimSpace(stdout.String())
	if got != "65534" {
		t.Errorf("id -u = %q, want %q", got, "65534")
	}
}
