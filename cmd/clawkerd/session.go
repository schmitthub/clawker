package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

// chunkBufSize is the read buffer for stdout/stderr drainers. 32 KiB
// is the typical Go bufio default and is large enough that bursty
// output coalesces into one Response per syscall but small enough
// that real-time progress (e.g. apt's per-line output) reaches CP
// without long buffering delays.
const chunkBufSize = 32 * 1024

// sendQueueDepth is the buffer size of the per-Session response
// channel. The single sender goroutine drains this into stream.Send.
// Depth 64 absorbs short bursts (multiple stages writing stderr at
// once) without backpressuring the producer goroutines.
const sendQueueDepth = 64

// runSession is the entry point invoked by clawkerdServer.Session.
// It owns the bidi gRPC stream for the lifetime of one CP-side dial:
// receives Commands, spawns per-command worker goroutines, serializes
// Response writes through a single sender goroutine, and tears
// everything down on stream close or context cancel.
//
// register is the CP-driven Register coordinator (shared across
// every Session for a single clawkerd process). RegisterRequired
// Commands route to register.Run; the result rides back on a
// RegisterDone Response correlated by command_id.
//
// spawnEntry is the AgentReady spawn-trigger thunk. handleAgentReady
// invokes it to fork the user CMD; threaded through as a non-optional
// dependency so a wiring bug fails loud at clawkerdServer construction
// rather than silently no-op'ing on first AgentReady.
func runSession(stream clawkerdv1.ClawkerdService_SessionServer, log *logger.Logger, register *registerCoordinator, spawnEntry func() error, progress *progressReporter) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	// Audit log: every Session entry from the listener emits an Info
	// event with peer CN + cert thumbprint. Sessions are long-lived
	// (server-streaming, agent's lifetime), so two log lines per
	// Session are negligible. The ContainerCP CN-pin and ClientAuth
	// EKU assertion run upstream in pinPeerCNToCP — by the time
	// runSession executes, the peer is the trusted CP.
	startedAt := time.Now()
	peerCN, peerThumbprint := peerSummary(stream.Context())
	log.Info().
		Str("event", "session_started").
		Str("peer_cn", peerCN).
		Str("peer_thumbprint", peerThumbprint).
		Msg("clawkerd: Session started")
	defer func() {
		// Settle the progress reporter on every Session exit so a
		// transport break or init failure (no AgentReady received)
		// quiets further writes. Idempotent + race-safe with
		// handleAgentReady's Final.
		progress.Stop()
		log.Info().
			Str("event", "session_ended").
			Str("peer_cn", peerCN).
			Dur("duration", time.Since(startedAt)).
			Msg("clawkerd: Session ended")
	}()

	s := &session{
		log:        log,
		stream:     stream,
		sendCh:     make(chan *clawkerdv1.Response, sendQueueDepth),
		cmds:       make(map[string]*runningCommand),
		register:   register,
		spawnEntry: spawnEntry,
		progress:   progress,
	}

	// Sender goroutine: single writer to stream.Send (gRPC's
	// SendMsg is NOT goroutine-safe). All producers push to sendCh.
	// Cancel handed in so a Send failure (broken stream) tears the
	// session ctx down deterministically — without that, producer
	// goroutines blocked on `sendCh <- resp` would park until the
	// receiver loop independently noticed the broken transport,
	// stretching the truncated-output window arbitrarily.
	var senderWG sync.WaitGroup
	senderWG.Add(1)
	go func() {
		defer senderWG.Done()
		// PID-1 resilience: a panic inside runSender (e.g. from a
		// malformed gRPC frame nil-derefing inside stream.Send) would
		// otherwise kill clawkerd. Recover, cancel the session ctx so
		// producer goroutines unblock, and let the receiver loop
		// surface a normal teardown.
		defer recoverGoroutine(s.log, "session_sender", cancel)
		s.runSender(ctx, cancel)
	}()

	// Receive loop: routes inbound Commands until the stream closes
	// or ctx is cancelled.
	recvErr := s.runReceiver(ctx)

	// Stop accepting new commands and tear down anything still
	// running. Cancelling each command's ctx propagates SIGKILL via
	// exec.CommandContext.
	cancel()
	s.shutdownRunning()

	// Wait for the sender goroutine to drain. It exits when ctx is
	// done, but there may be in-flight Responses we want delivered.
	// shutdownRunning above doesn't drain sendCh — sender does that
	// via the ctx-done branch and any final sends after cancel are
	// best-effort.
	senderWG.Wait()

	return recvErr
}

// session holds per-stream state. Lifetime == one Session RPC.
type session struct {
	log    *logger.Logger
	stream clawkerdv1.ClawkerdService_SessionServer

	sendCh chan *clawkerdv1.Response

	mu   sync.Mutex
	cmds map[string]*runningCommand

	// register coordinates the CP-driven Register handshake. Shared
	// with the parent clawkerdServer so the (single-use) Hydra
	// assertion is consumed at most once across all Sessions for
	// this process.
	register *registerCoordinator

	// spawnEntry forks the user CMD on AgentReady. Closed over the
	// spawnState built in main(); shared across every Session for
	// the process lifetime. nil rejects with Error{IO_ERROR} so a
	// wiring bug surfaces as a typed terminal failure rather than a
	// silent timeout.
	spawnEntry func() error

	// progress drives the user-facing TTY boot-status reporter (plain
	// status lines, no animation). Owned by main(); shared across every
	// Session for the process lifetime so a CP reconnect after the user
	// CMD has spawned silently no-ops on the already-stopped reporter
	// rather than re-emitting init banners. nil-tolerant; test fixtures
	// leave it unset.
	progress *progressReporter
}

// handleRegisterRequired drives the CP-triggered Register handshake.
// Runs in a goroutine so the Session receive loop stays responsive
// (the Hydra exchange + AgentService.Register chain takes seconds).
// The result rides back on a RegisterDone Response correlated by
// command_id.
//
// If the registerCoordinator is nil (test wiring without a coordinator),
// reply with ok=false so the CP-side dialer doesn't hang waiting for
// a Response that will never come.
//
// Panic safety: Run dials Hydra and CP, decodes JSON, parses certs —
// any panic in that chain (nil pointer in a future refactor, malformed
// input from a misbehaving Hydra) would otherwise kill clawkerd; as
// PID 1, that exits the container. Recover, log, reply ok=false so
// CP sees a terminal outcome instead of timing out.
func (s *session) handleRegisterRequired(ctx context.Context, commandID string) {
	if s.register == nil {
		s.send(ctx, &clawkerdv1.Response{
			CommandId: commandID,
			Payload: &clawkerdv1.Response_RegisterDone{
				RegisterDone: &clawkerdv1.RegisterDone{
					Ok:    false,
					Error: "clawkerd has no register coordinator wired",
				},
			},
		})
		return
	}
	go func() {
		// Top-level recover wraps the whole goroutine — register.Run
		// AND s.send. s.send can panic on a torn-down sendCh, so a
		// per-call recover would leak panics. Mirrors handleAgentReady.
		// Enriched logger so a panic surfaces command_id in the
		// goroutine_panic event.
		recoverLog := s.log.With("command_id", commandID)
		defer recoverGoroutine(recoverLog, "register_required", nil)
		var (
			ok     bool
			errMsg string
		)
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.log.Error().
						Interface("panic", r).
						Bytes("stack", debug.Stack()).
						Str("event", "register_panic").
						Str("command_id", commandID).
						Msg("clawkerd: registerCoordinator.Run panicked; replying RegisterDone{ok=false}")
					ok = false
					errMsg = fmt.Sprintf("register panic: %v", r)
				}
			}()
			ok, errMsg = s.register.Run(ctx, s.log)
		}()
		s.send(ctx, &clawkerdv1.Response{
			CommandId: commandID,
			Payload: &clawkerdv1.Response_RegisterDone{
				RegisterDone: &clawkerdv1.RegisterDone{
					Ok:    ok,
					Error: errMsg,
				},
			},
		})
	}()
}

