// Command clawkerd is the per-container agent daemon. It runs as
// PID 1 of the agent container, owns the per-container
// ClawkerdService listener that the CP dials for command dispatch,
// and supervises the user CMD across its lifetime.
//
// Boot sequence:
//
//  1. Read bootstrap material delivered by the CLI to
//     consts.BootstrapDir (cert.pem, key.pem, ca.pem, assertion.jwt).
//     cert/key/ca are loaded into the listener's TLS config; the
//     assertion JWT is held in memory for the CP-driven Register
//     handshake (clawkerd exchanges it at Hydra for an access token
//     when CP sends RegisterRequired on the Session stream).
//  2. Start the ClawkerdService mTLS listener on
//     consts.DefaultClawkerdPort. The listener pins peer CN to
//     consts.ContainerCP so no other agent's CA-signed cert can
//     connect.
//  3. Resolve the unprivileged container user via $CLAWKER_USER and
//     /etc/passwd; build the spawn state but do NOT spawn yet —
//     handleAgentReady triggers the spawn when CP-driven init
//     completes. Privilege drop happens in the child via
//     SysProcAttr.Credential; clawkerd stays root.
//  4. Wait for either ctx.Done (SIGTERM/SIGINT) or main child exit.
//     On SIGTERM: forward to the child pgroup, escalate to SIGKILL
//     after grace, then Stop (force-close) the listener and drain
//     reparented orphans. On main exit: Stop first so in-flight
//     session.go pipelines see a closed listener before the reaper
//     transitions to Wait4(-1) — see spawnState's reaper phasing.
//     Exit with the child's bash-convention exit code so Docker's
//     restart-on-failure machinery sees the right value.
//
// Identity / registration: clawkerd performs a one-time, CP-driven
// Register call when CP sends a RegisterRequired Command on the
// Session stream. clawkerd exchanges the CLI-signed client_assertion
// JWT at Hydra for an access token, mTLS-dials CP's AgentService, and
// calls Register. CP captures the live mTLS peer's cert thumbprint at
// handler entry and writes the (thumbprint, container_id) row into
// agentregistry. The assertion is single-use; subsequent Sessions for
// the same container observe an existing registry row and skip
// Register.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// logsDir is where clawkerd writes its rotated log file under
// the container-side clawker log directory so an operator triaging issues finds early-boot
// bootstrap failures AND structured log events under one directory.
// The Dockerfile pre-creates the path.
const logsDir = consts.CPLogsPath

// logFilename is the rotated log file's basename — distinct from
// clawker.log on the host so an operator triaging issues can tell at a
// glance which side wrote which entries.
const logFilename = "clawkerd.log"

// shutdownGrace bounds the SIGTERM→SIGKILL escalation window applied
// to the user CMD on container stop. Matches Docker's default
// --stop-timeout (10s) so a clean operator-driven `docker stop` does
// not race docker's own SIGKILL.
const shutdownGrace = 10 * time.Second

// exitCodeConfig is returned for deterministic pre-spawn config
// failures (missing required env, /etc/passwd parse fails, malformed
// bootstrap material). Distinct from the generic exit-1 transient
// path so an operator running `restart: on-failure:max-retries=N`
// can wire trip-and-stop on this code instead of restart-looping
// the same broken config 3+ times. Unix tradition: 2 = configuration
// error.
const exitCodeConfig = 2

