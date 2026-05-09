//go:build unix

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/schmitthub/clawker/internal/logger"
)

// spawnConfig is the all-inputs struct main() passes to the spawn entry.
type spawnConfig struct {
	argv      []string
	dir       string
	env       []string
	user      *ExecUser
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	log       *logger.Logger
	readyFile string                       // touched after Start; "" = skip
	lookPath  func(string) (string, error) // test seam, defaults to exec.LookPath
}

// spawnState tracks the user CMD across its lifetime. Exactly one
// instance per clawkerd process. Single-shot via runEntered CAS; mu
// guards proc/pgid/finalWS; doneCh closes exactly once after main
// child reaped + descendants drained (closed exclusively by runReaper
// or its panic-recovery path).
//
// Lifecycle (call ordering):
//
//	Run                 → installs child + reaper + signal forwarder
//	  ↓
//	[Stop(grace)]       → optional; SIGTERM child pgroup, escalate to SIGKILL
//	  ↓
//	<-MainExited()      → fires when phase 1 reaps the main child
//	  ↓
//	BeginOrphanDrain    → REQUIRED before Wait/Done unblock
//	  ↓
//	Wait()/<-Done()     → returns bash-convention exit code
//
// Started, SpawnErr, MainExited may be called at any time.
//
// Reaper phasing: runReaper waits on mainPID specifically (phase 1)
// while the main child is alive — never Wait4(-1) — so concurrent
// exec.Cmd.Wait calls in session.go's ShellCommand pipelines are not
// stolen. After main exits, runReaper transitions to phase 2 which
// drains reparented orphans via Wait4(-1, WNOHANG). Phase 2 races
// session.go's c.Wait for any still-running stage children, so the
// gate is HELD by default — callers MUST call BeginOrphanDrain after
// they have torn down concurrent exec.Cmd.Wait surfaces (the gRPC
// listener + ShellCommand pipelines). A caller that forgets gets a
// loud hang on Wait/Done, never silent stage-pid theft. Tests with
// no concurrent exec.Cmd.Wait surface call BeginOrphanDrain right
// after Run.
//
// runEntered vs Started: runEntered is the CAS-once gate guarding
// runOnce so a second Run returns errAlreadySpawned (only after the
// first call succeeded — see Run godoc). Started reflects whether
// runOnce installed a child process (s.proc != nil under mu); these
// diverge during the spawn-error window (runEntered=true, proc=nil)
// where main()'s Stop must no-op and MainExited must already be
// closed via closeAllGates.
type spawnState struct {
	log *logger.Logger

	runEntered atomic.Bool
	spawnErr   error

	mu              sync.Mutex
	proc            *os.Process // nil before spawn
	pgid            int
	finalWS         *syscall.WaitStatus // non-nil iff main child reaped
	doneCh          chan struct{}
	mainExitedCh    chan struct{}
	orphanDrainCh   chan struct{}
	stopOnce        sync.Once
	doneOnce        sync.Once
	mainExitedOnce  sync.Once
	orphanDrainOnce sync.Once
}

// newSpawnState returns a spawnState ready to receive Run.
func newSpawnState(log *logger.Logger) *spawnState {
	return &spawnState{
		log:           log,
		doneCh:        make(chan struct{}),
		mainExitedCh:  make(chan struct{}),
		orphanDrainCh: make(chan struct{}),
	}
}

// BeginOrphanDrain releases the phase-2 gate so the reaper can drain
// reparented orphans via Wait4(-1, WNOHANG). Idempotent. The gate is
// HELD by default (never auto-opened) so a caller that forgets to
// release it gets a loud hang on Wait/Done rather than silent
// stage-pid theft from concurrent exec.Cmd.Wait surfaces. Production
// callers (clawkerd main()) release this AFTER GracefulStop drains
// the gRPC listener; test callers with no concurrent exec.Cmd.Wait
// surface release it immediately after Run.
func (s *spawnState) BeginOrphanDrain() {
	s.orphanDrainOnce.Do(func() { close(s.orphanDrainCh) })
}

