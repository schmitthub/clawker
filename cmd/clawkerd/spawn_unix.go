//go:build unix

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/schmitthub/clawker/internal/logger"
)

// reaperRetryBudget caps consecutive non-EINTR/non-ECHILD wait4
// failures before the reaper escalates to closeAllGates. Sized for
// transient kernel hiccups (EAGAIN-class faults) while still tripping
// fast on a deterministic EINVAL/EFAULT bug — without a budget the
// loop hot-spins at 20Hz emitting Error logs while main()'s
// `<-MainExited()` hangs forever.
const reaperRetryBudget = 32

// orphanDrainRetryBudget caps consecutive unknown wait4(-1) errors
// in drainOrphans before it gives up. The pre-budget fallback was
// "treat as drained" — silently closing doneCh while reparented
// orphans may still be alive. Now the reaper aborts loudly via the
// spawn_orphan_drain_aborted event so operators see "supervisor gave
// up draining" instead of "clean shutdown with zombie pile".
const orphanDrainRetryBudget = 32

// spawnConfig is the all-inputs struct passed to spawnState.Run.
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
// instance per clawkerd process. runEntered is the CAS-once gate
// guarding runOnce — a second Run after a successful first call
// returns errAlreadySpawned; a second Run after a failed first call
// returns the captured original spawn error so a Session reconnect
// cannot mask a never-spawned child as Done{0}. mu guards
// proc/pgid/finalWS; doneCh closes exactly once after main child
// reaped + descendants drained (closed exclusively by runReaper or
// its panic-recovery path).
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
// Spawned, SpawnErr, MainExited may be called at any time.
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
// runEntered vs Spawned: runEntered is the CAS-once gate guarding
// runOnce so a second Run returns errAlreadySpawned (only after the
// first call succeeded — see Run godoc). Spawned reflects whether
// runOnce installed a child process (s.proc != nil under mu); these
// diverge during the spawn-error window (runEntered=true, proc=nil)
// where main()'s Stop must no-op and MainExited must already be
// closed via closeAllGates.
type spawnState struct {
	log *logger.Logger

	runEntered atomic.Bool
	runDoneCh  chan struct{} // closed after the CAS-winner's runOnce returns

	mu              sync.Mutex
	spawnErr        error       // mu-guarded; published after runDoneCh closes
	reaperErr       error       // mu-guarded; set by reaper bailouts so SpawnErr surfaces them on the shutdown line
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
		runDoneCh:     make(chan struct{}),
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
// callers (clawkerd main()) release this AFTER Stop force-closes the
// gRPC listener; test callers with no concurrent exec.Cmd.Wait
// surface release it immediately after Run.
func (s *spawnState) BeginOrphanDrain() {
	s.orphanDrainOnce.Do(func() { close(s.orphanDrainCh) })
}

// MainExited returns a channel that closes once the main child has
// been reaped (phase 1 complete) — even if phase 2 has not started
// yet. main() uses it as the trigger to Stop (force-close) the gRPC
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
// first call succeeded. If the first call failed, every subsequent
// call returns that original error so a Session reconnect that
// re-dispatches AgentReady cannot mask a never-spawned child as
// Done{0}.
//
// A second caller that races the first one's runOnce blocks on
// runDoneCh so it observes the final outcome rather than reading a
// torn nil from spawnErr. Without this barrier, a CP retry storm
// could land a second AgentReady mid-spawn, read spawnErr=nil, and
// reply Done{0} for a child that ultimately failed to fork — exactly
// the silent-spawn-failure mode the single-spawn invariant exists to
// prevent.
func (s *spawnState) Run(cfg spawnConfig) error {
	if !s.runEntered.CompareAndSwap(false, true) {
		<-s.runDoneCh
		s.mu.Lock()
		err := s.spawnErr
		s.mu.Unlock()
		if err != nil {
			return err
		}
		return errAlreadySpawned
	}
	defer close(s.runDoneCh)
	err := s.runOnce(cfg)
	s.mu.Lock()
	s.spawnErr = err
	s.mu.Unlock()
	if err != nil {
		// Close every gate so callers selecting on MainExited/Done
		// unblock rather than deadlocking after a spawn-error path.
		// Including orphanDrainCh: a held gate would otherwise leave
		// a caller's BeginOrphanDrain call dangling on a process
		// that never spawned.
		s.closeAllGates()
	}
	return err
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
	routedArgv, resolvedPath, routedErr := routeArgs(cfg.argv, lookPath)
	if routedErr != nil {
		// Broken image path: argv[0] is not on PATH so we routed to
		// "claude". Warn (not Info) — the supervisor is silently
		// running "claude <orig_argv0>" instead of what the user
		// asked for; an operator triaging "why is claude getting
		// weird args" needs this to surface above operational noise.
		cfg.log.Warn().Err(routedErr).
			Str("event", "spawn_argv_routed_to_claude").
			Str("orig_argv0", cfg.argv[0]).
			Strs("routed_argv", routedArgv).
			Msg("clawkerd: argv[0] not on PATH; routing through 'claude'")
	}

	// resolvedPath is non-empty only on the no-rewrite success path
	// (routeArgs called lookPath and got a hit); on the rewrite paths
	// argv[0]=="claude" and we resolve it here.
	if resolvedPath == "" {
		resolvedPath = routedArgv[0]
		if !strings.Contains(resolvedPath, "/") {
			p, err := lookPath(resolvedPath)
			if err != nil {
				return fmt.Errorf("clawkerd: lookup %q: %w", resolvedPath, err)
			}
			resolvedPath = p
		}
	}

	// Detect whether stdin is a controlling terminal. When clawkerd was
	// started with `docker run -ti`, the kernel attached stdin/out/err
	// to a pty whose foreground pgroup is currently PID 1 (clawkerd).
	// Without explicit foreground transfer the spawn child sits in its
	// own pgroup but the kernel routes keystrokes / SIGINT (Ctrl+C) to
	// clawkerd's pgroup — interactive input never reaches the user CMD
	// and the terminal looks hung.
	//
	// When stdin is NOT a TTY (`docker run` without -ti, CI, piped),
	// we leave Foreground unset; the spawn happens detached, which is
	// the right behavior for non-interactive runs.
	cttyFd := stdinCttyFd(cfg.stdin)
	// nosemgrep: go.lang.security.audit.dangerous-exec-cmd.dangerous-exec-cmd -- clawkerd is PID 1 and exists to spawn the container's user CMD; argv comes from os.Args, set by Docker from the image's CMD. No caller-attacker-controlled input path.
	cmd := &exec.Cmd{
		Path:        resolvedPath,
		Args:        routedArgv,
		Dir:         cfg.dir,
		Env:         envForUser(cfg.env, cfg.user),
		Stdin:       cfg.stdin,
		Stdout:      cfg.stdout,
		Stderr:      cfg.stderr,
		SysProcAttr: buildSysProcAttr(cfg.user, cttyFd),
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
		// -pid and hoping pid==pgid held. ESRCH is the expected path
		// when the child exited immediately between Start and Getpgid;
		// otherwise log the underlying error so an operator can
		// distinguish "kernel race" from "rogue child still running
		// under dropped UID with no supervisor".
		if killErr := proc.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			cfg.log.Error().Err(killErr).
				Str("event", "spawn_getpgid_recovery_kill_failed").
				Int("pid", proc.Pid).
				Msg("clawkerd: kill on getpgid recovery failed; child may still be running under dropped UID")
		}
		state, waitErr := proc.Wait()
		if waitErr != nil && !errors.Is(waitErr, syscall.ECHILD) {
			cfg.log.Error().Err(waitErr).
				Str("event", "spawn_getpgid_recovery_wait_failed").
				Int("pid", proc.Pid).
				Msg("clawkerd: wait on getpgid recovery failed; child reaped status unknown")
		} else if waitErr == nil && state != nil {
			// Race-window child reaped; surface its exit status.
			cfg.log.Info().
				Str("event", "spawn_child_exited_before_pgroup_install").
				Int("pid", proc.Pid).
				Int("exit_code", mapExitCode(state)).
				Msg("clawkerd: child exited before Getpgid; treating as spawn failure")
		}
		return fmt.Errorf("clawkerd: getpgid pid=%d: %w", proc.Pid, err)
	}

	s.mu.Lock()
	s.proc = proc
	s.pgid = pgid
	s.mu.Unlock()

	uid, gid := uint32(0), uint32(0)
	if cfg.user != nil {
		uid, gid = cfg.user.UID(), cfg.user.GID()
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
				Msg("clawkerd: HEALTHCHECK ready file write failed; if HEALTHCHECK is configured, container will appear unhealthy")
		}
	}

	go s.runSignalForwarder(cfg.log)
	go s.runReaper(cfg.log)

	return nil
}

