package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

// fakeBidiStream satisfies grpc.BidiStreamingServer[Command, Response]
// without standing up a real gRPC handshake. The embedded
// grpc.ServerStream is nil — every method on it panics if called,
// which is what we want for tests that exercise only the surface
// runSender / receiver use (Send, Recv).
type fakeBidiStream struct {
	grpc.ServerStream
	sendErr  error
	sendOnce sync.Once
}

func (f *fakeBidiStream) Send(*clawkerdv1.Response) error {
	var err error
	f.sendOnce.Do(func() { err = f.sendErr })
	return err
}

func (f *fakeBidiStream) Recv() (*clawkerdv1.Command, error) {
	return nil, io.EOF
}

// trueBinPath returns an absolute path to a real `true` binary on the
// test host. macOS ships coreutils under /usr/bin/true; Linux (incl. the
// clawker container images) ships them under /bin/true. The candidate
// list is a closed set of absolute paths — no $PATH consultation — so a
// hostile env cannot shadow `true` with an arbitrary binary at test
// time. Skips the test if neither canonical location exists.
func trueBinPath(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/bin/true", "/usr/bin/true"} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	t.Skip("no `true` binary at /bin/true or /usr/bin/true on this host")
	return ""
}

// newTestSession builds a session whose sendCh and cmds map are
// exposed but no sender goroutine runs — tests drain responses
// directly off the channel. Returns the session plus a log buffer
// so tests can assert on emitted audit events.
func newTestSession() (*session, *bytes.Buffer) {
	var logBuf bytes.Buffer
	s := &session{
		log:    logger.NewWriter(&logBuf),
		sendCh: make(chan *clawkerdv1.Response, 256),
		cmds:   make(map[string]*runningCommand),
	}
	return s, &logBuf
}

func drainAll(s *session) []*clawkerdv1.Response {
	var out []*clawkerdv1.Response
	for {
		select {
		case r := <-s.sendCh:
			out = append(out, r)
		default:
			return out
		}
	}
}

// --- dispatch: command_id non-empty contract -----------------------

// TestDispatch_EmptyCommandID covers the contract that every payload
// EXCEPT Hello requires a non-empty command_id. Hello is a stateless
// echo; the others all create per-command state (running_command map,
// stdin pipe, signal route) that needs the ID as the lookup key.
func TestDispatch_EmptyCommandID(t *testing.T) {
	cases := []struct {
		name        string
		cmd         *clawkerdv1.Command
		expectError bool
	}{
		{
			name:        "shell rejected",
			cmd:         &clawkerdv1.Command{Payload: &clawkerdv1.Command_Shell{Shell: &clawkerdv1.ShellCommand{}}},
			expectError: true,
		},
		{
			name:        "stdin rejected",
			cmd:         &clawkerdv1.Command{Payload: &clawkerdv1.Command_Stdin{Stdin: &clawkerdv1.Stdin{Data: []byte("x")}}},
			expectError: true,
		},
		{
			name:        "close_stdin rejected",
			cmd:         &clawkerdv1.Command{Payload: &clawkerdv1.Command_CloseStdin{CloseStdin: &clawkerdv1.CloseStdin{}}},
			expectError: true,
		},
		{
			name:        "signal rejected",
			cmd:         &clawkerdv1.Command{Payload: &clawkerdv1.Command_Signal{Signal: &clawkerdv1.Signal{Signo: int32(syscall.SIGTERM)}}},
			expectError: true,
		},
		{
			name:        "register_required rejected",
			cmd:         &clawkerdv1.Command{Payload: &clawkerdv1.Command_RegisterRequired{RegisterRequired: &clawkerdv1.RegisterRequired{}}},
			expectError: true,
		},
		{
			name:        "agent_ready rejected",
			cmd:         &clawkerdv1.Command{Payload: &clawkerdv1.Command_AgentReady{AgentReady: &clawkerdv1.AgentReady{}}},
			expectError: true,
		},
		{
			// Hello is the inverse: stateless echo, empty command_id MUST
			// remain accepted.
			name:        "hello allowed",
			cmd:         &clawkerdv1.Command{Payload: &clawkerdv1.Command_Hello{Hello: &clawkerdv1.Hello{}}},
			expectError: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestSession()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			s.dispatch(ctx, tc.cmd)
			resps := drainAll(s)
			require.Len(t, resps, 1)
			if tc.expectError {
				er := resps[0].GetError()
				require.NotNil(t, er)
				// Pin code + message for the shell case (the canonical
				// error path); the others use the same path so checking
				// non-nil GetError is sufficient to catch a regression
				// that drops the rejection.
				if tc.name == "shell rejected" {
					assert.Equal(t, clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST, er.Code)
					assert.Contains(t, er.Message, "command_id required")
				}
			} else {
				assert.NotNil(t, resps[0].GetHelloAck())
			}
		})
	}
}