func main() {
	// Ignore SIGTTIN/SIGTTOU before any other setup. Once the spawn
	// child becomes the controlling tty's foreground pgroup (via
	// Foreground:true in spawn_unix.go), clawkerd is a background
	// process w.r.t. that tty. Any read/write by clawkerd against
	// stdin/stdout/stderr (final shutdown logs to os.Stderr fallback,
	// fmt.Fprintf paths) would trigger SIGTTOU/SIGTTIN, whose default
	// action is "stop the process" — clawkerd freezes in T state, the
	// container never exits, and the host's `clawker run` never sees
	// container teardown so it cannot restore the host terminal mode
	// the user sees as "frozen terminal after agent exit". Lifted
	// from tini's configure_signals: same shape, same reason.
	// signal.Ignore installs SIG_IGN at the OS level so the kernel
	// drops these signals before they ever stop the daemon.
	signal.Ignore(syscall.SIGTTIN, syscall.SIGTTOU)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)

	// Logger init has to land BEFORE run() so every subsequent event
	// can flow through it. If the file-writer init itself fails we have
	// no other channel to surface the bootstrap problem on, so write to
	// stderr and exit non-zero — that's the only stderr write in the
	// whole daemon. No `defer` on the cleanup paths because os.Exit
	// skips deferred funcs; ordering is explicit instead.
	log, err := logger.New(logger.Options{
		LogsDir:  logsDir,
		Filename: logFilename,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawkerd: logger init failed: %v\n", err)
		stop()
		os.Exit(1)
	}

	exitCode, runErr := run(ctx, log)
	if runErr != nil {
		log.Error().Err(runErr).Str("event", "shutdown").Msg("clawkerd exiting with error")
		if exitCode == 0 {
			exitCode = 1
		}
	} else {
		log.Info().Int("exit_code", exitCode).Str("event", "shutdown").Msg("clawkerd exiting")
	}
	if err := log.Close(context.Background()); err != nil {
		// Fallback to stderr because the logger itself is what's
		// closing — any zerolog-routed surface is unreliable here.
		fmt.Fprintf(os.Stderr, "clawkerd: logger close failed: %v\n", err)
	}
	stop()
	os.Exit(exitCode)
}