// Wait blocks until doneCh closes and returns the bash-convention
// exit code recorded by the reaper. Returns 1 without blocking if
// the spawn never installed a child (Run not called, or Run failed
// before installing proc); returns 1 if the reaper closed doneCh
// without recording a final status (panic, ECHILD on main, retry
// budget exhausted).
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

func (s *spawnState) Done() <-chan struct{} {
	return s.doneCh
}

// Spawned reports whether Run was invoked AND succeeded in installing
// a child process. Used by main()'s teardown path to skip blocking
// on MainExited when the daemon is shutting down before any spawn
// occurred (e.g. SIGTERM arrives before AgentReady). Symmetric with
// SpawnErr() — both report supervisor state to main().
func (s *spawnState) Spawned() bool { return s.spawned() }

// SpawnErr returns the supervisor-level error main() should surface
// on the shutdown line: the original Run error if Run failed, or the
// reaper-bailout error if the supervisor aborted phase 1/phase 2
// without recording finalWS (retry budget exhausted, no main pid,
// ECHILD on main, panic). Returns nil only if the child was reaped
// cleanly (or Run was never called).
//
// Without surfacing the reaper-bailout cause, a budget-exhausted
// supervisor would silently exit 1 with `event=shutdown` carrying no
// error field; operators would have to grep for `spawn_*_aborted`
// separately to find it.
func (s *spawnState) SpawnErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spawnErr != nil {
		return s.spawnErr
	}
	return s.reaperErr
}