// MainExited returns a channel that closes once the main child has
// been reaped (phase 1 complete) — even if phase 2 has not started
// yet. main() uses it as the trigger to GracefulStop the gRPC
// listener before BeginOrphanDrain releases phase 2.
func (s *spawnState) MainExited() <-chan struct{} {
	return s.mainExitedCh
}

func (s *spawnState) closeMainExitedCh() {
	s.mainExitedOnce.Do(func() { close(s.mainExitedCh) })
}

// Run forks+execs cfg.argv (after routeArgs) with privilege drop
// (SysProcAttr.Credential from cfg.user) and Setpgid:true. Touches
// cfg.readyFile after Start succeeds. Starts the signal-forwarder and
// reaper goroutines.
//
// Single-use: a second call returns errAlreadySpawned only if the
// first call succeeded. If the first call failed (s.spawnErr != nil),
// every subsequent call returns that original error so a Session
// reconnect that re-dispatches AgentReady cannot mask a never-spawned
// child as Done{0}.
func (s *spawnState) Run(cfg spawnConfig) error {
	if !s.runEntered.CompareAndSwap(false, true) {
		if s.spawnErr != nil {
			return s.spawnErr
		}
		return errAlreadySpawned
	}
	s.spawnErr = s.runOnce(cfg)
	if s.spawnErr != nil {
		// Close every gate so callers selecting on MainExited/Done
		// unblock rather than deadlocking after a spawn-error path.
		// Including orphanDrainCh: a held gate would otherwise leave
		// a caller's BeginOrphanDrain call dangling on a process
		// that never spawned.
		s.closeAllGates()
	}
	return s.spawnErr
}

func (s *spawnState) runOnce(cfg spawnConfig) error {
	if cfg.log == nil {
		return errors.New("clawkerd: spawn config missing logger")
	}
	if len(cfg.argv) == 0 {
		return errEmptyArgv
	}

	lookPath := cfg.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	routedArgv := routeArgs(cfg.argv, lookPath)

	resolvedPath := routedArgv[0]
	if !strings.Contains(resolvedPath, "/") {
		p, err := lookPath(resolvedPath)
		if err != nil {
			return fmt.Errorf("clawkerd: lookup %q: %w", resolvedPath, err)
		}
		resolvedPath = p
	}

	// nosemgrep: go.lang.security.audit.dangerous-exec-cmd.dangerous-exec-cmd -- clawkerd is PID 1 and exists to spawn the container's user CMD; argv comes from os.Args, set by Docker from the image's CMD. No caller-attacker-controlled input path.
	cmd := &exec.Cmd{
		Path:   resolvedPath,
		Args:   routedArgv,
		Dir:    cfg.dir,
		Env:    envWithHome(cfg.env, cfg.user),
		Stdin:  cfg.stdin,
		Stdout: cfg.stdout,
		Stderr: cfg.stderr,
		SysProcAttr: &syscall.SysProcAttr{
			Setpgid: true,
		},
	}
	if cfg.user != nil {
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid:    cfg.user.UID,
			Gid:    cfg.user.GID,
			Groups: cfg.user.Groups,
		}
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("clawkerd: spawn %q: %w", resolvedPath, err)
	}
	proc := cmd.Process

	pgid, err := unix.Getpgid(proc.Pid)
	if err != nil {
		// Setpgid:true is the kernel-side guarantee. Getpgid failing
		// here means the kernel returned an error on a pid we just
		// forked — refuse the spawn rather than silently kill-targeting
		// -pid and hoping pid==pgid held.
		_ = proc.Kill()
		_, _ = proc.Wait()
		return fmt.Errorf("clawkerd: getpgid pid=%d: %w", proc.Pid, err)
	}

	s.mu.Lock()
	s.proc = proc
	s.pgid = pgid
	s.mu.Unlock()

	uid, gid := uint32(0), uint32(0)
	if cfg.user != nil {
		uid, gid = cfg.user.UID, cfg.user.GID
	}
	cfg.log.Info().
		Str("event", "spawn_started").
		Int("pid", proc.Pid).
		Int("pgid", pgid).
		Strs("argv", routedArgv).
		Uint32("uid", uid).
		Uint32("gid", gid).
		Msg("clawkerd: user CMD spawned")

	if cfg.readyFile != "" {
		if err := touchReadyFile(cfg.readyFile); err != nil {
			cfg.log.Error().Err(err).
				Str("event", "spawn_ready_file_touch_failed").
				Str("path", cfg.readyFile).
				Msg("clawkerd: HEALTHCHECK ready file write failed; container will appear unhealthy")
		}
	}

	go s.runSignalForwarder(cfg.log)
	go s.runReaper(cfg.log)

	return nil
}

