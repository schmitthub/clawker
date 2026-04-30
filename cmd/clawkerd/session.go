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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

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
func runSession(stream clawkerdv1.ClawkerdService_SessionServer, log *logger.Logger) error {
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
		log.Info().
			Str("event", "session_ended").
			Str("peer_cn", peerCN).
			Dur("duration", time.Since(startedAt)).
			Msg("clawkerd: Session ended")
	}()

	s := &session{
		log:    log,
		stream: stream,
		sendCh: make(chan *clawkerdv1.Response, sendQueueDepth),
		cmds:   make(map[string]*runningCommand),
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
}

// runningCommand tracks one in-flight ShellCommand for routing
// follow-up Stdin / CloseStdin / Signal frames. The per-command ctx
// (derived from the Session ctx by startShellCommand) is plumbed as a
// first parameter through runShellCommand and the drainers — never
// stored on the struct, per the project ctx-handling rule.
type runningCommand struct {
	id     string
	cancel context.CancelFunc

	stdinMu     sync.Mutex
	stdin       io.WriteCloser // writer half of stage[0] stdin pipe
	stdinClosed bool

	// processes holds each stage's *exec.Cmd. Index matches stage_index.
	// Used to forward Signal frames to each stage's process.
	processes []*exec.Cmd
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
			return
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
		}
	}
}

// send pushes a Response onto sendCh. Drops on ctx-done so producer
// goroutines unblock when the stream is tearing down.
func (s *session) send(ctx context.Context, resp *clawkerdv1.Response) {
	select {
	case s.sendCh <- resp:
	case <-ctx.Done():
	}
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
	default:
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
	rc := &runningCommand{
		id:     id,
		cancel: cmdCancel,
	}
	s.cmds[id] = rc
	s.mu.Unlock()

	go s.runShellCommand(cmdCtx, rc, sc)
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
func (s *session) runShellCommand(ctx context.Context, rc *runningCommand, sc *clawkerdv1.ShellCommand) {
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

	// Build each stage's *exec.Cmd. Use CommandContext so a ctx
	// cancel (timeout, Session teardown) sends SIGKILL automatically.
	cmds := make([]*exec.Cmd, len(sc.Stages))
	for i, st := range sc.Stages {
		c := exec.CommandContext(ctx, st.Argv[0], st.Argv[1:]...)
		c.Dir = st.Cwd
		c.Env = buildEnv(st.Env)
		// Per-stage credential: drop privileges if uid/gid set. Zero
		// means "inherit from clawkerd" (which currently runs as
		// root inside the container — see entrypoint.sh).
		if st.Uid != 0 || st.Gid != 0 {
			c.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{Uid: st.Uid, Gid: st.Gid},
			}
		}
		cmds[i] = c
	}
	rc.processes = cmds

	// Wire stdin into stage[0]: io.Pipe so we can feed initial_stdin
	// and subsequent Stdin frames.
	stdinR, stdinW := io.Pipe()
	cmds[0].Stdin = stdinR
	rc.stdinMu.Lock()
	rc.stdin = stdinW
	rc.stdinMu.Unlock()

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

	// Chain stage[i].stdout → stage[i+1].stdin via os pipes. Capture
	// the final stage's stdout for streaming.
	stagePipes := make([]io.ReadCloser, len(cmds)-1)
	for i := 0; i < len(cmds)-1; i++ {
		out, err := cmds[i].StdoutPipe()
		if err != nil {
			s.send(ctx, errResponse(rc.id,
				clawkerdv1.ErrorCode_ERROR_CODE_SPAWN_FAILED,
				fmt.Sprintf("stage[%d] StdoutPipe: %v", i, err)))
			s.closePipeOnce(rc.id, "stdin", stdinW, &closeStats)
			auditOutcome = "spawn_failed"
			return
		}
		stagePipes[i] = out
		cmds[i+1].Stdin = out
	}

	finalStdout, err := cmds[len(cmds)-1].StdoutPipe()
	if err != nil {
		s.send(ctx, errResponse(rc.id,
			clawkerdv1.ErrorCode_ERROR_CODE_SPAWN_FAILED,
			fmt.Sprintf("final stage StdoutPipe: %v", err)))
		s.closePipeOnce(rc.id, "stdin", stdinW, &closeStats)
		auditOutcome = "spawn_failed"
		return
	}

	// Per-stage stderr pipes for streaming StderrChunk.
	stderrPipes := make([]io.ReadCloser, len(cmds))
	for i := range cmds {
		errPipe, perr := cmds[i].StderrPipe()
		if perr != nil {
			s.send(ctx, errResponse(rc.id,
				clawkerdv1.ErrorCode_ERROR_CODE_SPAWN_FAILED,
				fmt.Sprintf("stage[%d] StderrPipe: %v", i, perr)))
			s.closePipeOnce(rc.id, "stdin", stdinW, &closeStats)
			auditOutcome = "spawn_failed"
			return
		}
		stderrPipes[i] = errPipe
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

	// Drain initial_stdin into stage[0] in a goroutine — a large
	// initial_stdin must not block before we start reading outputs,
	// or the pipeline can deadlock if stage[0] is a filter that
	// produces stdout while consuming stdin.
	if len(sc.InitialStdin) > 0 {
		go func() {
			rc.stdinMu.Lock()
			w := rc.stdin
			rc.stdinMu.Unlock()
			if w == nil {
				return
			}
			if _, werr := w.Write(sc.InitialStdin); werr != nil && !errors.Is(werr, io.ErrClosedPipe) {
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
			s.drainStderr(ctx, rc, uint32(i), stderrPipes[i])
		}()
	}

	// Stdout drainer — final stage only.
	wg.Add(1)
	go func() {
		defer wg.Done()
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
			waitErr := c.Wait()
			if isFinal {
				finalStageErrCh <- waitErr
			}
			s.send(ctx, stageExitResponse(rc.id, uint32(i), c, waitErr))
		}()
	}

	// Block until every reaper finishes so the stdin writer can be
	// safely closed (no further data flow possible) and downstream
	// pipes drain.
	reapWG.Wait()
	rc.stdinMu.Lock()
	if !rc.stdinClosed {
		s.closePipeOnce(rc.id, "stdin", stdinW, &closeStats)
		rc.stdinClosed = true
	}
	rc.stdinMu.Unlock()
	for i, p := range stagePipes {
		s.closePipeOnce(rc.id, fmt.Sprintf("stage[%d]_stdout", i), p, &closeStats)
	}

	// Wait for stdout/stderr drainers to finish so chunks can't
	// arrive after Done.
	wg.Wait()

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

// drainStdout reads chunks from the final stage's stdout pipe and
// emits StdoutChunk responses until EOF or read error. Read errors
// other than EOF / ErrClosedPipe are surfaced as IO_ERROR but do not
// kill the pipeline — the reaper still emits Done/StageExit.
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
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
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
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
			return
		}
		s.send(ctx, errResponse(rc.id,
			clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR,
			fmt.Sprintf("stderr drain stage[%d]: %v", stageIndex, err)))
		return
	}
}