// stageReaperPanicHookForTest is the panic-injection seam for the
// stage-reaper recover regression test. nil in production. When
// non-nil, fires AFTER c.Wait() returns and BEFORE the
// finalStageErrCh send, exercising the recover's deadlock-prevention
// branch (always send sentinel so the worker at <-finalStageErrCh
// unblocks).
//
// Test-only: set before runShellCommand starts and unset in t.Cleanup.
// Concurrent access against a running reaper is racy by design — the
// production path reads it once per stage on a cold path with no
// synchronization, mirroring how Go test seams elsewhere in the
// codebase work.
var stageReaperPanicHookForTest func(stageIndex int, isFinal bool)

// handleAgentReady is the terminal step of CP-driven init. Invokes
// s.spawnEntry (closed over the spawnState built in main()) which
// forks the user CMD as PID 1's only child via spawnState.Run.
// Source of truth for "already spawned" is spawnState's CAS.
//
// Outcomes (all replied as a Response correlated by commandID):
//   - Spawn succeeds → Done{0}. Listener stays live; main()'s wait
//     loop sees the eventual MainExited signal when the child exits.
//   - errAlreadySpawned → Done{0}. CP reconnect path: a previous
//     AgentReady this process already forked the child. Reply
//     idempotently rather than refusing — CP's plan would otherwise
//     stall on a stale Session.
//   - spawnEntry == nil → Error{IO_ERROR}. Wiring bug: handleAgentReady
//     fired before main() set the entry. Same classification as the
//     panic-recovery branch (both are clawkerd-internal bugs); CP sees
//     a typed terminal failure rather than a silent timeout.
//   - Any other spawn error → Error{IO_ERROR}, with the spawn error
//     message in detail. The reaper has already closed Done on the
//     spawn-error path, so the daemon will exit non-zero shortly
//     after this Response ships.
//
// Spawned synchronously on the Session goroutine (no `go func()`
// wrapper) — Run installs the goroutines and returns immediately on
// success. Wrapping in a goroutine would race with the reply: CP
// could see Done{0} before exec.Cmd.Start has actually forked, and
// the next Session command (e.g. Hello reach-check) would race the
// child's first scheduling slice. Keep this synchronous so the wire
// order matches the kernel order.
func (s *session) handleAgentReady(ctx context.Context, commandID string) {
	// Mirror the handleRegisterRequired recover pattern: a panic in
	// the spawn path would otherwise kill clawkerd (PID 1) and the
	// container with no diagnostic surface. Recover, log structurally,
	// surface as Error{IO_ERROR} so CP sees a terminal Response and
	// the audit log carries the panic event.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		s.log.Error().
			Interface("panic", r).
			Bytes("stack", debug.Stack()).
			Str("event", "agent_ready_panic").
			Str("command_id", commandID).
			Msg("clawkerd: handleAgentReady panicked; reporting IO_ERROR so CP sees a terminal Response")
		s.send(ctx, errResponse(commandID,
			clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR,
			fmt.Sprintf("agent_ready: panic: %v", r)))
	}()

	if s.spawnEntry == nil {
		s.log.Error().
			Str("event", "agent_ready_unwired").
			Str("command_id", commandID).
			Msg("clawkerd: AgentReady received before spawn entry was wired; container will not start the user CMD")
		s.send(ctx, errResponse(commandID,
			clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR,
			"agent_ready: spawn entry not wired"))
		return
	}

	// Settle the progress reporter with a closing banner BEFORE spawning.
	// SysProcAttr.Foreground=true transfers the controlling tty's
	// foreground pgroup to the child during fork — once spawn returns,
	// any further clawkerd write would visually clobber the user CMD's
	// startup output. Final is idempotent so a CP reconnect re-dispatch
	// (errAlreadySpawned path) cleanly no-ops.
	s.progress.Final()

	err := s.spawnEntry()
	if err != nil && !errors.Is(err, errAlreadySpawned) {
		s.log.Error().Err(err).
			Str("event", "agent_ready_spawn_failed").
			Str("command_id", commandID).
			Msg("clawkerd: AgentReady — spawn failed")
		s.send(ctx, errResponse(commandID,
			clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR,
			fmt.Sprintf("agent_ready: spawn: %v", err)))
		return
	}

	// Both happy-path (err == nil) and reconnect (errAlreadySpawned)
	// reply Done{0}. The audit event differs so operators can
	// distinguish first-boot from a CP reconnect re-dispatch.
	if err == nil {
		s.log.Info().
			Str("event", "agent_ready_spawned").
			Str("command_id", commandID).
			Msg("clawkerd: AgentReady — user CMD spawned")
	} else {
		s.log.Info().
			Str("event", "agent_ready_already_spawned").
			Str("command_id", commandID).
			Msg("clawkerd: AgentReady on reconnect — child already running")
	}
	s.send(ctx, &clawkerdv1.Response{
		CommandId: commandID,
		Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 0}},
	})
}

// runningCommand tracks one in-flight ShellCommand for routing
// follow-up Stdin / CloseStdin / Signal frames. The per-command ctx
// (derived from the Session ctx by startShellCommand) is plumbed as a
// first parameter through runShellCommand and the drainers — never
// stored on the struct, per the project ctx-handling rule.
//
// stdinMu guards stdin/stdinClosed/processes — all per-command IO
// state published from runShellCommand to routeStdin/routeCloseStdin/
// routeSignal. Callers MUST go through the methods (snapshotStdin /
// markStdinClosed / closeStdinOnce / snapshotProcesses / publishProcesses)
// rather than touching fields directly so the locking discipline is
// centralized.
type runningCommand struct {
	id     string
	cancel context.CancelFunc

	stdinMu     sync.Mutex
	stdin       io.WriteCloser // writer half of stage[0] stdin pipe
	stdinClosed bool

	// stdinReady gates routeStdin / routeCloseStdin until any
	// InitialStdin payload has been written. Without it, CP's natural
	// "ShellCommand+InitialStdin then CloseStdin" sequence races: the
	// close beats the write, the write returns ErrClosedPipe, and the
	// payload is silently lost. Closed exactly once by
	// runShellCommand's deferred Once closer (covers success,
	// SPAWN_FAILED, IO_ERROR, panic-recovery paths). routeStdin /
	// routeCloseStdin select on (stdinReady, ctx.Done) so a session
	// teardown that beats the close also unblocks them.
	stdinReady chan struct{}

	// processes holds each stage's *exec.Cmd. Index matches stage_index.
	// Published once via publishProcesses; routeSignal reads via
	// snapshotProcesses.
	processes []*exec.Cmd
}

// snapshotStdin returns the current stdin writer + closed flag under
// the publish lock. A (nil, _) return means stdin was never published.
func (rc *runningCommand) snapshotStdin() (io.WriteCloser, bool) {
	rc.stdinMu.Lock()
	defer rc.stdinMu.Unlock()
	return rc.stdin, rc.stdinClosed
}