// Wait blocks until the main child has exited AND all reparented
// descendants have been reaped, then returns the bash-convention exit
// code. Returns 1 if Run was never called, returned a spawn error,
// or the reaper closed doneCh without recording a final status.
func (s *spawnState) Wait() int {
	if !s.spawned() {
		return 1
	}
	<-s.doneCh
	s.mu.Lock()
	ws := s.finalWS
	s.mu.Unlock()
	if ws == nil {
		return 1
	}
	return mapWaitStatus(*ws)
}

// Done returns a channel that closes once the main child has been
// reaped and all descendants have drained (or once Run has failed).
func (s *spawnState) Done() <-chan struct{} {
	return s.doneCh
}

// Started reports whether Run was invoked AND succeeded in installing
// a child process. Used by main()'s teardown path to skip blocking
// on MainExited when the daemon is shutting down before any spawn
// occurred (e.g. SIGTERM arrives before AgentReady).
func (s *spawnState) Started() bool { return s.spawned() }

// SpawnErr returns the error captured from Run's first call, or nil
// if Run succeeded or was never invoked. main() reads it after Wait
// returns to decide whether to surface a non-zero exit cause to
// the caller's runErr channel — without this, an AgentReady that
// failed to fork the child would silently exit 1 with no shutdown
// log line tying the exit code to the spawn error.
func (s *spawnState) SpawnErr() error { return s.spawnErr }

// Stop sends SIGTERM to the child pgroup, then SIGKILL after grace.
// Idempotent. No-op if Run hasn't been called.
func (s *spawnState) Stop(grace time.Duration) {
	s.mu.Lock()
	pgid := s.pgid
	proc := s.proc
	s.mu.Unlock()
	if proc == nil {
		s.log.Info().
			Str("event", "spawn_stop_before_run").
			Msg("clawkerd: Stop called before Run; nothing to forward")
		return
	}
	s.stopOnce.Do(func() {
		if err := unix.Kill(-pgid, unix.SIGTERM); err != nil && !errors.Is(err, unix.ESRCH) {
			s.log.Warn().Err(err).
				Str("event", "spawn_stop_sigterm_failed").
				Int("pgid", pgid).
				Msg("clawkerd: SIGTERM forward to child pgroup failed")
		}
		go s.runStopWatchdog(grace, pgid)
	})
}

// runStopWatchdog escalates to SIGKILL if the child pgroup hasn't
// drained within the grace window. Bails early if the reaper has
// already closed doneCh. The recover onPanic re-attempts SIGKILL
// from the recovery path because a panic between the timer wait and
// the kill (logger panic, kernel-side fault) would otherwise leave
// the child running past grace — main()'s `<-MainExited` then waits
// indefinitely until docker's external SIGKILL fires.
func (s *spawnState) runStopWatchdog(grace time.Duration, pgid int) {
	defer recoverGoroutine(s.log, "stop_watchdog", func() {
		_ = unix.Kill(-pgid, unix.SIGKILL)
	})
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-s.doneCh:
		return
	case <-t.C:
	}
	if err := unix.Kill(-pgid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
		s.log.Warn().Err(err).
			Str("event", "spawn_stop_sigkill_failed").
			Int("pgid", pgid).
			Msg("clawkerd: SIGKILL escalation failed")
	}
}

