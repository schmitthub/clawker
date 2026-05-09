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
//     after grace, then GracefulStop the listener and drain
//     reparented orphans. On main exit: GracefulStop first so
//     in-flight session.go pipelines drain before the reaper
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
// /var/log/clawker/ so an operator triaging issues finds early-boot
// bootstrap failures AND structured log events under one directory.
// The Dockerfile pre-creates the path; clawkerd runs as root inside
// the container (PID 1, never drops privileges itself — privilege
// drop happens in the child via SysProcAttr.Credential), so 0o755
// is fine.
const logsDir = "/var/log/clawker"

// logFilename is the rotated log file's basename — distinct from
// clawker.log on the host so an operator triaging issues can tell at a
// glance which side wrote which entries.
const logFilename = "clawkerd.log"

// shutdownGrace bounds the SIGTERM→SIGKILL escalation window applied
// to the user CMD on container stop. Matches Docker's default
// --stop-timeout (10s) so a clean operator-driven `docker stop` does
// not race docker's own SIGKILL.
const shutdownGrace = 10 * time.Second

func main() {
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
	if err := log.Close(); err != nil {
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
	// CLAWKER_PROJECT is allowed to be empty — empty matches the
	// 2-segment naming case (docker.ContainerName behavior) where the
	// canonical CN is "clawker.<agent>".
	project := os.Getenv(consts.EnvProject)
	if agentName == "" {
		return 1, fmt.Errorf("required env not set: %s", consts.EnvAgent)
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
		return 1, fmt.Errorf("resolve user %q: %w", userSpec, err)
	}

	boot, err := readBootstrap(consts.BootstrapDir)
	if err != nil {
		log.Error().Err(err).Str("event", "bootstrap_read_failed").Msg("read bootstrap")
		return 1, fmt.Errorf("read bootstrap: %w", err)
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
	// releases it via BeginOrphanDrain only AFTER GracefulStop
	// drains session.go's in-flight ShellCommand pipelines, so
	// phase 2 never races c.Wait for stage children.
	spawn := newSpawnState(log)
	spawnEntry := func() error {
		return spawn.Run(spawnConfig{
			argv: os.Args[1:],
			// Set Dir to the unprivileged user's home; PID 1 inherits
			// / from Docker, but the user CMD expects $HOME as cwd.
			dir:    execUser.Home,
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

	clawkerdSrv, err := startClawkerdListener(boot, register, spawnEntry, log)
	if err != nil {
		log.Error().Err(err).Str("event", "clawkerd_listener_start_failed").Msg("start clawkerd listener")
		return 1, fmt.Errorf("start clawkerd listener: %w", err)
	}

	log.Info().Str("event", "daemon_idle").Msg("entering daemon idle loop; CP may dial Session at any time")

	// Wait for either signal-driven shutdown OR main child exit.
	// MainExited fires once spawnState's reaper has reaped the user
	// CMD pid (phase 1 done) but BEFORE phase 2's Wait4(-1) orphan
	// drain — so we have a clean window to GracefulStop the listener
	// (drains session.go's c.Wait calls) before releasing phase 2.
	select {
	case <-ctx.Done():
		log.Info().Str("event", "shutdown_signal_received").Msg("SIGTERM/SIGINT received")
		// SIGTERM-before-spawn: a signal arriving before CP
		// dispatched AgentReady means no child to forward to and
		// no reaper goroutine running. Skip the MainExited wait —
		// it would never fire — and proceed straight to listener
		// teardown.
		if spawn.Started() {
			spawn.Stop(shutdownGrace)
			<-spawn.MainExited()
		} else {
			log.Info().
				Str("event", "shutdown_before_spawn").
				Msg("SIGTERM arrived before AgentReady; no user CMD to terminate")
		}
	case <-spawn.MainExited():
		log.Info().Str("event", "main_child_exited").Msg("main child exited")
	}

	// Tear down the gRPC listener BEFORE phase 2 begins so
	// session.go's exec.Cmd.Wait calls complete (their stage children
	// remain reapable to those calls; the reaper's Wait4(mainPID)
	// already finished and Wait4(-1) hasn't started). Without this
	// ordering, Wait4(-1) steals stage-child pids and session.go's
	// c.Wait returns ECHILD with bogus exit codes for in-flight
	// pipelines.
	log.Info().Str("event", "clawkerd_listener_stopping").Msg("graceful stop")
	clawkerdSrv.GracefulStop()
	log.Info().Str("event", "clawkerd_listener_stopped").Msg("listener torn down")

	// Phase 2: drain reparented orphans now that session.go can no
	// longer dispatch new ShellCommand pipelines. Idempotent + safe
	// even when the child never spawned (gates already closed via
	// the spawn-error path).
	spawn.BeginOrphanDrain()

	exitCode := spawn.Wait()
	// Surface the spawn error as runErr so a Run failure (no child
	// ever forked) propagates to the shutdown log line and main()'s
	// non-zero-exit path. Without this, a failed AgentReady spawn
	// silently exits 1 with `event=shutdown` carrying no error
	// field — operators have to grep for `agent_ready_spawn_failed`
	// to find the cause.
	if err := spawn.SpawnErr(); err != nil {
		return exitCode, fmt.Errorf("spawn user CMD: %w", err)
	}
	return exitCode, nil
}

// bootstrap mirrors the CLI's per-agent registration material on disk.
// Assertion is the single-use Hydra client_assertion JWT clawkerd
// exchanges for an access token when CP triggers the Register
// handshake.
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