// markStdinClosed sets the closed flag idempotently. Caller invokes
// after a stdin Write fails so subsequent Stdin frames take the
// "already closed" branch instead of re-attempting writes to a broken
// pipe and re-reporting IO_ERROR per frame.
func (rc *runningCommand) markStdinClosed() {
	rc.stdinMu.Lock()
	rc.stdinClosed = true
	rc.stdinMu.Unlock()
}

// closeStdinOnce closes stdin idempotently. Returns the underlying
// Close error with ErrClosedPipe filtered to nil (peer already closed
// is success). Caller decides logging discipline (Warn for
// CP-initiated CloseStdin; pipeCloseStats.record for pipeline
// teardown). Lock is held across Close to keep stdinClosed and the
// kernel-side close atomic against routeStdin/routeCloseStdin races.
func (rc *runningCommand) closeStdinOnce() error {
	rc.stdinMu.Lock()
	defer rc.stdinMu.Unlock()
	if rc.stdinClosed {
		return nil
	}
	rc.stdinClosed = true
	if rc.stdin == nil {
		return nil
	}
	if err := rc.stdin.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	return nil
}

// snapshotProcesses returns a copy of the per-stage *exec.Cmd slice.
// Caller must tolerate c.Process being nil or in the post-reap window
// (see routeSignal's ESRCH/ErrProcessDone filter).
func (rc *runningCommand) snapshotProcesses() []*exec.Cmd {
	rc.stdinMu.Lock()
	defer rc.stdinMu.Unlock()
	out := make([]*exec.Cmd, len(rc.processes))
	copy(out, rc.processes)
	return out
}

// publishProcesses sets the per-stage *exec.Cmd slice. Called once
// by runShellCommand after Start; subsequent reads via
// snapshotProcesses observe a settled state.
func (rc *runningCommand) publishProcesses(cmds []*exec.Cmd) {
	rc.stdinMu.Lock()
	rc.processes = cmds
	rc.stdinMu.Unlock()
}

// runSender drains sendCh into stream.Send. Exits when ctx is done.
// Errors from Send are logged and propagate via cancel() — calling
// cancel here is what unblocks producer goroutines parked on
// `sendCh <- resp` (their select races against `<-ctx.Done()`). The
// stream is unrecoverable past a Send error, so the session must end;
// without cancel, the receiver loop is the only path to teardown and
// it can lag arbitrarily on a half-broken transport.
func (s *session) runSender(ctx context.Context, cancel context.CancelFunc) {
	for {
		select {
		case <-ctx.Done():
			// Drain anything queued before ctx.Done fired so an
			// operator triaging "CP saw timeout instead of terminal"
			// sees exactly which command_id's Response landed in
			// sendCh but never made it to stream.Send. Producers
			// parked on `sendCh <- resp` unblock via their own
			// ctx.Done branch (see s.send) so this drain only sweeps
			// the buffer.
			for {
				select {
				case resp := <-s.sendCh:
					if resp == nil {
						continue
					}
					s.log.Warn().
						Str("event", "session_send_undelivered_on_teardown").
						Str("command_id", resp.CommandId).
						Str("payload_type", fmt.Sprintf("%T", resp.Payload)).
						Msg("clawkerd: response queued before teardown but never written to stream")
				default:
					return
				}
			}
		case resp := <-s.sendCh:
			if resp == nil {
				continue
			}
			if err := s.stream.Send(resp); err != nil {
				s.log.Error().Err(err).
					Str("event", "session_send_failed").
					Str("command_id", resp.CommandId).
					Msg("stream.Send failed; cancelling session ctx and abandoning sender")
				cancel()
				return
			}
			// Settle the user-facing init-step line only after the
			// terminal Response has shipped on the wire. Mirrors the
			// "starting" line emitted in dispatch. EndStep on a dropped
			// or unsent Response would leave the user with a "✓ done"
			// line for a step CP never saw the outcome of.
			s.settleInitStep(resp)
		}
	}
}

// settleInitStep emits the user-facing completion line for an init
// step's terminal Response after a successful stream.Send. Non-init
// CommandIDs (parseInitStep returns false) and non-terminal payloads
// are no-ops.
func (s *session) settleInitStep(resp *clawkerdv1.Response) {
	if s.progress == nil || resp == nil {
		return
	}
	label, ok := parseInitStep(resp.CommandId)
	if !ok {
		return
	}
	switch resp.Payload.(type) {
	case *clawkerdv1.Response_Done:
		s.progress.EndStep(label, true)
	case *clawkerdv1.Response_Error:
		s.progress.EndStep(label, false)
	}
}

// send pushes a Response onto sendCh. Drops on ctx-done so producer
// goroutines unblock when the stream is tearing down. Terminal
// payloads (Done / Error / RegisterDone) are dropped at Warn — when
// CP doesn't see the outcome it falls back to its own timeout, and
// operators triaging "RegisterDone timeout" or "step timeout" upstream
// need a breadcrumb here to distinguish "clawkerd never produced a
// Response" from "clawkerd produced one but the stream died before it
// shipped". Non-terminal chunks (Started / Stdout / Stderr / StageExit)
// drop at Debug — losing one is at worst a gap in streaming output and
// CP doesn't gate any control-flow decision on a specific chunk.
func (s *session) send(ctx context.Context, resp *clawkerdv1.Response) {
	select {
	case s.sendCh <- resp:
	case <-ctx.Done():
		var (
			event     *zerolog.Event
			eventName string
			msg       string
		)
		switch classifyDropPayload(resp) {
		case payloadClassChunk:
			event = s.log.Debug()
			eventName = "session_send_dropped_chunk"
			msg = "clawkerd: dropping non-terminal Response on Session teardown"
		case payloadClassTerminal:
			event = s.log.Warn()
			eventName = "session_send_dropped_terminal"
			msg = "clawkerd: dropping terminal Response on Session teardown — CP will see its own timeout instead of the true outcome"
		default:
			// Wire-vocabulary drift: a payload variant was added to
			// clawkerd.proto without updating classifyDropPayload.
			// Loud-fail at Warn so operators see the breadcrumb rather
			// than silently downgrading to Debug under the chunk arm.
			event = s.log.Warn()
			eventName = "session_send_dropped_unknown"
			msg = "clawkerd: dropping Response of unclassified payload type on Session teardown — classifyDropPayload missing a switch arm"
		}
		event.
			Str("event", eventName).
			Str("command_id", resp.CommandId).
			Str("payload_type", fmt.Sprintf("%T", resp.Payload)).
			Msg(msg)
	}
}

// payloadClass is the drop-time classification of a Response payload:
// terminal verdicts loss-blocks CP on its command_id (Warn); streaming
// chunks lose at most a fragment of progress output (Debug); unknown
// is drift (Warn, distinct event).
type payloadClass int

const (
	payloadClassUnknown payloadClass = iota
	payloadClassChunk
	payloadClassTerminal
)