// runSignalForwarder forwards every signal the supervisor receives
// (excluding SIGCHLD, SIGURG, and the program-error set) to the
// child's process group. Exits when doneCh closes.
func (s *spawnState) runSignalForwarder(log *logger.Logger) {
	defer recoverGoroutine(log, "signal_forwarder", nil)
	ch := make(chan os.Signal, 32)
	signal.Notify(ch, forwardableSignals()...)
	defer signal.Stop(ch)
	for {
		select {
		case <-s.doneCh:
			return
		case sig := <-ch:
			s.mu.Lock()
			pgid := s.pgid
			s.mu.Unlock()
			if pgid == 0 {
				continue
			}
			usig, ok := sig.(syscall.Signal)
			if !ok {
				log.Debug().
					Str("event", "spawn_signal_unknown_type").
					Str("type", fmt.Sprintf("%T", sig)).
					Msg("clawkerd: dropped non-syscall.Signal")
				continue
			}
			err := unix.Kill(-pgid, usig)
			if err != nil && !errors.Is(err, unix.ESRCH) {
				log.Warn().Err(err).
					Str("event", "spawn_signal_forward_failed").
					Int("pgid", pgid).
					Int("signo", int(usig)).
					Msg("clawkerd: signal forward failed")
			}
		}
	}
}

// runReaper waits on the main child via Wait4(mainPID), then drains
// any reparented orphans via Wait4(-1, WNOHANG). The two-phase split
// is load-bearing: while main is alive, session.go ShellCommand
// stages may be calling exec.Cmd.Wait — using Wait4(-1) here would
// race those calls and steal their reapable pids. After phase 1 the
// reaper closes mainExitedCh so callers (main()) can GracefulStop
// the gRPC listener and drain in-flight pipelines, then waits on
// orphanDrainCh before phase 2 begins.
//
// Closes doneCh exactly once on exit (or on panic via
// recoverGoroutine). closeAllGates also fires on the panic-recovery
// path so callers waiting on MainExited do not deadlock.
func (s *spawnState) runReaper(log *logger.Logger) {
	defer recoverGoroutine(log, "reaper", s.closeAllGates)
	mainPID := s.mainPID()
	if mainPID == 0 {
		log.Error().
			Str("event", "spawn_reaper_no_main_pid").
			Msg("clawkerd: reaper started with no main pid; closing doneCh")
		s.closeAllGates()
		return
	}
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, unix.SIGCHLD)
	defer signal.Stop(sigchld)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	// Phase 1: wait on mainPID specifically. No -1 calls here so we
	// don't race session.go's exec.Cmd.Wait for stage children.
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(mainPID, &ws, syscall.WNOHANG, nil)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if errors.Is(err, syscall.ECHILD) {
				log.Warn().
					Int("pid", mainPID).
					Str("event", "spawn_reaper_main_already_reaped").
					Msg("clawkerd: main child already reaped elsewhere; closing doneCh without final status")
				s.closeAllGates()
				return
			}
			log.Error().Err(err).
				Str("event", "spawn_wait4_main_failed").
				Int("pid", mainPID).
				Msg("clawkerd: wait4 on main child failed; will retry")
		} else if pid == mainPID {
			s.mu.Lock()
			s.finalWS = &ws
			s.mu.Unlock()
			log.Info().
				Str("event", "spawn_main_reaped").
				Int("pid", pid).
				Bool("signaled", ws.Signaled()).
				Int("exit_status", ws.ExitStatus()).
				Msg("clawkerd: main child reaped")
			break
		}
		select {
		case <-sigchld:
		case <-ticker.C:
		}
	}
	// Phase 1 done. Signal callers waiting to coordinate teardown
	// (e.g. main()'s GracefulStop) BEFORE phase 2 starts draining.
	s.closeMainExitedCh()
	// Wait for the phase-2 gate before running Wait4(-1, WNOHANG):
	// concurrent exec.Cmd.Wait surfaces (session.go's ShellCommand
	// stages) must drain first, otherwise the reaper steals their
	// stage-child pids and leaves c.Wait returning ECHILD.
	<-s.orphanDrainCh

	// Phase 2: drain reparented orphans now that main has exited.
	for {
		drained := s.drainOrphans(log)
		if drained {
			s.closeDoneCh()
			return
		}
		select {
		case <-sigchld:
		case <-ticker.C:
		}
	}
}