// routeStdin writes a Stdin frame's bytes into the target command's
// stage[0] stdin. UNKNOWN_COMMAND_ID if no such command is running.
func (s *session) routeStdin(ctx context.Context, id string, st *clawkerdv1.Stdin) {
	rc := s.lookup(id)
	if rc == nil {
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_UNKNOWN_COMMAND_ID,
			"stdin: no running command with that id"))
		return
	}
	rc.stdinMu.Lock()
	w := rc.stdin
	closed := rc.stdinClosed
	rc.stdinMu.Unlock()
	if closed || w == nil {
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
			"stdin: already closed"))
		return
	}
	if _, err := w.Write(st.Data); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR,
			fmt.Sprintf("stdin write: %v", err)))
	}
}

// routeCloseStdin closes stage[0]'s stdin pipe so a stdin-reading
// command sees EOF. Idempotent.
func (s *session) routeCloseStdin(ctx context.Context, id string) {
	rc := s.lookup(id)
	if rc == nil {
		s.send(ctx, errResponse(id,
			clawkerdv1.ErrorCode_ERROR_CODE_UNKNOWN_COMMAND_ID,
			"close_stdin: no running command with that id"))
		return
	}
	rc.stdinMu.Lock()
	defer rc.stdinMu.Unlock()
	if rc.stdinClosed {
		return
	}
	if rc.stdin != nil {
		_ = rc.stdin.Close()
	}
	rc.stdinClosed = true
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
	for i, c := range rc.processes {
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
func stageExitResponse(id string, stageIndex uint32, c *exec.Cmd, waitErr error) *clawkerdv1.Response {
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

// closePipeOnce closes w, logging at Warn on real errors. `stats`
// dedupes per-goroutine: a runShellCommand goroutine passes the same
// *pipeCloseStats to each call site so a torrent of failed closes
// during pipeline teardown produces exactly one Warn line + a
// suppressed counter the caller flushes on exit. io.ErrClosedPipe is
// treated as success since it just means a peer already closed.
func (s *session) closePipeOnce(cmdID, name string, w io.Closer, stats *pipeCloseStats) {
	if w == nil {
		return
	}
	err := w.Close()
	if err == nil || errors.Is(err, io.ErrClosedPipe) {
		return
	}
	if stats.logged {
		stats.suppressed++
		return
	}
	s.log.Warn().Err(err).
		Str("event", "session_pipe_close_failed").
		Str("command_id", cmdID).
		Str("pipe", name).
		Msg("clawkerd: pipe close failed during pipeline teardown")
	stats.logged = true
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