// classifyDropPayload returns the drop-time class of resp. The switch
// is intentionally exhaustive over current proto variants — a new
// variant added to clawkerd.proto without a matching arm here falls
// to payloadClassUnknown, which send() logs at Warn with a distinct
// event so operators see the breadcrumb rather than silently
// downgrading to chunk-Debug.
func classifyDropPayload(resp *clawkerdv1.Response) payloadClass {
	if resp == nil {
		return payloadClassUnknown
	}
	switch resp.Payload.(type) {
	case *clawkerdv1.Response_Done, *clawkerdv1.Response_Error, *clawkerdv1.Response_RegisterDone:
		return payloadClassTerminal
	case *clawkerdv1.Response_Started, *clawkerdv1.Response_Stdout, *clawkerdv1.Response_Stderr, *clawkerdv1.Response_StageExit:
		return payloadClassChunk
	}
	return payloadClassUnknown
}

// runReceiver loops on stream.Recv and dispatches each Command.
// Returns nil on graceful client close (io.EOF), the underlying error
// otherwise (excluding context.Canceled which is treated as nil).
func (s *session) runReceiver(ctx context.Context) error {
	for {
		cmd, err := s.stream.Recv()
		if errors.Is(err, io.EOF) {
			s.log.Info().Str("event", "session_eof").Msg("CP closed Session stream")
			return nil
		}
		if err != nil {
			// ctx-canceled is our own teardown (Session ending). Log
			// the cause at Info — operators get an audit trail without
			// elevating teardown noise to Error. Surface the error so
			// gRPC closes the call cleanly with context.Canceled rather
			// than success.
			if ctx.Err() != nil {
				s.log.Info().Err(err).Str("event", "session_recv_teardown").Msg("stream.Recv ended during ctx-cancel teardown")
				return err
			}
			s.log.Error().Err(err).Str("event", "session_recv_failed").Msg("stream.Recv failed")
			return err
		}
		s.dispatch(ctx, cmd)
	}
}

// dispatch routes one Command to the right handler. command_id is
// the dup-detection / routing key for everything except Hello, so
// reject empty up front for those payload types — the wire-level
// proto3 string field has no non-empty invariant of its own. Hello
// is a stateless echo with no dup tracking; allow empty there to
// preserve compatibility.
func (s *session) dispatch(ctx context.Context, cmd *clawkerdv1.Command) {
	switch p := cmd.Payload.(type) {
	case *clawkerdv1.Command_Hello:
		s.send(ctx, &clawkerdv1.Response{
			CommandId: cmd.CommandId,
			Payload:   &clawkerdv1.Response_HelloAck{HelloAck: &clawkerdv1.HelloAck{}},
		})
	case *clawkerdv1.Command_Shell:
		if cmd.CommandId == "" {
			s.send(ctx, errResponse("",
				clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
				"command_id required"))
			return
		}
		// CP-driven init steps carry an `init-` prefixed CommandID;
		// emit the "in progress" status line off the boundary. Done/Error
		// completion is fired in runSender via settleInitStep, only after
		// stream.Send succeeds — so a step's "✓ done" line is never emitted
		// for a Response CP didn't actually receive.
		if label, ok := parseInitStep(cmd.CommandId); ok {
			s.progress.StartStep(label)
		}
		s.startShellCommand(ctx, cmd.CommandId, p.Shell)
	case *clawkerdv1.Command_Stdin:
		if cmd.CommandId == "" {
			s.send(ctx, errResponse("",
				clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
				"command_id required"))
			return
		}
		s.routeStdin(ctx, cmd.CommandId, p.Stdin)
	case *clawkerdv1.Command_CloseStdin:
		if cmd.CommandId == "" {
			s.send(ctx, errResponse("",
				clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
				"command_id required"))
			return
		}
		s.routeCloseStdin(ctx, cmd.CommandId)
	case *clawkerdv1.Command_Signal:
		if cmd.CommandId == "" {
			s.send(ctx, errResponse("",
				clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
				"command_id required"))
			return
		}
		s.routeSignal(ctx, cmd.CommandId, p.Signal)
	case *clawkerdv1.Command_RegisterRequired:
		if cmd.CommandId == "" {
			s.send(ctx, errResponse("",
				clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
				"command_id required"))
			return
		}
		s.handleRegisterRequired(ctx, cmd.CommandId)
	case *clawkerdv1.Command_AgentReady:
		if cmd.CommandId == "" {
			s.send(ctx, errResponse("",
				clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
				"command_id required"))
			return
		}
		s.handleAgentReady(ctx, cmd.CommandId)
	default:
		// Unknown payload is the canonical CP/clawkerd version-skew
		// signal — the proto added a Command variant that this clawkerd
		// build doesn't know how to handle. Audit log per package
		// CLAUDE.md: every command-dispatch outcome must be observable.
		s.log.Warn().
			Str("event", "session_unknown_payload").
			Str("command_id", cmd.CommandId).
			Str("payload_type", fmt.Sprintf("%T", cmd.Payload)).
			Msg("clawkerd: dispatch received unknown Command payload type — version skew?")
		s.send(ctx, errResponse(cmd.CommandId,
			clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
			fmt.Sprintf("unknown payload type %T", cmd.Payload)))
	}
}

// startShellCommand validates the request, spawns the pipeline, and
// registers the runningCommand for routing follow-up frames. Caller
// (dispatch) guarantees id is non-empty.
func (s *session) startShellCommand(ctx context.Context, id string, sc *clawkerdv1.ShellCommand) {
	if len(sc.Stages) == 0 {
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
			"shell: stages must not be empty"))
		return
	}
	for i, st := range sc.Stages {
		if len(st.Argv) == 0 {
			s.send(ctx, errResponse(id,
				clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
				fmt.Sprintf("shell: stage[%d] argv must not be empty", i)))
			return
		}
	}

	// Check for duplicate command_id while holding the mu — if
	// another ShellCommand with the same id is still running, this
	// is INVALID_REQUEST. CP is expected to issue unique IDs.
	s.mu.Lock()
	if _, exists := s.cmds[id]; exists {
		s.mu.Unlock()
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
			"shell: command_id already in use"))
		return
	}

	cmdCtx, cmdCancel := context.WithCancel(ctx)

	// Ordering invariant: stdinW must be allocated AND registered
	// under s.mu BEFORE runShellCommand is spawned. routeCloseStdin /
	// routeStdin / routeSignal can fire on the receiver goroutine the
	// instant the next Command arrives — well before the worker is
	// scheduled. See runningCommand.stdinReady for the race the gate
	// (combined with this ordering) prevents.
	stdinR, stdinW := io.Pipe()
	rc := &runningCommand{
		id:         id,
		cancel:     cmdCancel,
		stdin:      stdinW,
		stdinReady: make(chan struct{}),
	}
	s.cmds[id] = rc
	s.mu.Unlock()

	go s.runShellCommand(cmdCtx, rc, sc, stdinR)
}

