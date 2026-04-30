// Command clawkerd is the per-container agent daemon. It runs as a
// backgrounded child of the container entrypoint shell, owns the
// per-container ClawkerdService listener that the CP dials for command
// dispatch, and idles for the container's lifetime.
//
// Boot sequence:
//
//  1. Read bootstrap material delivered by the CLI to
//     consts.BootstrapDir (cert.pem, key.pem, ca.pem, assertion.jwt,
//     verifier). cert/key/ca are loaded into the listener's TLS
//     config; assertion + verifier are read in but unused by clawkerd
//     today (CLI is the registry writer). They stay on disk and stay
//     loaded so the agent→CP RPCs landing in upcoming branches can
//     consume them without re-deriving the bootstrap surface.
//  2. Start the ClawkerdService mTLS listener on
//     consts.DefaultClawkerdPort. The listener pins peer CN to
//     consts.ContainerCP so no other agent's CA-signed cert can
//     connect.
//  3. Idle on ctx.Done — daemon lifetime is bound to container
//     lifetime. The :7700 listener stays up for CP to dial Session
//     repeatedly; CP→clawkerd connection breaks are logged from the
//     listener side but do not kill the daemon.
//
// Identity / registration: the CLI writes the agentregistry row at
// container CREATE time (via host-side sqlite). CP reads the registry
// when it dials clawkerd's listener and verifies the peer cert
// thumbprint against the registered row — provenance attestation flows
// entirely through that path, no clawkerd outbound RPC required.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// logsDir is where clawkerd writes its rotated log file. Co-located
// with the entrypoint's stdout/stderr capture target (/var/log/clawker/)
// so an operator triaging issues finds bootstrap-time stderr writes
// AND structured log events under one directory. The directory is
// created by the entrypoint before clawkerd launches; clawkerd itself
// runs as root inside the container (the entrypoint backgrounds it
// before privilege drop), so 0o755 is fine.
const logsDir = "/var/log/clawker"

// logFilename is the rotated log file's basename — distinct from
// clawker.log on the host so an operator triaging issues can tell at a
// glance which side wrote which entries.
const logFilename = "clawkerd.log"

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

	exitCode := 0
	if err := run(ctx, log); err != nil {
		log.Error().Err(err).Str("event", "shutdown").Msg("clawkerd exiting with error")
		exitCode = 1
	} else {
		log.Info().Str("event", "shutdown").Msg("clawkerd exiting cleanly")
	}
	if err := log.Close(); err != nil {
		// Fallback to stderr because the logger itself is what's
		// closing — any zerolog-routed surface is unreliable here.
		fmt.Fprintf(os.Stderr, "clawkerd: logger close failed: %v\n", err)
	}
	stop()
	os.Exit(exitCode)
}

func run(ctx context.Context, log *logger.Logger) error {
	agentName := os.Getenv(consts.EnvAgent)
	// CLAWKER_PROJECT is allowed to be empty — empty matches the
	// 2-segment naming case (docker.ContainerName behavior) where the
	// canonical CN is "clawker.<agent>".
	project := os.Getenv(consts.EnvProject)
	if agentName == "" {
		return fmt.Errorf("required env not set: %s", consts.EnvAgent)
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

	boot, err := readBootstrap(consts.BootstrapDir)
	if err != nil {
		log.Error().Err(err).Str("event", "bootstrap_read_failed").Msg("read bootstrap")
		return fmt.Errorf("read bootstrap: %w", err)
	}

	clawkerdSrv, err := startClawkerdListener(boot, log)
	if err != nil {
		log.Error().Err(err).Str("event", "clawkerd_listener_start_failed").Msg("start clawkerd listener")
		return fmt.Errorf("start clawkerd listener: %w", err)
	}
	defer func() {
		log.Info().Str("event", "clawkerd_listener_stopping").Msg("graceful stop")
		clawkerdSrv.GracefulStop()
		log.Info().Str("event", "clawkerd_listener_stopped").Msg("listener torn down")
	}()

	log.Info().Str("event", "daemon_idle").Msg("entering daemon idle loop; CP may dial Session at any time")

	// clawkerd is a DAEMON. Its lifetime is the container's lifetime,
	// bounded only by SIGTERM (ctx cancel). The :7700 ClawkerdService
	// listener (already serving) is the entire RPC surface — CP dials
	// in to dispatch commands. CP→clawkerd connection breaks are logged
	// from the listener side but do not kill the daemon.
	<-ctx.Done()
	log.Info().Str("event", "shutdown_signal_received").Msg("SIGTERM received; tearing down clawkerd")
	return nil
}

// bootstrap mirrors the CLI's per-agent registration material on disk.
// Assertion + Verifier are loaded but unused in this branch; they stay
// loaded so the agent→CP RPCs landing in upcoming branches can consume
// them without re-deriving the bootstrap surface.
type bootstrap struct {
	CertPEM, KeyPEM, CACertPEM []byte
	Assertion                  string
	Verifier                   string
}

// readBootstrap reads the five bootstrap files from dir. Missing files
// fail loudly — a partial boot is a security regression (e.g. cert
// missing while verifier present would let clawkerd proceed without
// the cert pinning that defends against tmpfs swap).
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
	verifier, err := read(consts.BootstrapVerifierFile)
	if err != nil {
		return nil, err
	}

	return &bootstrap{
		CertPEM:   cert,
		KeyPEM:    key,
		CACertPEM: ca,
		Assertion: strings.TrimSpace(string(assertion)),
		Verifier:  strings.TrimSpace(string(verifier)),
	}, nil
}