// --- dispatch: dup-detection on command_id -------------------------

func TestStartShellCommand_DuplicateID_Rejects(t *testing.T) {
	s, _ := newTestSession()
	ctx := t.Context()

	// Inject an already-running runningCommand with id "dup". Don't
	// bother spawning a real process — the dup check fires before
	// any pipeline setup.
	_, cmdCancel := context.WithCancel(ctx)
	defer cmdCancel()
	s.mu.Lock()
	s.cmds["dup"] = &runningCommand{id: "dup", cancel: cmdCancel}
	s.mu.Unlock()

	s.startShellCommand(ctx, "dup", &clawkerdv1.ShellCommand{
		Stages: []*clawkerdv1.PipeStage{{Argv: []string{trueBinPath(t)}}},
	})

	resps := drainAll(s)
	require.Len(t, resps, 1)
	er := resps[0].GetError()
	require.NotNil(t, er)
	assert.Equal(t, clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST, er.Code)
	assert.Contains(t, er.Message, "already in use")
}

// --- runShellCommand: audit log + happy path -----------------------

// runUntilDone spawns runShellCommand in a goroutine, waits for the
// stage to be registered (so routeCloseStdin can find it), routes a
// CloseStdin to unblock exec's stdin-copier goroutine, and returns
// when runShellCommand exits. Mirrors the real CP→clawkerd pattern:
// CP always sends CloseStdin once it has nothing more to write.
func runUntilDone(t *testing.T, ctx context.Context, s *session, sc *clawkerdv1.ShellCommand, id string) {
	t.Helper()
	cmdCtx, cmdCancel := context.WithCancel(ctx)
	stdinR, stdinW := io.Pipe()
	rc := &runningCommand{id: id, cancel: cmdCancel, stdin: stdinW, stdinReady: make(chan struct{})}
	s.mu.Lock()
	s.cmds[rc.id] = rc
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runShellCommand(cmdCtx, rc, sc, stdinR)
	}()

	// Wait for the stdin pipe to be wired up, then close it so the
	// exec stdin-copier goroutine unblocks and c.Wait() returns.
	closeDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(closeDeadline) {
		s.mu.Lock()
		cur, ok := s.cmds[rc.id]
		var ready bool
		if ok {
			cur.stdinMu.Lock()
			ready = cur.stdin != nil
			cur.stdinMu.Unlock()
		}
		s.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	s.routeCloseStdin(ctx, id)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("runShellCommand did not return in time for id=%s", id)
	}
}

func TestRunShellCommand_AuditLogStartedAndDone(t *testing.T) {
	s, logBuf := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	truePath := trueBinPath(t)
	runUntilDone(t, ctx, s, &clawkerdv1.ShellCommand{
		Stages: []*clawkerdv1.PipeStage{{Argv: []string{truePath}}},
	}, "audit-1")

	logs := logBuf.String()
	assert.Contains(t, logs, `"event":"shell_command_started"`, "started event missing")
	assert.Contains(t, logs, `"event":"shell_command_done"`, "done event missing")
	assert.Contains(t, logs, `"argv":["`+truePath+`"]`, "argv field missing")
	assert.Contains(t, logs, `"command_id":"audit-1"`)
	assert.Contains(t, logs, `"outcome":"completed"`)
	assert.Contains(t, logs, `"final_exit_code":0`)
	assert.Contains(t, logs, `"duration":`)

	resps := drainAll(s)
	var sawStarted, sawDone bool
	for _, r := range resps {
		if r.GetStarted() != nil {
			sawStarted = true
		}
		if r.GetDone() != nil {
			assert.Equal(t, int32(0), r.GetDone().FinalExitCode)
			sawDone = true
		}
	}
	assert.True(t, sawStarted, "Started response missing")
	assert.True(t, sawDone, "Done response missing")
}