// runShellCommand is the per-command worker. Lifetime: spawn → reap.
// Sends Started/Stdout/Stderr/StageExit/Done/Error responses through
// s.sendCh. Removes itself from s.cmds on exit.
//
// Audit logging: clawkerd runs as root inside the container and
// ShellCommand can dispatch arbitrary argv with arbitrary uid/gid. The
// CN-pinned mTLS listener is the only trust boundary today (CP is the
// sole authorized caller); per-command argv allow-listing is a v2
// concern. Until then, every command emits a structured
// `shell_command_started` event per stage at Info with full argv +
// cwd + uid/gid, and a `shell_command_done` event with duration +
// outcome at Info on terminal exit. Operators forwarding clawkerd's
// log to durable storage get a complete audit trail.
func (s *session) runShellCommand(ctx context.Context, rc *runningCommand, sc *clawkerdv1.ShellCommand, stdinR *io.PipeReader) {
	// PID-1 resilience: a panic anywhere in the worker outside the
	// per-stage reapers (e.g. exec.CommandContext nil-deref, time.AfterFunc
	// callback, unexpected pipe-close path) would otherwise kill clawkerd
	// and the container with no diagnostic surface. Recover at the top
	// level — declared first so it runs LAST in LIFO defer order, after
	// the audit-log defer below has emitted shell_command_done with the
	// (probably "incomplete") outcome. onPanic cancels the per-command
	// ctx so any started exec.CommandContext stages get SIGKILL'd.
	recoverLog := s.log.With("command_id", rc.id)
	defer recoverGoroutine(recoverLog, "shell_command_worker", rc.cancel)
	startedAt := time.Now()
	var (
		auditFinalExit int32 = -1
		auditTimedOut  bool
		auditOutcome   string = "incomplete"
	)
	for i, st := range sc.Stages {
		s.log.Info().
			Str("event", "shell_command_started").
			Str("command_id", rc.id).
			Int("stage_index", i).
			Strs("argv", st.Argv).
			Str("cwd", st.Cwd).
			Uint32("uid", st.Uid).
			Uint32("gid", st.Gid).
			Uint32("timeout_seconds", sc.TimeoutSeconds).
			Msg("clawkerd: shell command stage started")
	}
	defer func() {
		rc.cancel()
		s.mu.Lock()
		delete(s.cmds, rc.id)
		s.mu.Unlock()
		s.log.Info().
			Str("event", "shell_command_done").
			Str("command_id", rc.id).
			Dur("duration", time.Since(startedAt)).
			Int32("final_exit_code", auditFinalExit).
			Bool("timed_out", auditTimedOut).
			Str("outcome", auditOutcome).
			Msg("clawkerd: shell command done")
	}()

	// Defer guarantees stdinReady fires on every return path
	// (success, SPAWN_FAILED, panic recovery). See
	// runningCommand.stdinReady for the race contract.
	var stdinReadyOnce sync.Once
	closeStdinReady := func() { stdinReadyOnce.Do(func() { close(rc.stdinReady) }) }
	defer closeStdinReady()

	// Build each stage's *exec.Cmd. Use CommandContext so a ctx
	// cancel (timeout, Session teardown) sends SIGKILL automatically.
	cmds := make([]*exec.Cmd, len(sc.Stages))
	for i, st := range sc.Stages {
		c := exec.CommandContext(ctx, st.Argv[0], st.Argv[1:]...)
		c.Dir = st.Cwd
		c.Env = buildEnv(st.Env)
		// Per-stage credential: drop privileges if uid/gid set. Zero
		// means "inherit from clawkerd" — clawkerd is PID 1 of the
		// agent container and stays root for the supervisor's
		// lifetime (privilege drop happens in the spawn child via
		// SysProcAttr.Credential, not in clawkerd's own process).
		if st.Uid != 0 || st.Gid != 0 {
			c.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{Uid: st.Uid, Gid: st.Gid},
			}
		}
		cmds[i] = c
	}

	// stdinR was allocated by startShellCommand before this goroutine
	// spawned, and rc.stdin (== stdinW) was registered under the
	// session lock at the same time so routeCloseStdin / routeStdin
	// races are race-free. Here we just plumb stdinR into stage[0]
	// and publish processes so routeSignal observers see a settled
	// state.
	cmds[0].Stdin = stdinR
	stdinW, _ := rc.snapshotStdin()
	rc.publishProcesses(cmds)

	// closeStats accumulates close-error accounting across every
	// closePipeOnce call site in this goroutine: first failure lands
	// as a Warn at the call site, subsequent ones increment
	// Suppressed. A summary Warn is flushed via the deferred audit
	// emitter below if Suppressed > 0 so a torrent of close failures
	// (e.g. real FD leaks during pipeline teardown) cannot vanish
	// silently.
	var closeStats pipeCloseStats
	defer func() {
		if closeStats.suppressed > 0 {
			s.log.Warn().
				Str("event", "session_pipe_close_failed_suppressed").
				Str("command_id", rc.id).
				Int("suppressed", closeStats.suppressed).
				Msg("clawkerd: additional pipe close failures suppressed during pipeline teardown")
		}
	}()

	// stdout/stderr pipes are owned via os.Pipe rather than
	// cmd.StdoutPipe()/StderrPipe(). cmd.Wait() closes the read end of
	// every stdlib-created pipe (it tracks them in parentIOPipes); our
	// stage reapers call c.Wait() concurrently with the drain goroutines,
	// so a stdlib pipe lets Wait close the read end mid-drain and
	// silently DISCARD buffered output a fast-exit child already wrote —
	// the bug behind empty `ssh` known_hosts on init. A user-supplied
	// *os.File is never tracked by exec (writerDescriptor/readerDescriptor
	// return the *os.File without appending to childIOFiles/parentIOPipes,
	// see os/exec/exec.go), so neither Start nor Wait ever touches these.
	// We close the write ends right after Start — leaving the child as the
	// sole writer so the reader EOFs exactly on child exit — and the read
	// ends at teardown once the drainers have returned.
	//
	// createdPipes tracks every fd until the pipeline is live so a spawn
	// failure (this loop or the Start loop below) closes them all; it is
	// disarmed once Start succeeds, after which the drain/teardown path
	// owns the read ends and the write ends are already closed.
	var createdPipes []*os.File
	defer func() {
		for _, f := range createdPipes {
			_ = f.Close()
		}
	}()
	newPipe := func(label string) (r, w *os.File, ok bool) {
		pr, pw, perr := os.Pipe()
		if perr != nil {
			s.send(ctx, errResponse(rc.id,
				clawkerdv1.ErrorCode_ERROR_CODE_SPAWN_FAILED,
				fmt.Sprintf("%s pipe: %v", label, perr)))
			s.closePipeOnce(rc.id, "stdin", stdinW, &closeStats)
			auditOutcome = "spawn_failed"
			return nil, nil, false
		}
		createdPipes = append(createdPipes, pr, pw)
		return pr, pw, true
	}

	// Chain stage[i].stdout → stage[i+1].stdin; capture the final
	// stage's stdout read end for streaming. writeEnds collects the
	// parent's copy of every stdout/stderr write end so the Start loop
	// can close them once each child owns its own dup.
	var writeEnds []*os.File
	stagePipes := make([]io.ReadCloser, len(cmds)-1)
	for i := 0; i < len(cmds)-1; i++ {
		pr, pw, ok := newPipe(fmt.Sprintf("stage[%d] stdout", i))
		if !ok {
			return
		}
		cmds[i].Stdout = pw
		cmds[i+1].Stdin = pr
		stagePipes[i] = pr
		writeEnds = append(writeEnds, pw)
	}

	finalRead, finalWrite, ok := newPipe("final stage stdout")
	if !ok {
		return
	}
	cmds[len(cmds)-1].Stdout = finalWrite
	finalStdout := io.ReadCloser(finalRead)
	writeEnds = append(writeEnds, finalWrite)

	// Per-stage stderr pipes for streaming StderrChunk.
	stderrPipes := make([]io.ReadCloser, len(cmds))
	for i := range cmds {
		pr, pw, ok := newPipe(fmt.Sprintf("stage[%d] stderr", i))
		if !ok {
			return
		}
		cmds[i].Stderr = pw
		stderrPipes[i] = pr
		writeEnds = append(writeEnds, pw)
	}

	// Start each stage. Failure mid-way kills already-started
	// stages by ctx cancel.
	for i, c := range cmds {
		if startErr := c.Start(); startErr != nil {
			// Send the error response BEFORE cancelling the per-command
			// ctx — s.send select-races against ctx.Done, and a
			// cancelled ctx means the SPAWN_FAILED response can drop
			// in favor of the ctx.Done branch.
			s.send(ctx, errResponse(rc.id,
				clawkerdv1.ErrorCode_ERROR_CODE_SPAWN_FAILED,
				fmt.Sprintf("stage[%d] start: %v", i, startErr)))
			rc.cancel() // kills any started stages via CommandContext
			s.closePipeOnce(rc.id, "stdin", stdinW, &closeStats)
			auditOutcome = "spawn_failed"
			return
		}
	}

	// All stages running. Close the parent's copy of every stdout/stderr
	// write end so each child is the sole writer — otherwise the read
	// ends never EOF (the parent fd keeps the pipe open) and the drainers
	// block forever. With the write ends closed and the pipeline live,
	// the read ends are now owned by the drain/teardown path, so disarm
	// the spawn-failure cleanup. See the os.Pipe ownership note above.
	for _, w := range writeEnds {
		s.closePipeOnce(rc.id, "child_write_end", w, &closeStats)
	}
	createdPipes = nil

	// All stages running. Tell CP.
	s.send(ctx, &clawkerdv1.Response{
		CommandId: rc.id,
		Payload:   &clawkerdv1.Response_Started{Started: &clawkerdv1.Started{}},
	})

	// Optional timeout watchdog. On fire: SIGKILL via ctx cancel
	// then surface ERROR_CODE_TIMEOUT after reaping.
	var timedOut atomic.Bool
	if sc.TimeoutSeconds > 0 {
		t := time.AfterFunc(time.Duration(sc.TimeoutSeconds)*time.Second, func() {
			timedOut.Store(true)
			rc.cancel()
		})
		defer t.Stop()
	}

	// initial_stdin runs in a goroutine so a large payload doesn't
	// block this goroutine before the output drainers start (filters
	// like grep can deadlock on full pipe buffers otherwise).
	if len(sc.InitialStdin) > 0 {
		go func() {
			// LIFO: recoverGoroutine fires FIRST on panic so peers
			// waiting on stdinReady do not deadlock; closeStdinReady
			// then fires unconditionally.
			defer closeStdinReady()
			defer recoverGoroutine(s.log.With("command_id", rc.id), "initial_stdin_writer", nil)
			w, closed := rc.snapshotStdin()
			if w == nil || closed {
				return
			}
			if _, werr := w.Write(sc.InitialStdin); werr != nil && !isStdinPeerClosed(werr) {
				s.log.Error().Err(werr).
					Str("event", "session_initial_stdin_write_failed").
					Str("command_id", rc.id).
					Msg("write initial_stdin")
				// Surface to CP so it can distinguish "command ran
				// against the requested input" from "command ran
				// against truncated input". Without this, a write
				// failure shows up only as a (possibly success) Done
				// with no clue stdin was incomplete — silent semantic
				// divergence between CP intent and clawkerd execution.
				s.send(ctx, errResponse(rc.id,
					clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR,
					fmt.Sprintf("initial_stdin write failed: %v", werr)))
			}
		}()
	} else {
		closeStdinReady()
	}

	// Streaming drainers + reapers. Use a WaitGroup so we can emit
	// Done/Error only after every stream has been drained and every
	// stage reaped.
	var wg sync.WaitGroup

	// Stderr drainers — one per stage, tagged by stage_index.
	for i := range cmds {

		wg.Add(1)
		go func() {
			defer wg.Done()
			// PID-1 resilience: defer-LIFO means recover runs first
			// on panic, then wg.Done — wg.Wait won't deadlock.
			defer recoverGoroutine(s.log.With("command_id", rc.id, "stage_index", i), "drain_stderr", nil)
			s.drainStderr(ctx, rc, uint32(i), stderrPipes[i])
		}()
	}

	// Stdout drainer — final stage only.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer recoverGoroutine(s.log.With("command_id", rc.id), "drain_stdout", nil)
		s.drainStdout(ctx, rc, finalStdout)
	}()

	// Stage reapers — emit StageExit for each as it finishes. Using
	// a separate goroutine per stage means CP sees each StageExit
	// promptly (no head-of-line blocking on a slow earlier stage).
	//
	// The final-stage Wait error feeds Done.final_exit_code via
	// exitCodeOf. Earlier-stage errors are not propagated (the
	// pipeline's final exit code is the only thing CP cares about).
	// A buffered channel (cap 1) carries the final-stage err out of
	// its reaper goroutine without a shared slice — `go test -race`
	// flags the previous shared-slice write/read pattern even though
	// reapWG.Wait happens-before the read.
	finalStageErrCh := make(chan error, 1)
	finalIdx := len(cmds) - 1
	var reapWG sync.WaitGroup
	for i, c := range cmds {
		isFinal := i == finalIdx
		reapWG.Add(1)
		wg.Add(1)
		go func() {
			defer reapWG.Done()
			defer wg.Done()
			// A panic here without recover would crash clawkerd
			// AND leave the worker deadlocked at <-finalStageErrCh
			// (line below the reapWG.Wait), because the channel
			// would never receive. Recover ensures: (1) clawkerd
			// stays up, (2) the final-stage channel always gets a
			// value so the worker unblocks, (3) CP sees a synthetic
			// StageExit so the stage axis transitions out of
			// running.
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				panicErr := fmt.Errorf("stage reaper panicked: %v", r)
				s.log.Error().
					Interface("panic", r).
					Bytes("stack", debug.Stack()).
					Str("event", "shell_stage_reaper_panic").
					Str("command_id", rc.id).
					Int("stage_index", i).
					Bool("is_final", isFinal).
					Msg("clawkerd: stage reaper panicked; sending sentinel + synthetic StageExit so worker unblocks and CP sees terminal")
				if isFinal {
					// Buffered cap 1 — non-blocking. Default
					// branch guards the panic-after-send window
					// (Wait completed, channel sent, then s.send
					// or stageExitResponse panicked): the channel
					// is already filled and the worker will
					// proceed; sentinel is redundant.
					select {
					case finalStageErrCh <- panicErr:
					default:
					}
				}
				s.send(ctx, s.stageExitResponse(rc.id, uint32(i), c, panicErr))
			}()
			waitErr := c.Wait()
			if hook := stageReaperPanicHookForTest; hook != nil {
				hook(i, isFinal)
			}
			if isFinal {
				finalStageErrCh <- waitErr
			}
			s.send(ctx, s.stageExitResponse(rc.id, uint32(i), c, waitErr))
		}()
	}

	// Block until every reaper finishes so the stdin writer can be
	// safely closed (no further data flow possible) and downstream
	// pipes drain. closeStdinOnce is idempotent — handles the
	// already-closed case if routeCloseStdin won the race.
	reapWG.Wait()
	if err := rc.closeStdinOnce(); err != nil {
		closeStats.record(s.log, rc.id, "stdin", err)
	}
	for i, p := range stagePipes {
		s.closePipeOnce(rc.id, fmt.Sprintf("stage[%d]_stdout", i), p, &closeStats)
	}

	// Wait for stdout/stderr drainers to finish so chunks can't
	// arrive after Done.
	wg.Wait()

	// Drainers have returned, so close the read ends we own (os.Pipe,
	// not stdlib StdoutPipe/StderrPipe — Wait never closed them). Closing
	// before wg.Wait would race a drainer mid-Read; the stdlib's
	// close-on-Wait of exactly these read ends mid-drain was the original
	// truncated-output bug.
	s.closePipeOnce(rc.id, "final_stdout", finalStdout, &closeStats)
	for i, p := range stderrPipes {
		s.closePipeOnce(rc.id, fmt.Sprintf("stage[%d]_stderr", i), p, &closeStats)
	}

	if timedOut.Load() {
		s.send(ctx, errResponse(rc.id,
			clawkerdv1.ErrorCode_ERROR_CODE_TIMEOUT,
			fmt.Sprintf("pipeline killed after %ds timeout", sc.TimeoutSeconds)))
		auditTimedOut = true
		auditOutcome = "timeout"
		return
	}
	finalExit := exitCodeOf(cmds[finalIdx], <-finalStageErrCh)
	s.send(ctx, &clawkerdv1.Response{
		CommandId: rc.id,
		Payload: &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{
			FinalExitCode: finalExit,
		}},
	})
	auditFinalExit = finalExit
	auditOutcome = "completed"
}