// drainOrphans reaps every currently exitable orphan reparented to
// PID 1. Returns true when Wait4 reports no reapable children remain.
// Errors other than ECHILD are logged and treated as "drained" so
// the reaper doesn't spin forever on a kernel-side fault.
func (s *spawnState) drainOrphans(log *logger.Logger) bool {
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if errors.Is(err, syscall.ECHILD) {
				return true
			}
			log.Error().Err(err).
				Str("event", "spawn_wait4_orphan_failed").
				Msg("clawkerd: wait4 on orphan drain failed; treating as drained to avoid spin")
			return true
		}
		if pid == 0 {
			return true
		}
		log.Debug().
			Int("pid", pid).
			Bool("signaled", ws.Signaled()).
			Int("exit_status", ws.ExitStatus()).
			Str("event", "spawn_orphan_reaped").
			Msg("clawkerd: orphan reaped")
	}
}

func (s *spawnState) closeDoneCh() {
	s.doneOnce.Do(func() { close(s.doneCh) })
}

// closeAllGates is the failure-path bailout: closes every signal
// channel a caller might be selecting on so a reaper bailout (panic,
// no-main-pid, ECHILD on main) cannot strand main()'s teardown
// select on MainExited or its BeginOrphanDrain follow-up.
func (s *spawnState) closeAllGates() {
	s.closeMainExitedCh()
	s.BeginOrphanDrain()
	s.closeDoneCh()
}

func (s *spawnState) mainPID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil {
		return 0
	}
	return s.proc.Pid
}

// spawned reports whether Run installed a child process.
func (s *spawnState) spawned() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proc != nil
}

// mapWaitStatus converts a syscall.WaitStatus from Wait4 into the
// bash-convention exit code (signaled → 128+signum, exited → status).
func mapWaitStatus(ws syscall.WaitStatus) int {
	switch {
	case ws.Signaled():
		return 128 + int(ws.Signal())
	case ws.Exited():
		return ws.ExitStatus()
	}
	return 1
}

// forwardableSignals returns the signal set the supervisor forwards
// to the child's process group. Excludes:
//
//   - SIGCHLD: handled by the reaper
//   - SIGURG:  Go runtime uses it for goroutine preemption
//   - program-error signals (SIGFPE/SIGILL/SIGSEGV/SIGBUS/SIGABRT/
//     SIGTRAP/SIGSYS): these indicate supervisor-side bugs; let them
//     crash clawkerd rather than masking via forward
//   - SIGKILL/SIGSTOP: cannot be caught
func forwardableSignals() []os.Signal {
	return []os.Signal{
		unix.SIGHUP,
		unix.SIGINT,
		unix.SIGQUIT,
		unix.SIGTERM,
		unix.SIGUSR1,
		unix.SIGUSR2,
		unix.SIGPIPE,
		unix.SIGALRM,
		unix.SIGTSTP,
		unix.SIGTTIN,
		unix.SIGTTOU,
		unix.SIGWINCH,
	}
}

// touchReadyFile creates path if missing; touch-style.
func touchReadyFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// recoverGoroutine is the resilience-contract recovery wrapper for
// long-lived spawnState goroutines. clawkerd is PID 1; a panic in
// any of these would kill the supervisor and strand eBPF state (root
// CLAUDE.md "CP crashing is a SECURITY incident" — same contract
// applies to clawkerd-as-PID-1). onPanic, when non-nil, fires after
// the structured log so a panic in a load-bearing goroutine (e.g.
// the reaper) can release waiters via closeDoneCh rather than
// deadlocking Wait().
func recoverGoroutine(log *logger.Logger, name string, onPanic func()) {
	r := recover()
	if r == nil {
		return
	}
	log.Error().
		Interface("panic", r).
		Bytes("stack", debug.Stack()).
		Str("event", "spawn_goroutine_panic").
		Str("goroutine", name).
		Msg("clawkerd: spawn goroutine recovered from panic; supervisor degrading but staying alive")
	if onPanic != nil {
		onPanic()
	}
}