func TestRunShellCommand_AuditLogOnSpawnFailure(t *testing.T) {
	// Non-existent binary path forces exec.Cmd.Start to fail with
	// ENOENT, exercising the start-loop spawn-failed return path.
	// Spawn-failure does not register a stdin pipe with a child, so
	// runShellCommand returns synchronously — no helper needed.
	s, logBuf := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmdCtx, cmdCancel := context.WithCancel(ctx)
	stdinR, stdinW := io.Pipe()
	rc := &runningCommand{id: "spawn-fail", cancel: cmdCancel, stdin: stdinW, stdinReady: make(chan struct{})}
	s.mu.Lock()
	s.cmds[rc.id] = rc
	s.mu.Unlock()

	s.runShellCommand(cmdCtx, rc, &clawkerdv1.ShellCommand{
		Stages: []*clawkerdv1.PipeStage{{Argv: []string{"/no/such/binary/clawker-test"}}},
	}, stdinR)

	logs := logBuf.String()
	assert.Contains(t, logs, `"event":"shell_command_started"`)
	assert.Contains(t, logs, `"event":"shell_command_done"`)
	assert.Contains(t, logs, `"outcome":"spawn_failed"`)

	resps := drainAll(s)
	var sawSpawnErr bool
	for _, r := range resps {
		if er := r.GetError(); er != nil && er.Code == clawkerdv1.ErrorCode_ERROR_CODE_SPAWN_FAILED {
			sawSpawnErr = true
		}
	}
	assert.True(t, sawSpawnErr, "SPAWN_FAILED response missing")
}

// TestStartShellCommand_InitialStdinCloseStdinRace pins the regression
// fix for the bug that caused agent-init's `ssh` step to land an empty
// known_hosts file: CP sends ShellCommand+InitialStdin and immediately
// follows with CloseStdin. Without the stdinReady gate, routeCloseStdin
// would run BEFORE the InitialStdin write goroutine, the Write would
// return ErrClosedPipe, and the payload would silently vanish.
//
// We dispatch through the real receiver path (runReceiver → dispatch →
// startShellCommand → ...) so the gate's race-window is fully
// exercised; child is `cat` which echoes its stdin to stdout. CP
// expects to see the InitialStdin payload back as a StdoutChunk; if
// the race is unfixed, stdout is empty.
func TestStartShellCommand_InitialStdinCloseStdinRace(t *testing.T) {
	const payload = "hello-from-initial-stdin\n"
	const id = "init-stdin-race-1"

	s, _ := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Dispatch ShellCommand and CloseStdin back-to-back via dispatch().
	// dispatch is synchronous from the receiver loop's perspective;
	// startShellCommand returns immediately after spawning the worker
	// goroutine, then we immediately route CloseStdin against the same
	// id — this is the exact ordering CP produces.
	s.dispatch(ctx, &clawkerdv1.Command{
		CommandId: id,
		Payload: &clawkerdv1.Command_Shell{Shell: &clawkerdv1.ShellCommand{
			Stages:         []*clawkerdv1.PipeStage{{Argv: []string{"/bin/cat"}}},
			InitialStdin:   []byte(payload),
			TimeoutSeconds: 5,
		}},
	})
	s.dispatch(ctx, &clawkerdv1.Command{
		CommandId: id,
		Payload:   &clawkerdv1.Command_CloseStdin{CloseStdin: &clawkerdv1.CloseStdin{}},
	})

	// Drain Responses until Done; assemble stdout.
	var stdout strings.Builder
	deadline := time.After(4 * time.Second)
	for {
		select {
		case r := <-s.sendCh:
			if r == nil {
				continue
			}
			if c := r.GetStdout(); c != nil && r.CommandId == id {
				stdout.Write(c.Data)
			}
			if d := r.GetDone(); d != nil && r.CommandId == id {
				assert.Equal(t, int32(0), d.FinalExitCode)
				assert.Equal(t, payload, stdout.String(),
					"InitialStdin payload must reach the child even when CloseStdin is sent immediately after ShellCommand")
				return
			}
			if e := r.GetError(); e != nil && r.CommandId == id {
				t.Fatalf("unexpected Error response: %v", e)
			}
		case <-deadline:
			t.Fatalf("Done never arrived; stdout so far: %q", stdout.String())
		}
	}
}