// isExpectedDrainEnd reports whether err signals an orderly end of
// the drain loop rather than a real I/O fault. Three flavors all mean
// "the pipe closed normally":
//
//   - io.EOF: peer wrote-and-closed.
//   - io.ErrClosedPipe: in-process io.Pipe peer closed.
//   - os.ErrClosed (wrapped in *fs.PathError): exec.Cmd.Wait closes
//     stdout/stderr pipes after seeing the command exit (per stdlib
//     docs). For fast-exit commands the reaper can win the race
//     against an in-flight Read on the drain side; the stdlib then
//     returns "read |0: file already closed". Without this filter, CP
//     sees ERROR_CODE_IO_ERROR even though Wait completed cleanly.
func isExpectedDrainEnd(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, os.ErrClosed)
}

// isStdinPeerClosed reports whether a stdin Write error signals the
// child closed its read side before the writer finished. Both
// ErrClosedPipe (in-process io.Pipe) and EPIPE (kernel pipe; what
// fast-exit children produce, e.g. `printf … | head -c1`) mean the
// same thing: the child got the bytes it cared about and exited.
// Surfacing IO_ERROR for either would tell CP "stdin truncated"
// when the command actually completed normally.
func isStdinPeerClosed(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EPIPE)
}

// drainStdout reads chunks from the final stage's stdout pipe and
// emits StdoutChunk responses until EOF or read error. Read errors
// other than the orderly-close set are surfaced as IO_ERROR but do
// not kill the pipeline — the reaper still emits Done/StageExit.
func (s *session) drainStdout(ctx context.Context, rc *runningCommand, r io.ReadCloser) {
	buf := make([]byte, chunkBufSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			s.send(ctx, &clawkerdv1.Response{
				CommandId: rc.id,
				Payload:   &clawkerdv1.Response_Stdout{Stdout: &clawkerdv1.StdoutChunk{Data: data}},
			})
		}
		if err == nil {
			continue
		}
		if isExpectedDrainEnd(err) {
			return
		}
		s.send(ctx, errResponse(rc.id,
			clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR,
			fmt.Sprintf("stdout drain: %v", err)))
		return
	}
}

