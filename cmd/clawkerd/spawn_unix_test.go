//go:build unix

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

const fastReadyPoll = 10 * time.Second

func TestSpawnState_RunWaitEcho(t *testing.T) {
	s := newSpawnState(logger.Nop())
	stdout := &lockedBuf{}
	cfg := spawnConfig{
		argv:   []string{"/bin/echo", "hello"},
		stdout: stdout,
		stderr: stdout,
		stdin:  bytes.NewReader(nil),
		log:    logger.Nop(),
	}
	if err := s.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s.BeginOrphanDrain()
	if code := s.Wait(); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != "hello" {
		t.Errorf("stdout = %q, want %q", got, "hello")
	}
}

func TestSpawnState_RunWaitFalse(t *testing.T) {
	s := newSpawnState(logger.Nop())
	cfg := spawnConfig{
		argv:   []string{"/bin/sh", "-c", "exit 1"},
		stdout: &lockedBuf{},
		stderr: &lockedBuf{},
		stdin:  bytes.NewReader(nil),
		log:    logger.Nop(),
	}
	if err := s.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s.BeginOrphanDrain()
	if code := s.Wait(); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestSpawnState_RunWaitExit42(t *testing.T) {
	s := newSpawnState(logger.Nop())
	cfg := spawnConfig{
		argv:   []string{"/bin/sh", "-c", "exit 42"},
		stdout: &lockedBuf{},
		stderr: &lockedBuf{},
		stdin:  bytes.NewReader(nil),
		log:    logger.Nop(),
	}
	if err := s.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s.BeginOrphanDrain()
	if code := s.Wait(); code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

func TestSpawnState_StopSignaled(t *testing.T) {
	s := newSpawnState(logger.Nop())
	cfg := spawnConfig{
		argv:   []string{"/bin/sh", "-c", "trap '' INT; sleep 30"},
		stdout: &lockedBuf{},
		stderr: &lockedBuf{},
		stdin:  bytes.NewReader(nil),
		log:    logger.Nop(),
	}
	if err := s.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s.BeginOrphanDrain()
	s.Stop(100 * time.Millisecond)
	code := s.Wait()
	wantTerm := 128 + int(syscall.SIGTERM)
	wantKill := 128 + int(syscall.SIGKILL)
	if code != wantTerm && code != wantKill {
		t.Errorf("exit = %d, want %d (SIGTERM) or %d (SIGKILL)", code, wantTerm, wantKill)
	}
}

func TestSpawnState_DoubleRunReturnsAlreadySpawned(t *testing.T) {
	s := newSpawnState(logger.Nop())
	cfg := spawnConfig{
		argv:   []string{"/bin/sh", "-c", "exit 0"},
		stdout: &lockedBuf{},
		stderr: &lockedBuf{},
		stdin:  bytes.NewReader(nil),
		log:    logger.Nop(),
	}
	if err := s.Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	s.BeginOrphanDrain()
	if err := s.Run(cfg); !errors.Is(err, errAlreadySpawned) {
		t.Fatalf("second Run err = %v, want errAlreadySpawned", err)
	}
	if code := s.Wait(); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

// TestSpawnState_DoubleRunPropagatesOriginalSpawnError verifies the
// silent-failure invariant: if the first Run failed before installing
// a child, every subsequent caller must see that original error
// rather than errAlreadySpawned. Without this contract, a Session
// reconnect would map errAlreadySpawned to Done{0} and tell CP "child
// running fine" when no child ever started.
func TestSpawnState_DoubleRunPropagatesOriginalSpawnError(t *testing.T) {
	s := newSpawnState(logger.Nop())
	cfg := spawnConfig{log: logger.Nop()} // empty argv → errEmptyArgv
	first := s.Run(cfg)
	if !errors.Is(first, errEmptyArgv) {
		t.Fatalf("first Run err = %v, want errEmptyArgv", first)
	}
	second := s.Run(cfg)
	if !errors.Is(second, errEmptyArgv) {
		t.Errorf("second Run err = %v, want errEmptyArgv (NOT errAlreadySpawned)", second)
	}
}

// TestSpawnState_SpawnErrorClosesDoneCh verifies that Done() unblocks
// on a failed Run so a caller selecting on Done() doesn't deadlock.
func TestSpawnState_SpawnErrorClosesDoneCh(t *testing.T) {
	s := newSpawnState(logger.Nop())
	cfg := spawnConfig{log: logger.Nop()}
	if err := s.Run(cfg); err == nil {
		t.Fatal("expected error from empty argv")
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() did not close on spawn error — caller would deadlock")
	}
}

func TestSpawnState_ReadyFileTouchedAfterStart(t *testing.T) {
	dir := t.TempDir()
	readyFile := filepath.Join(dir, "ready")
	s := newSpawnState(logger.Nop())
	cfg := spawnConfig{
		argv:      []string{"/bin/sh", "-c", "exit 0"},
		stdout:    &lockedBuf{},
		stderr:    &lockedBuf{},
		stdin:     bytes.NewReader(nil),
		log:       logger.Nop(),
		readyFile: readyFile,
	}
	if err := s.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s.BeginOrphanDrain()
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("ready file not created: %v", err)
	}
	_ = s.Wait()
}

// TestSpawnState_DescendantsReaped pins phase-2 of the reaper: after
// main exits, reparented orphans (the backgrounded sleeper) must
// drain before doneCh closes.
func TestSpawnState_DescendantsReaped(t *testing.T) {
	s := newSpawnState(logger.Nop())
	cfg := spawnConfig{
		argv:   []string{"/bin/sh", "-c", "(sleep 0.2; exit 0) & exit 0"},
		stdout: &lockedBuf{},
		stderr: &lockedBuf{},
		stdin:  bytes.NewReader(nil),
		log:    logger.Nop(),
	}
	if err := s.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s.BeginOrphanDrain()
	select {
	case <-s.Done():
	case <-time.After(fastReadyPoll):
		t.Fatal("doneCh did not close within bounded poll")
	}
	if code := s.Wait(); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

func TestSpawnState_RunErrorOnEmptyArgv(t *testing.T) {
	s := newSpawnState(logger.Nop())
	cfg := spawnConfig{log: logger.Nop()}
	if err := s.Run(cfg); !errors.Is(err, errEmptyArgv) {
		t.Fatalf("Run err = %v, want errEmptyArgv", err)
	}
	if code := s.Wait(); code != 1 {
		t.Errorf("Wait after spawn error = %d, want 1", code)
	}
}

func TestSpawnState_RunErrorOnUnresolvableArgv(t *testing.T) {
	s := newSpawnState(logger.Nop())
	failPath := func(string) (string, error) { return "", errors.New("synthetic ENOENT") }
	cfg := spawnConfig{
		argv:     []string{"definitely-not-a-binary"},
		stdout:   &lockedBuf{},
		stderr:   &lockedBuf{},
		stdin:    bytes.NewReader(nil),
		log:      logger.Nop(),
		lookPath: failPath,
	}
	if err := s.Run(cfg); err == nil {
		t.Fatal("expected Run error when lookPath fails for argv[0]")
	}
	if code := s.Wait(); code != 1 {
		t.Errorf("Wait after spawn error = %d, want 1", code)
	}
}

func TestMapWaitStatus_Exited(t *testing.T) {
	c, err := waitStatusFromShell("exit 5")
	if err != nil {
		t.Fatalf("seed shell: %v", err)
	}
	if got := mapWaitStatus(c); got != 5 {
		t.Errorf("mapWaitStatus = %d, want 5", got)
	}
}

func TestMapWaitStatus_Signaled(t *testing.T) {
	c, err := waitStatusFromShell("kill -TERM $$")
	if err != nil {
		t.Fatalf("seed shell: %v", err)
	}
	want := 128 + int(syscall.SIGTERM)
	if got := mapWaitStatus(c); got != want {
		t.Errorf("mapWaitStatus = %d, want %d", got, want)
	}
}

// TestForwardableSignals_ExcludesUnsafe pins the security/correctness
// invariant that SIGCHLD/SIGURG/program-error signals are NEVER
// forwarded — a refactor that adds SIGCHLD silently corrupts the
// reaper, and an E2E suite cannot cleanly assert that absence.
func TestForwardableSignals_ExcludesUnsafe(t *testing.T) {
	got := forwardableSignals()
	excluded := map[syscall.Signal]string{
		syscall.SIGCHLD: "SIGCHLD (reaper handles)",
		syscall.SIGURG:  "SIGURG (Go runtime preemption)",
		syscall.SIGFPE:  "SIGFPE (program-error)",
		syscall.SIGILL:  "SIGILL (program-error)",
		syscall.SIGSEGV: "SIGSEGV (program-error)",
		syscall.SIGBUS:  "SIGBUS (program-error)",
		syscall.SIGABRT: "SIGABRT (program-error)",
		syscall.SIGTRAP: "SIGTRAP (program-error)",
		syscall.SIGSYS:  "SIGSYS (program-error)",
	}
	for _, s := range got {
		us, ok := s.(syscall.Signal)
		if !ok {
			t.Errorf("non-syscall.Signal in forwardable set: %v", s)
			continue
		}
		if reason, bad := excluded[us]; bad {
			t.Errorf("forwardable signals include %v — %s", us, reason)
		}
	}
}

// TestSpawnState_StopBeforeRunIsNoop pins idempotence: Stop called
// before Run (e.g. SIGTERM lands during early-boot crash) must not
// panic or block.
func TestSpawnState_StopBeforeRunIsNoop(t *testing.T) {
	s := newSpawnState(logger.Nop())
	s.Stop(time.Millisecond)
}

func waitStatusFromShell(script string) (syscall.WaitStatus, error) {
	c := exec.Command("/bin/sh", "-c", script)
	_ = c.Run()
	if c.ProcessState == nil {
		return 0, errors.New("no ProcessState")
	}
	ws, ok := c.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		return 0, errors.New("non-WaitStatus Sys()")
	}
	return ws, nil
}

type lockedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