// TestRunShellCommand_FastExitNoIOError pins the regression fix for
// the bug that broke agent-init's `post-init` step on second boots
// (when the marker file makes the script exit in <500ms).
//
// exec.Cmd.Wait closes stdout/stderr pipes after the child exits.
// For a fast-exit command, the reaper goroutine wins the race against
// the in-flight Read on the drain side, the stdlib returns
// "read |0: file already closed" (a *fs.PathError wrapping
// os.ErrClosed) — not io.EOF. Without isExpectedDrainEnd filtering
// this, drainStdout/drainStderr would surface ERROR_CODE_IO_ERROR
// even though clawkerd's own audit log records outcome=completed.
//
// /bin/true is the canonical fast exit. Run it many times to keep
// the race window covered across scheduler vagaries; assert no
// ERROR_CODE_IO_ERROR leaks through.
func TestRunShellCommand_FastExitNoIOError(t *testing.T) {
	truePath := trueBinPath(t)
	const N = 20
	for i := range N {
		s, _ := newTestSession()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		runUntilDone(t, ctx, s, &clawkerdv1.ShellCommand{
			Stages: []*clawkerdv1.PipeStage{{Argv: []string{truePath}}},
		}, fmt.Sprintf("fast-exit-%d", i))
		for _, r := range drainAll(s) {
			if e := r.GetError(); e != nil {
				t.Fatalf("iter %d: unexpected Error response: code=%v msg=%q",
					i, e.Code, e.Message)
			}
		}
		cancel()
	}
}

// TestRunShellCommand_ConcurrentClean exercises the stage-reaper
// concurrency path. Run several pipelines in parallel under
// `go test -race` to catch the previous shared-slice write pattern
// (stageErrs[i] = waitErr) which flagged here under race detector.
// Each goroutine gets its own session so the shared log buffer
// can't itself be the race source.
func TestRunShellCommand_ConcurrentClean(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const N = 8
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, _ := newTestSession()
			runUntilDone(t, ctx, s, &clawkerdv1.ShellCommand{
				Stages: []*clawkerdv1.PipeStage{
					{Argv: []string{"/bin/echo", "hello"}},
					{Argv: []string{"/bin/cat"}},
				},
			}, "conc-"+string(rune('a'+i)))
		}(i)
	}
	wg.Wait()
}

// --- runSender: Send-failure cancels session ctx -------------------

// TestRunSender_SendFailureCancelsCtx pins the contract: when
// stream.Send returns error, runSender must call the cancel handed
// to it so producer goroutines blocked on `sendCh <- resp` unblock
// via their `<-ctx.Done` branch instead of parking until the
// receiver loop notices the broken transport. Without this, a
// half-broken stream (write side dead, read side momentarily alive)
// would strand in-flight Stdout/Stderr/Done responses for arbitrary
// time before the session fully tore down.
func TestRunSender_SendFailureCancelsCtx(t *testing.T) {
	s, _ := newTestSession()
	stream := &fakeBidiStream{sendErr: errors.New("synthetic Send failure")}
	s.stream = stream

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Push a response so runSender has something to drain.
	s.sendCh <- &clawkerdv1.Response{CommandId: "fail-1"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runSender(ctx, cancel)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runSender did not exit after Send failure")
	}

	// runSender must have called cancel — a producer goroutine that
	// would otherwise park on sendCh now races against ctx.Done and
	// the test verifies ctx is actually done.
	select {
	case <-ctx.Done():
	default:
		t.Fatal("runSender did not cancel ctx on Send failure; producer goroutines would park indefinitely")
	}
}

// --- closePipeOnce -------------------------------------------------

type errCloser struct {
	closed atomic.Int32
	err    error
}

func (e *errCloser) Close() error {
	e.closed.Add(1)
	return e.err
}