// drainStderr is the per-stage analog of drainStdout.
func (s *session) drainStderr(ctx context.Context, rc *runningCommand, stageIndex uint32, r io.ReadCloser) {
	buf := make([]byte, chunkBufSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			s.send(ctx, &clawkerdv1.Response{
				CommandId: rc.id,
				Payload: &clawkerdv1.Response_Stderr{Stderr: &clawkerdv1.StderrChunk{
					StageIndex: stageIndex,
					Data:       data,
				}},
			})
		}
		if err == nil {
			continue
		}
		if isExpectedDrainEnd(err) {
			return
		}
		s.send(ctx, errResponse(rc.id,
			clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR,
			fmt.Sprintf("stderr drain stage[%d]: %v", stageIndex, err)))
		return
	}
}

// waitStdinReady blocks until either the per-command stdinReady gate
// closes (InitialStdin write completed or there was none) or the
// session ctx cancels. Returns true on the gate-closed path; false if
// ctx cancelled first (caller should bail without touching stdin —
// session is tearing down). See runningCommand.stdinReady for the
// race this gate prevents.
func waitStdinReady(ctx context.Context, rc *runningCommand) bool {
	select {
	case <-rc.stdinReady:
		return true
	case <-ctx.Done():
		return false
	}
}

// routeStdin writes a Stdin frame's bytes into the target command's
// stage[0] stdin. UNKNOWN_COMMAND_ID if no such command is running.
// Blocks on rc.stdinReady (see runningCommand.stdinReady).
func (s *session) routeStdin(ctx context.Context, id string, st *clawkerdv1.Stdin) {
	rc := s.lookup(id)
	if rc == nil {
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_UNKNOWN_COMMAND_ID,
			"stdin: no running command with that id"))
		return
	}
	if !waitStdinReady(ctx, rc) {
		return
	}
	w, closed := rc.snapshotStdin()
	if closed || w == nil {
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
			"stdin: already closed"))
		return
	}
	if _, err := w.Write(st.Data); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		// Mark stdinClosed so subsequent Stdin frames take the cleaner
		// "already closed" branch above instead of re-attempting writes
		// to a broken pipe and re-reporting IO_ERROR per frame.
		rc.markStdinClosed()
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR,
			fmt.Sprintf("stdin write: %v", err)))
	}
}

// routeCloseStdin closes stage[0]'s stdin pipe so a stdin-reading
// command sees EOF. Idempotent. Blocks on rc.stdinReady (see
// runningCommand.stdinReady).
func (s *session) routeCloseStdin(ctx context.Context, id string) {
	rc := s.lookup(id)
	if rc == nil {
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_UNKNOWN_COMMAND_ID,
			"close_stdin: no running command with that id"))
		return
	}
	if !waitStdinReady(ctx, rc) {
		return
	}
	// CP-driven explicit close. Surface real Close errors via the
	// audit log so an EBADF / EIO doesn't vanish silently — the
	// CP receives no Error response either way (CloseStdin has no
	// Response payload), so the log is the only signal.
	if err := rc.closeStdinOnce(); err != nil {
		s.log.Warn().Err(err).
			Str("event", "session_close_stdin_failed").
			Str("command_id", id).
			Msg("clawkerd: explicit CloseStdin returned error")
	}
}