// run drives the daemon lifecycle. The returned exit code is
// propagated to the container's exit (Docker `restart: on-failure`
// reads it). Bash-convention encoding (128+signum for signaled
// child) flows through spawnState.Wait. A non-nil error indicates a
// pre-spawn bootstrap failure; main() forces a non-zero exit in
// that case so a misconfigured container fails loud rather than
// looking healthy.
func run(ctx context.Context, log *logger.Logger) (int, error) {
	agentName := os.Getenv(consts.EnvAgent)
	// CLAWKER_PROJECT is allowed to be empty — empty is the
	// global-scope-agent case (2-segment naming, docker.ContainerName
	// behavior) where the AgentFullName is "clawker.<agent>".
	project := os.Getenv(consts.EnvProject)
	if agentName == "" {
		return exitCodeConfig, fmt.Errorf("required env not set: %s", consts.EnvAgent)
	}

	// Bind agent + project on every subsequent log line so a multi-
	// container clawkerd log (if/when shared on disk via volume mount)
	// stays trivially filterable. Match the field names AdminService
	// uses on its side so a CP/agent log correlation grep on `agent=`
	// joins both halves of the handshake.
	log = log.With("agent", agentName, "project", project)
	log.Info().
		Str("event", "boot").
		Str("bootstrap_dir", consts.BootstrapDir).
		Msg("clawkerd starting")

	// User-facing boot progress on the attached TTY. clawkerd is PID 1
	// of the agent container; until handleAgentReady transfers the
	// controlling tty's foreground pgroup to the spawned user CMD, the
	// user otherwise sees a blank terminal during CP-driven init.
	// Threaded through the listener → server → session chain so step
	// boundaries observed in dispatch/send/handleAgentReady can drive
	// the spinner. nil-safe, so a wiring oversight is benign.
	progress := newProgressReporter(os.Stdout)
	progress.Banner("Starting Clawker agent...")
	defer progress.Stop()

	// Resolve the unprivileged user the spawn child will run as.
	// Default to consts.ContainerUser ("claude") when CLAWKER_USER is
	// unset so a hand-built image without the Dockerfile-set env
	// still gets a sensible identity.
	userSpec := os.Getenv(consts.EnvClawkerUser)
	if userSpec == "" {
		userSpec = consts.ContainerUser
	}
	passwdPath, groupPath := passwdGroupPaths()
	execUser, err := resolveUser(userSpec, passwdPath, groupPath)
	if err != nil {
		log.Error().Err(err).
			Str("event", "resolve_user_failed").
			Str("user", userSpec).
			Msg("clawkerd: cannot resolve container user; refusing to spawn")
		return exitCodeConfig, fmt.Errorf("resolve user %q: %w", userSpec, err)
	}

	boot, err := readBootstrap(consts.BootstrapDir)
	if err != nil {
		log.Error().Err(err).Str("event", "bootstrap_read_failed").Msg("read bootstrap")
		return exitCodeConfig, fmt.Errorf("read bootstrap: %w", err)
	}

	// registerCoordinator drives the CP-triggered Register handshake.
	// CP sends RegisterRequired on the Session bidi stream when it
	// observes Miss at Hello time; clawkerd routes it through this
	// coordinator. Shared across every Session for the process
	// lifetime so the (single-use) Hydra assertion is consumed at
	// most once. CLAWKER_CP_HYDRA_URL + CLAWKER_CP_AGENT_ADDR may be
	// empty at boot — Run() reports the failure on the first attempt.
	register := newRegisterCoordinator(
		boot,
		os.Getenv(consts.EnvClawkerdHydraURL),
		os.Getenv(consts.EnvClawkerdAgentAddr),
		agentName,
		project,
	)

	// Build the spawn state. The child is NOT forked here —
	// handleAgentReady invokes spawnEntry when CP dispatches
	// AgentReady, the terminal step of the CP-driven init plan. The
	// reaper's Wait4(-1, WNOHANG) phase 2 is HELD by default; main()
	// releases it via BeginOrphanDrain only AFTER the listener Stop
	// (force-close, NOT GracefulStop — see teardown below) and after
	// the main-child-exit cascade has torn down session.go's
	// in-flight ShellCommand pipelines, so phase 2 never races
	// c.Wait for stage children.
	spawn := newSpawnState(log)
	spawnEntry := func() error {
		return spawn.Run(spawnConfig{
			argv: os.Args[1:],
			// Leave Dir empty so the child inherits PID 1's cwd, which
			// the kernel set from Docker's WorkingDir (image WORKDIR or
			// HostConfig.WorkingDir). Mirrors tini/gosu — neither
			// chdirs; both let Docker's WorkingDir pass through to the
			// user CMD.
			env:    os.Environ(),
			user:   execUser,
			stdin:  os.Stdin,
			stdout: os.Stdout,
			stderr: os.Stderr,
			log:    log,
			// HEALTHCHECK reads /var/run/clawker/ready. Touched
			// immediately after exec.Cmd.Start returns nil so the
			// healthy transition matches the user CMD becoming a
			// real process.
			readyFile: consts.ReadyMarkerPath,
		})
	}

	// listenerFatalCh fires once if the Serve goroutine dies on a
	// non-stop error or panics. Without this signal, main() sits on
	// ctx.Done with a bricked listener — container looks alive but
	// CP cannot dispatch commands. Buffered cap 1: the listener fires
	// at most once per failure, but a future refactor that retries
	// Serve must not block here.
	listenerFatalCh := make(chan error, 1)
	onListenerFatal := func(err error) {
		select {
		case listenerFatalCh <- err:
		default:
		}
	}

	clawkerdSrv, err := startClawkerdListener(boot, register, spawnEntry, onListenerFatal, log, progress)
	if err != nil {
		log.Error().Err(err).Str("event", "clawkerd_listener_start_failed").Msg("start clawkerd listener")
		// Wiring bugs and malformed bootstrap material are deterministic
		// — exit 2 so `restart: on-failure:max-retries=N` trips and stops
		// instead of restart-looping the same broken state forever. Bind
		// failures (port-in-use after a teardown race) stay transient.
		code := 1
		if errors.Is(err, errListenerConfig) {
			code = exitCodeConfig
		}
		return code, fmt.Errorf("start clawkerd listener: %w", err)
	}

	log.Info().Str("event", "daemon_idle").Msg("entering daemon idle loop; CP may dial Session at any time")

	// Wait for either signal-driven shutdown, listener fatal, OR main
	// child exit. MainExited fires once spawnState's reaper has reaped
	// the user CMD pid (phase 1 done) but BEFORE phase 2's Wait4(-1)
	// orphan drain — so we have a clean window to Stop the listener
	// before releasing phase 2.
	var listenerFatalErr error
	select {
	case <-ctx.Done():
		log.Info().Str("event", "shutdown_signal_received").Msg("SIGTERM/SIGINT received")
		// SIGTERM-before-spawn: a signal arriving before CP
		// dispatched AgentReady means no child to forward to and
		// no reaper goroutine running. Skip the MainExited wait —
		// it would never fire — and proceed straight to listener
		// teardown.
		if spawn.Spawned() {
			spawn.Stop(shutdownGrace)
			<-spawn.MainExited()
		} else {
			log.Info().
				Str("event", "shutdown_before_spawn").
				Msg("SIGTERM arrived before AgentReady; no user CMD to terminate")
		}
	case <-spawn.MainExited():
		log.Info().Str("event", "main_child_exited").Msg("main child exited")
	case listenerFatalErr = <-listenerFatalCh:
		// Listener Serve died (Serve returned non-stop error or the
		// goroutine panicked). The daemon cannot dispatch any further
		// commands — proceed to teardown. If the user CMD is running,
		// terminate it gracefully so we don't strand a child while
		// the container exits.
		log.Error().Err(listenerFatalErr).
			Str("event", "listener_fatal_received").
			Msg("clawkerd: listener died; tearing down daemon")
		if spawn.Spawned() {
			spawn.Stop(shutdownGrace)
			<-spawn.MainExited()
		}
	}

	// Tear down the gRPC listener BEFORE phase 2 begins so
	// session.go's exec.Cmd.Wait calls complete (their stage children
	// remain reapable to those calls; the reaper's Wait4(mainPID)
	// already finished and Wait4(-1) hasn't started). Without this
	// ordering, Wait4(-1) steals stage-child pids and session.go's
	// c.Wait returns ECHILD with bogus exit codes for in-flight
	// pipelines.
	//
	// Use Stop (force-close), NOT GracefulStop. The user CMD has
	// already exited (or we're shutting down post-SIGTERM); there is
	// nothing graceful left to do. CP holds the Session bidi stream
	// open from its side and only releases on stream close, so
	// GracefulStop would hang indefinitely waiting for the streaming
	// RPC handler to return. Force-close the listener: CP observes a
	// connection error on its end (which is the correct signal — the
	// agent is gone), in-flight ShellCommands were already drained by
	// main-child exit cascade, and the container can finally
	// terminate so the host `clawker run` exits raw-tty mode.
	log.Info().Str("event", "clawkerd_listener_stopping").Msg("stopping listener")
	clawkerdSrv.Stop()
	log.Info().Str("event", "clawkerd_listener_stopped").Msg("listener torn down")

	// Phase 2: drain reparented orphans now that session.go can no
	// longer dispatch new ShellCommand pipelines. Idempotent + safe
	// even when the child never spawned (gates already closed via
	// the spawn-error path).
	spawn.BeginOrphanDrain()

	exitCode := spawn.Wait()
	// Surface the spawn error as runErr so a Run failure (no child
	// ever forked) propagates to the shutdown log line and main()'s
	// non-zero-exit path. SpawnErr also reports reaper-bailout causes
	// (retry-budget exhaustion, ECHILD on main, panic recovery) so a
	// supervisor that aborted phase 1/2 without recording finalWS
	// surfaces its cause on the shutdown line instead of forcing
	// operators to grep for `spawn_*_aborted` separately.
	if err := spawn.SpawnErr(); err != nil {
		return exitCode, fmt.Errorf("spawn user CMD: %w", err)
	}
	if listenerFatalErr != nil {
		// Listener died but the user CMD reaped cleanly — exit code
		// reflects the child but the runErr surfaces the listener
		// cause so operators see it on the shutdown line.
		return exitCode, fmt.Errorf("listener fatal: %w", listenerFatalErr)
	}
	return exitCode, nil
}