func TestClosePipeOnce_LogsExactlyOnce(t *testing.T) {
	s, logBuf := newTestSession()
	c := &errCloser{err: errors.New("synthetic close failure")}
	var stats pipeCloseStats
	for range 5 {
		s.closePipeOnce("cmd-x", "stdin", c, &stats)
	}
	out := logBuf.String()
	// First failure logs at Warn; remaining 4 increment Suppressed
	// silently. The summary line is emitted by runShellCommand on
	// exit, not by closePipeOnce, so this unit test sees only the
	// first event line.
	occurrences := strings.Count(out, `"event":"session_pipe_close_failed"`)
	assert.Equal(t, 1, occurrences, "want exactly one Warn line, got %d:\n%s", occurrences, out)
	assert.True(t, stats.logged)
	assert.Equal(t, 4, stats.suppressed, "remaining failures must be counted as suppressed")
	assert.Equal(t, int32(5), c.closed.Load(), "closer should still be invoked every call")
}

func TestClosePipeOnce_SilentOnClosedPipe(t *testing.T) {
	// io.ErrClosedPipe is success-equivalent — peer already closed.
	// The helper must not log and must not count toward suppressed.
	s, logBuf := newTestSession()
	var stats pipeCloseStats
	s.closePipeOnce("cmd-y", "stdin", &errCloser{err: io.ErrClosedPipe}, &stats)
	assert.NotContains(t, logBuf.String(), "session_pipe_close_failed")
	assert.False(t, stats.logged)
	assert.Zero(t, stats.suppressed)
}

// --- routeSignal: os.ErrProcessDone + ESRCH filter -----------------

func TestRouteSignal_FiltersErrProcessDone(t *testing.T) {
	// Spawn a real process that exits immediately. After Wait, the
	// kernel has reaped the pid and Go's os.Process.Signal returns
	// os.ErrProcessDone (Go 1.17+) or syscall.ESRCH depending on
	// runtime. Either way, routeSignal must NOT log at Error.
	c := exec.Command(trueBinPath(t))
	require.NoError(t, c.Start())
	require.NoError(t, c.Wait())

	// Sanity: confirm the signal indeed errors after reap.
	sigErr := c.Process.Signal(syscall.SIGTERM)
	require.Error(t, sigErr)
	require.True(t,
		errors.Is(sigErr, os.ErrProcessDone) || errors.Is(sigErr, syscall.ESRCH),
		"unexpected signal-after-reap err: %v", sigErr)

	s, logBuf := newTestSession()
	rc := &runningCommand{
		id:        "filter-1",
		processes: []*exec.Cmd{c},
	}
	s.mu.Lock()
	s.cmds[rc.id] = rc
	s.mu.Unlock()

	s.routeSignal(context.Background(), rc.id, &clawkerdv1.Signal{Signo: int32(syscall.SIGTERM)})

	logs := logBuf.String()
	assert.NotContains(t, logs, "session_signal_forward_failed",
		"reaper-race signal must not surface as Error log")
	assert.Contains(t, logs, "session_signal_after_exit",
		"reaper-race signal must surface as Debug audit event")
}

// TestRouteSignal_GuardClauses pins the two trivial argument-validation
// branches of routeSignal in one table. Merged from previously
// separate per-branch tests — the regression bait is identical.
func TestRouteSignal_GuardClauses(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		signo    int32
		wantCode clawkerdv1.ErrorCode
	}{
		{"zero_signo", "any", 0, clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST},
		{"unknown_command_id", "ghost", int32(syscall.SIGTERM), clawkerdv1.ErrorCode_ERROR_CODE_UNKNOWN_COMMAND_ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestSession()
			s.routeSignal(context.Background(), tc.id, &clawkerdv1.Signal{Signo: tc.signo})
			resps := drainAll(s)
			require.Len(t, resps, 1)
			er := resps[0].GetError()
			require.NotNil(t, er)
			assert.Equal(t, tc.wantCode, er.Code)
		})
	}
}

// --- shutdownRunning -----------------------------------------------