// setReaperErr records the first reaper-bailout cause. Idempotent.
// Only the first cause is kept — subsequent gates closing on the
// same bailout path would otherwise overwrite the original signal.
func (s *spawnState) setReaperErr(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.reaperErr == nil {
		s.reaperErr = err
	}
	s.mu.Unlock()
}

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
// indefinitely until docker's external SIGKILL fires. Also closes
// every gate so a recovery-path kill failure cannot strand main()'s
// teardown; same shape as the reaper's panic-recovery contract.
func (s *spawnState) runStopWatchdog(grace time.Duration, pgid int) {
	defer recoverGoroutine(s.log, "stop_watchdog", func() {
		s.setReaperErr(errors.New("clawkerd: stop_watchdog goroutine panicked"))
		// Recovery-path SIGKILL: log via the structured channel if
		// possible — a kill failure here is exactly what the audit
		// log surface exists to capture. If the logger itself is
		// the source of the panic we just recovered from, a second
		// log call would re-panic; sub-recover catches that and
		// falls back to stderr so the worst-case stop trace still
		// lands somewhere (`docker logs <agent>`).
		err := unix.Kill(-pgid, unix.SIGKILL)
		if err != nil && !errors.Is(err, unix.ESRCH) {
			func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Fprintf(os.Stderr, "clawkerd: stop_watchdog recovery SIGKILL failed pgid=%d err=%v (logger panicked: %v)\n", pgid, err, r)
					}
				}()
				s.log.Error().Err(err).
					Str("event", "spawn_stop_watchdog_sigkill_failed").
					Int("pgid", pgid).
					Msg("clawkerd: stop_watchdog recovery SIGKILL failed")
			}()
		}
		// Release every gate so a watchdog panic cannot strand
		// main()'s `<-MainExited` / `<-Done` selects. Idempotent;
		// reaper paths normally close these themselves but a
		// watchdog panic followed by a reaper that never observes
		// the child exit (kill failed) would otherwise hang main().
		s.closeAllGates()
	})
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-s.doneCh:
		return
	case <-t.C:
	}
	if err := unix.Kill(-pgid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
		// Error not Warn: SIGKILL escalation failure means the child
		// is still running past grace and docker's external SIGKILL
		// will fire. Operators triaging "container took >10s to stop"
		// need this at Error to find it via `journalctl -p err`.
		s.log.Error().Err(err).
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
				// Spawn-error window: runEntered=true, proc=nil so
				// pgid was never installed. Production callers
				// shouldn't hit this (Run sets pgid before goroutines
				// start) but a partial-Run race or test seam could.
				// Log at Debug rather than dropping silently — a
				// SIGTERM lost here is a correctness invariant worth
				// observing.
				log.Debug().
					Str("event", "spawn_signal_dropped_no_pgid").
					Str("signal", sig.String()).
					Msg("clawkerd: signal received but child pgroup not installed; dropped")
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
	defer recoverGoroutine(log, "reaper", func() {
		s.setReaperErr(errors.New("clawkerd: reaper goroutine panicked"))
		s.closeAllGates()
	})
	mainPID := s.mainPID()
	if mainPID == 0 {
		log.Error().
			Str("event", "spawn_reaper_no_main_pid").
			Msg("clawkerd: reaper started with no main pid; closing doneCh")
		s.setReaperErr(errors.New("clawkerd: reaper started with no main pid"))
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
	//
	// Bounded retry on unexpected wait4 errors (EINVAL/EFAULT/etc.):
	// without a budget the loop hot-spins at 20Hz emitting Error logs
	// while main()'s `<-MainExited()` hangs forever — container appears
	// alive but is hung. After reaperRetryBudget consecutive non-EINTR/
	// non-ECHILD failures, escalate to closeAllGates so callers unblock.
	retries := 0
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(mainPID, &ws, syscall.WNOHANG, nil)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if errors.Is(err, syscall.ECHILD) {
				// Error not Warn: with Setpgid:true the kernel
				// guarantees nobody else can reap this child, so ECHILD
				// here is an invariant violation (kernel/library bug
				// or stolen reap). The container will then exit 1 with
				// finalWS=nil and an operator triaging "exit 1, no
				// spawn error" needs Error to find it.
				log.Error().
					Int("pid", mainPID).
					Str("event", "spawn_reaper_main_already_reaped").
					Msg("clawkerd: main child already reaped elsewhere; closing doneCh without final status")
				s.setReaperErr(fmt.Errorf("clawkerd: main child pid=%d already reaped; final status unknown", mainPID))
				s.closeAllGates()
				return
			}
			retries++
			// Per-retry at Warn so journald rate-limiting doesn't
			// drop the bookend abort line at Error below. Without
			// this split, the only operator surface for "reaper
			// gave up" can be silently rate-limited away alongside
			// 31 noisy retry warnings.
			log.Warn().Err(err).
				Str("event", "spawn_wait4_main_failed").
				Int("pid", mainPID).
				Int("retries", retries).
				Int("budget", reaperRetryBudget).
				Msg("clawkerd: wait4 on main child failed; will retry")
			if retries >= reaperRetryBudget {
				log.Error().
					Str("event", "spawn_wait4_main_aborted").
					Int("pid", mainPID).
					Int("retries", retries).
					Msg("clawkerd: wait4 retry budget exhausted; closing gates so main() can exit")
				s.setReaperErr(fmt.Errorf("clawkerd: wait4 retry budget exhausted on pid=%d: %w", mainPID, err))
				s.closeAllGates()
				return
			}
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
		} else {
			// pid==0: kernel returned "no state change yet" — main child
			// still alive. Reset the retry counter so a flapping kernel
			// (transient EINVAL/EFAULT mixed with healthy WNOHANG spins)
			// recovers cleanly rather than accumulating toward the budget.
			// "consecutive" per the budget docstring.
			retries = 0
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
	// Bounded retry on unknown wait4(-1) errors: retry up to
	// orphanDrainRetryBudget and abort loudly via the
	// spawn_orphan_drain_aborted event if the kernel keeps returning
	// errors, so operators distinguish "drain aborted" from
	// "clean shutdown".
	drainErrs := 0
	for {
		drained, err := s.drainOrphans(log)
		if err != nil {
			drainErrs++
			if drainErrs >= orphanDrainRetryBudget {
				log.Error().Err(err).
					Str("event", "spawn_orphan_drain_aborted").
					Int("retries", drainErrs).
					Msg("clawkerd: orphan drain retry budget exhausted; reparented orphans may still be alive")
				s.setReaperErr(fmt.Errorf("clawkerd: orphan drain retry budget exhausted: %w", err))
				// closeAllGates (not just closeDoneCh) for parity
				// with every other reaper-bailout path. Idempotent;
				// MainExited/orphanDrainCh are already closed above
				// on the normal flow but a future refactor that
				// reorders the close sequence stays safe.
				s.closeAllGates()
				return
			}
		} else {
			drainErrs = 0
		}
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
// PID 1. Return shape:
//
//   - (true,  nil) → ECHILD: no children left in the kernel's table;
//     phase-2 complete. Caller closes doneCh.
//   - (false, nil) → pid==0: children still alive, none ready right
//     now. Caller waits on sigchld/ticker and re-enters
//     drainOrphans.
//   - (false, err) → unknown wait4 error: caller bumps retry counter
//     and decides whether to abort.
//
// The pid==0/false distinction is load-bearing: under WNOHANG, pid==0
// means "I have unreaped children but none has changed state yet"
// — NOT "drained". Returning drained=true on pid==0 would close doneCh
// while reparented orphans are still alive (a backgrounded sleeper
// from the main child, a daemonized stage descendant), violating the
// package contract that phase 2 drains before doneCh closes.
func (s *spawnState) drainOrphans(log *logger.Logger) (bool, error) {
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if errors.Is(err, syscall.ECHILD) {
				return true, nil
			}
			log.Error().Err(err).
				Str("event", "spawn_wait4_orphan_failed").
				Msg("clawkerd: wait4 on orphan drain failed")
			return false, err
		}
		if pid == 0 {
			return false, nil
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
//
// Close order is load-bearing: MainExited → orphanDrain → Done.
// main()'s teardown selects on MainExited first, then triggers
// BeginOrphanDrain, then waits on Done — closing in that order
// preserves the happens-before contract those selects depend on.
// Reordering (e.g. closing Done first) would let Wait return before
// MainExited fired and break the teardown sequence.
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

func (s *spawnState) spawned() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proc != nil
}

// mapWaitStatus is the WaitStatus equivalent of mapExitCode.
func mapWaitStatus(ws syscall.WaitStatus) int {
	switch {
	case ws.Signaled():
		return 128 + int(ws.Signal())
	case ws.Exited():
		return ws.ExitStatus()
	}
	return 1
}

// forwardableSignals returns the inclusive set of signals the
// supervisor forwards to the child's process group. The list is
// authoritative — anything absent is intentionally NOT forwarded.
// Notable absences:
//
//   - SIGCHLD: reaper handles it; forwarding would corrupt reap loops
//   - SIGURG: Go runtime uses it for goroutine preemption
//   - SIGTTIN/SIGTTOU: ignored by the supervisor itself (main.go's
//     signal.Ignore) because once the child becomes the tty foreground
//     pgroup, any I/O by clawkerd would otherwise stop the daemon.
//     The kernel-delivered TTOU is for the SUPERVISOR's I/O attempt —
//     forwarding it to the child would be meaningless (the child is
//     the foreground; it doesn't get TTOU) and dangerous (default
//     action stops the child).
//   - program-error signals (SIGFPE/SIGILL/SIGSEGV/SIGBUS/SIGABRT/
//     SIGTRAP/SIGSYS): supervisor-side bugs; let them crash clawkerd
//     rather than masking via forward
//   - SIGKILL/SIGSTOP: cannot be caught at the OS level
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
		unix.SIGCONT,
		unix.SIGWINCH,
	}
}

// buildSysProcAttr returns the SysProcAttr for the spawn child:
// Setpgid:true so signals can target the child's pgroup, plus an
// optional Credential populated from user. user==nil means "stay
// root" (no Credential, kernel skips the setgroups/setgid/setuid
// sequence); user!=nil drops privileges kernel-side between fork and
// exec via setgroups → setgid → setuid (in that order; setgroups
// MUST run while still root). This is the security boundary
// clawkerd-as-PID-1 was built around — a regression that drops the
// Credential population (e.g. a refactor that loses cfg.user, or a
// wiring bug that resolves uid=0) silently re-introduces root in the
// user CMD.
//
// cttyFd >= 0 enables interactive-TTY mode: Foreground:true makes
// the kernel run tcsetpgrp(Ctty, child_pgrp) in the child between
// fork and exec, so the child's pgroup becomes the controlling
// terminal's foreground — keystrokes and Ctrl+C route to the user
// CMD instead of clawkerd. cttyFd is the child's fd number for the
// controlling TTY (0 when stdin is the TTY, since exec.Cmd maps
// Stdin to fd 0). Pass -1 for non-interactive runs.
//
// Setctty is intentionally NOT set: Go's exec validation rejects
// `both Setctty and Foreground set in SysProcAttr` (they are mutually
// exclusive). Foreground:true is the correct minimal primitive for
// "transfer foreground pgroup ownership" — the child stays in
// clawkerd's session.
func buildSysProcAttr(user *ExecUser, cttyFd int) *syscall.SysProcAttr {
	attr := &syscall.SysProcAttr{Setpgid: true}
	if cttyFd >= 0 {
		attr.Foreground = true
		attr.Ctty = cttyFd
	}
	if user != nil {
		attr.Credential = &syscall.Credential{
			Uid:    user.UID(),
			Gid:    user.GID(),
			Groups: user.Groups(),
		}
	}
	return attr
}

// stdinCttyFd reports the child fd number of the controlling TTY
// when stdin is a *os.File backed by a terminal, or -1 otherwise.
// Returns 0 for the TTY case because exec.Cmd.Stdin maps to fd 0 in
// the child. The TTY check uses TIOCGPGRP rather than golang.org/x/term
// to avoid pulling a new direct dependency for a one-line ioctl, and
// because TIOCGPGRP is portable across linux + darwin (TCGETS is
// linux-only) — the file is build-tagged unix and must compile for
// macOS dev hosts.
func stdinCttyFd(stdin io.Reader) int {
	f, ok := stdin.(*os.File)
	if !ok {
		return -1
	}
	if _, err := unix.IoctlGetInt(int(f.Fd()), unix.TIOCGPGRP); err != nil {
		return -1
	}
	return 0
}

// touchReadyFile creates or truncates path. O_TRUNC ensures
// HEALTHCHECK can't read a stale marker from a prior incarnation
// (clawkerd restart-loop, container recreated with a re-mounted
// volume). Without O_TRUNC the file's mtime would update but its
// "freshness" relative to the new clawkerd boot couldn't be asserted
// — a race-prone source of "container reports healthy before user
// CMD has actually started".
func touchReadyFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}