// bootstrap is the in-memory copy of the four bootstrap files
// clawkerd reads at boot. Assertion is the single-use Hydra
// client_assertion JWT clawkerd exchanges for an access token when
// CP triggers the Register handshake.
type bootstrap struct {
	CertPEM, KeyPEM, CACertPEM []byte
	Assertion                  string
}

// readBootstrap reads the four bootstrap files from dir. Missing files
// fail loudly — a partial boot is a security regression (e.g. cert
// missing would let clawkerd proceed without the cert pinning that
// defends against tmpfs swap).
func readBootstrap(dir string) (*bootstrap, error) {
	read := func(name string) ([]byte, error) {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("%s is empty", name)
		}
		return data, nil
	}

	cert, err := read(consts.BootstrapCertFile)
	if err != nil {
		return nil, err
	}
	key, err := read(consts.BootstrapKeyFile)
	if err != nil {
		return nil, err
	}
	ca, err := read(consts.BootstrapCAFile)
	if err != nil {
		return nil, err
	}
	assertion, err := read(consts.BootstrapAssertionFile)
	if err != nil {
		return nil, err
	}

	return &bootstrap{
		CertPEM:   cert,
		KeyPEM:    key,
		CACertPEM: ca,
		Assertion: strings.TrimSpace(string(assertion)),
	}, nil
}