func TestShutdownRunning_CancelsAllCommands(t *testing.T) {
	s, _ := newTestSession()
	const N = 4
	ctxs := make([]context.Context, 0, N)
	for i := range N {
		c, cancel := context.WithCancel(context.Background())
		ctxs = append(ctxs, c)
		id := "rc-" + string(rune('a'+i))
		// Wrap cancel so shutdownRunning's rc.cancel() also cancels the
		// per-test ctx — production wires these together via
		// context.WithCancel(parent) inside startShellCommand.
		wrapped := func() { cancel() }
		s.mu.Lock()
		s.cmds[id] = &runningCommand{id: id, cancel: wrapped}
		s.mu.Unlock()
	}
	s.shutdownRunning()
	for i, c := range ctxs {
		select {
		case <-c.Done():
			// ok
		case <-time.After(time.Second):
			t.Fatalf("rc-%d ctx not cancelled by shutdownRunning", i)
		}
	}
}

// --- handleRegisterRequired ----------------------------------------------

// (Empty-command_id contract for RegisterRequired and AgentReady is
// pinned in the parameterized TestDispatch_EmptyCommandID table.)

// TestHandleRegisterRequired_NilCoordinator pins the safety branch:
// when no coordinator is wired, reply ok=false instead of hanging the
// CP-side dialer waiting for a Response that will never come.
func TestHandleRegisterRequired_NilCoordinator(t *testing.T) {
	s, _ := newTestSession()
	s.register = nil
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.handleRegisterRequired(ctx, "cmd-1")

	resps := drainAll(s)
	require.Len(t, resps, 1)
	rd := resps[0].GetRegisterDone()
	require.NotNil(t, rd)
	assert.False(t, rd.Ok)
	assert.NotEmpty(t, rd.Error)
	assert.Equal(t, "cmd-1", resps[0].CommandId)
}

// TestHandleRegisterRequired_HappyPath: with a coordinator that
// returns (true, ""), RegisterDone{Ok:true} ships back on the wire
// with the matching command_id.
func TestHandleRegisterRequired_HappyPath(t *testing.T) {
	s, _ := newTestSession()
	s.register = &registerCoordinator{
		exchange: func(_ context.Context, _, _ string, _ *tls.Config) (string, bool, error) {
			return "tok", true, nil
		},
	}
	s.register.dialAndRegister = func(context.Context, *logger.Logger, string) (bool, string) {
		return true, ""
	}
	// Prevent the inner runOnce from rejecting on missing env.
	s.register.hydraURL = "https://hydra.test"
	s.register.agentAddr = "127.0.0.1:1"
	caPEM, certPEM, keyPEM := validPEMs(t)
	s.register.boot = &bootstrap{
		CertPEM:   certPEM,
		KeyPEM:    keyPEM,
		CACertPEM: caPEM,
		Assertion: "fake.jwt",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.handleRegisterRequired(ctx, "cmd-happy")

	resp := waitOneResponse(t, s, time.Second)
	require.Equal(t, "cmd-happy", resp.CommandId)
	rd := resp.GetRegisterDone()
	require.NotNil(t, rd)
	assert.True(t, rd.Ok)
	assert.Empty(t, rd.Error)
}

// TestHandleRegisterRequired_PanicRecovery pins panic safety: a panic
// inside register.Run is caught, recovered, and surfaced as
// RegisterDone{Ok:false}. Without recovery the goroutine would crash
// the entire clawkerd daemon.
func TestHandleRegisterRequired_PanicRecovery(t *testing.T) {
	s, _ := newTestSession()
	s.register = &registerCoordinator{
		exchange: func(_ context.Context, _, _ string, _ *tls.Config) (string, bool, error) {
			panic("simulated regression in token exchange")
		},
	}
	s.register.dialAndRegister = func(context.Context, *logger.Logger, string) (bool, string) {
		return true, ""
	}
	s.register.hydraURL = "https://hydra.test"
	s.register.agentAddr = "127.0.0.1:1"
	caPEM, certPEM, keyPEM := validPEMs(t)
	s.register.boot = &bootstrap{
		CertPEM:   certPEM,
		KeyPEM:    keyPEM,
		CACertPEM: caPEM,
		Assertion: "fake.jwt",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.handleRegisterRequired(ctx, "cmd-panic")

	resp := waitOneResponse(t, s, time.Second)
	require.Equal(t, "cmd-panic", resp.CommandId)
	rd := resp.GetRegisterDone()
	require.NotNil(t, rd)
	assert.False(t, rd.Ok)
	assert.Contains(t, rd.Error, "register panic")
}

// waitOneResponse drains exactly one Response off s.sendCh within
// timeout. handleRegisterRequired is async (spawns a goroutine), so a
// drainAll snapshot may race the goroutine writing to sendCh.
func waitOneResponse(t *testing.T, s *session, timeout time.Duration) *clawkerdv1.Response {
	t.Helper()
	select {
	case r := <-s.sendCh:
		return r
	case <-time.After(timeout):
		t.Fatal("timed out waiting for RegisterDone Response")
		return nil
	}
}

// withFifoPath rebinds agentReadyFifoPath for the duration of one
// test. Restores the prior value via t.Cleanup so test ordering can't
// leak the override.
func withFifoPath(t *testing.T, path string) {
	t.Helper()
	prev := agentReadyFifoPath
	agentReadyFifoPath = path
	t.Cleanup(func() { agentReadyFifoPath = prev })
}

// TestHandleAgentReady_HappyPath verifies the contract that drives
// the entrypoint release: with a reader blocked on the fifo, the
// handler opens it for write, the kernel unblocks the reader, the
// handler returns Done{0}.
func TestHandleAgentReady_HappyPath(t *testing.T) {
	dir := t.TempDir()
	fifo := dir + "/agent.fifo"
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	withFifoPath(t, fifo)

	// Open the read side O_RDONLY|O_NONBLOCK in the test goroutine so
	// we know it's open before dispatch — handler's O_WRONLY|O_NONBLOCK
	// open then succeeds (reader fd present) instead of returning ENXIO.
	// O_NONBLOCK on the reader makes the open non-blocking; subsequent
	// reads still block until the writer arrives.
	rfd, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rfd.Close() })

	readerDone := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(rfd)
		readerDone <- err
	}()

	s, _ := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.handleAgentReady(ctx, "ar-happy")

	resp := waitOneResponse(t, s, time.Second)
	assert.Equal(t, "ar-happy", resp.CommandId)
	done := resp.GetDone()
	require.NotNil(t, done, "expected Done payload, got %T", resp.Payload)
	assert.Equal(t, int32(0), done.FinalExitCode)

	select {
	case err := <-readerDone:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("reader never observed the fifo write")
	}
}