// routeSignal forwards a POSIX signal to every stage's process.
// Forwarding to all stages mirrors `kill -INT` behavior in a shell
// pipeline (signals propagate to each pipeline stage's process).
func (s *session) routeSignal(ctx context.Context, id string, sig *clawkerdv1.Signal) {
	if sig == nil || sig.Signo <= 0 {
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
			"signal: signo must be a positive integer"))
		return
	}
	rc := s.lookup(id)
	if rc == nil {
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_UNKNOWN_COMMAND_ID,
			"signal: no running command with that id"))
		return
	}
	// Snapshot processes under the publish lock so we don't race the
	// runShellCommand goroutine writing the slice header.
	processes := rc.snapshotProcesses()
	for i, c := range processes {
		if c == nil || c.Process == nil {
			continue
		}
		err := c.Process.Signal(syscall.Signal(sig.Signo))
		if err == nil {
			continue
		}
		// ESRCH and os.ErrProcessDone are race-with-reaper artifacts:
		// the stage exited between rc.processes capture and our
		// Signal call. Log at Debug, never elevate to Error — modern
		// Go (1.17+) returns os.ErrProcessDone on the Signal-after-
		// reap race, and missing this filter floods Error logs every
		// time SIGTERM hits a near-exited stage.
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			s.log.Debug().
				Str("event", "session_signal_after_exit").
				Str("command_id", id).
				Int("stage_index", i).
				Int32("signo", sig.Signo).
				Msg("signal forwarded to already-exited stage")
			continue
		}
		s.log.Error().Err(err).
			Str("event", "session_signal_forward_failed").
			Str("command_id", id).
			Int("stage_index", i).
			Int32("signo", sig.Signo).
			Msg("forward signal to stage")
	}
}

// lookup returns the runningCommand for id, or nil if not running.
func (s *session) lookup(id string) *runningCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmds[id]
}

// shutdownRunning cancels every in-flight command. Used on Session
// teardown — exec.CommandContext will SIGKILL each stage.
func (s *session) shutdownRunning() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rc := range s.cmds {
		rc.cancel()
	}
}

// buildEnv composes the env slice for one stage. Empty map →
// inherit clawkerd's environ (the CLI seeded clawkerd's env at
// container create time with vars CP doesn't know about — PATH,
// CLAWKER_AGENT, project-specific config — and most stages want
// those). Non-empty map → inherit AS A BASE and append the explicit
// entries on top; later entries in exec.Cmd.Env shadow earlier
// duplicates, so explicit values from CP override the inherited
// defaults.
func buildEnv(m map[string]string) []string {
	if len(m) == 0 {
		// nil tells exec.Cmd to use the current process's environ.
		return nil
	}
	base := os.Environ()
	out := make([]string, 0, len(base)+len(m))
	out = append(out, base...)
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// stageExitResponse builds the StageExit Response for one reaped
// stage. waitErr is the error returned by cmd.Wait(); for normal
// exit (non-zero or zero) it's *exec.ExitError. For signaled exit
// the ExitError carries WaitStatus with Signaled() true; we extract
// signo and report exit_code = -1 to match POSIX convention.
func (s *session) stageExitResponse(id string, stageIndex uint32, c *exec.Cmd, waitErr error) *clawkerdv1.Response {
	exitCode := int32(0)
	signo := int32(0)

	if c.ProcessState != nil {
		if ws, ok := c.ProcessState.Sys().(syscall.WaitStatus); ok {
			switch {
			case ws.Signaled():
				signo = int32(ws.Signal())
				exitCode = -1
			case ws.Exited():
				exitCode = int32(ws.ExitStatus())
			default:
				exitCode = int32(c.ProcessState.ExitCode())
			}
		} else {
			// Non-WaitStatus Sys() means a future GOOS port (Windows)
			// or a synthetic exec.Cmd test seam. Log at Debug so the
			// regression surfaces if/when it happens — without this,
			// the signaled-vs-exited distinction is silently lost
			// and CP sees signo=0 for every signaled child.
			s.log.Debug().
				Str("event", "shell_stage_exit_unexpected_sys_type").
				Str("sys_type", fmt.Sprintf("%T", c.ProcessState.Sys())).
				Msg("clawkerd: ProcessState.Sys() is not a syscall.WaitStatus; signal info lost")
			exitCode = int32(c.ProcessState.ExitCode())
		}
	} else if waitErr != nil {
		// Process never started successfully; treat as -1 with no
		// signal. The SPAWN_FAILED Error path normally catches this
		// before we ever reach reaping, but defend against partial
		// pipeline starts.
		exitCode = -1
	}

	return &clawkerdv1.Response{
		CommandId: id,
		Payload: &clawkerdv1.Response_StageExit{StageExit: &clawkerdv1.StageExit{
			StageIndex: stageIndex,
			ExitCode:   exitCode,
			Signo:      signo,
		}},
	}
}

// exitCodeOf returns the int32 exit code for the final stage, used
// to populate Done.final_exit_code. Mirrors the StageExit logic but
// returns just the int32.
func exitCodeOf(c *exec.Cmd, waitErr error) int32 {
	if c.ProcessState == nil {
		if waitErr != nil {
			return -1
		}
		return 0
	}
	if ws, ok := c.ProcessState.Sys().(syscall.WaitStatus); ok {
		if ws.Signaled() {
			return -1
		}
		return int32(ws.ExitStatus())
	}
	return int32(c.ProcessState.ExitCode())
}

// pipeCloseStats accumulates per-runShellCommand close-error
// accounting. The first non-success Close lands as a Warn at its call
// site so operators see the failure shape at the moment it surfaces;
// subsequent failures (typical during pipeline teardown when the
// kernel returns the same EBADF / EIO across every fd in the chain)
// increment Suppressed instead, and runShellCommand emits a single
// summary line on exit if Suppressed > 0. The earlier *bool-only
// dedupe silently swallowed N-1 close failures with no surviving
// signal — a regression that ate every close error past the first
// (e.g. a real FD leak across the chain) would have been invisible.
type pipeCloseStats struct {
	logged     bool
	suppressed int
}

// record dedupes a close error onto stats. First non-success failure
// logs at Warn; subsequent ones increment suppressed so a torrent of
// close errors during pipeline teardown produces exactly one Warn +
// a summary count flushed by the caller. io.ErrClosedPipe is success
// (peer already closed).
func (stats *pipeCloseStats) record(log *logger.Logger, cmdID, name string, err error) {
	if err == nil || errors.Is(err, io.ErrClosedPipe) {
		return
	}
	if stats.logged {
		stats.suppressed++
		return
	}
	log.Warn().Err(err).
		Str("event", "session_pipe_close_failed").
		Str("command_id", cmdID).
		Str("pipe", name).
		Msg("clawkerd: pipe close failed during pipeline teardown")
	stats.logged = true
}

// closePipeOnce closes w and records the outcome on stats.
func (s *session) closePipeOnce(cmdID, name string, w io.Closer, stats *pipeCloseStats) {
	if w == nil {
		return
	}
	stats.record(s.log, cmdID, name, w.Close())
}

// errResponse is a small helper for the Error variant.
func errResponse(id string, code clawkerdv1.ErrorCode, msg string) *clawkerdv1.Response {
	return &clawkerdv1.Response{
		CommandId: id,
		Payload: &clawkerdv1.Response_Error{Error: &clawkerdv1.Error{
			Code:    code,
			Message: msg,
		}},
	}
}

// peerSummary returns the peer's leaf-cert CN and SHA-256 thumbprint
// (hex) extracted from the gRPC stream context. Returns empty strings
// if peer info or TLS info is unavailable. The clawkerd listener
// requires mTLS, so production always has both fields populated; tests
// that drive runSession without TLS fall through to empty values
// rather than panicking the audit log.
func peerSummary(ctx context.Context) (cn, thumbprintHex string) {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil {
		return "", ""
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
		return "", ""
	}
	leaf := tlsInfo.State.PeerCertificates[0]
	sum := sha256.Sum256(leaf.Raw)
	return leaf.Subject.CommonName, hex.EncodeToString(sum[:])
}