// TestHandleAgentReady_NoReader covers the reconnect path: CP redials
// a long-running container after entrypoint already exec'd CMD, so
// the fifo has no reader. O_WRONLY|O_NONBLOCK returns ENXIO; handler
// treats it as no-op success.
func TestHandleAgentReady_NoReader(t *testing.T) {
	dir := t.TempDir()
	fifo := dir + "/agent.fifo"
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	withFifoPath(t, fifo)

	s, logBuf := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.handleAgentReady(ctx, "ar-no-reader")

	resp := waitOneResponse(t, s, time.Second)
	assert.Equal(t, "ar-no-reader", resp.CommandId)
	done := resp.GetDone()
	require.NotNil(t, done, "expected Done on ENXIO path, got %T", resp.Payload)
	assert.Equal(t, int32(0), done.FinalExitCode)
	assert.Contains(t, logBuf.String(), "agent_ready_no_reader",
		"audit log should record the no-reader path so operators can distinguish first-boot release from reconnect-replay")
}

// TestHandleAgentReady_FifoMissing covers the build-drift case: the
// entrypoint never created the fifo (image misconfiguration). Open
// returns fs.ErrNotExist; handler must surface NOT_FOUND (distinct
// from IO_ERROR) so operators can branch on the typed code without
// parsing the human-readable message.
func TestHandleAgentReady_FifoMissing(t *testing.T) {
	dir := t.TempDir()
	fifo := dir + "/never-created.fifo"
	withFifoPath(t, fifo)

	s, _ := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.handleAgentReady(ctx, "ar-missing")

	resp := waitOneResponse(t, s, time.Second)
	assert.Equal(t, "ar-missing", resp.CommandId)
	er := resp.GetError()
	require.NotNil(t, er, "expected Error payload, got %T", resp.Payload)
	assert.Equal(t, clawkerdv1.ErrorCode_ERROR_CODE_NOT_FOUND, er.Code,
		"missing fifo must classify as NOT_FOUND, distinct from syscall IO_ERROR")
}
