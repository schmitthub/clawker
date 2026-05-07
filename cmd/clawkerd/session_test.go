package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
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
		Stages: []*clawkerdv1.PipeStage{{Argv: []string{"/bin/true"}}},
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
	rc := &runningCommand{id: id, cancel: cmdCancel}
	s.mu.Lock()
	s.cmds[rc.id] = rc
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runShellCommand(cmdCtx, rc, sc)
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
	rc := &runningCommand{id: "spawn-fail", cancel: cmdCancel}
	s.mu.Lock()
	s.cmds[rc.id] = rc
	s.mu.Unlock()

	s.runShellCommand(cmdCtx, rc, &clawkerdv1.ShellCommand{
		Stages: []*clawkerdv1.PipeStage{{Argv: []string{"/no/such/binary/clawker-test"}}},
	})

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
	// os.ErrProcessDone (Go 1.17+) or syscall.ESRCH or "process already
	// finished" depending on runtime. Either way, routeSignal must NOT
	// log at Error.
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

func TestRouteSignal_RejectsZeroSigno(t *testing.T) {
	s, _ := newTestSession()
	s.routeSignal(context.Background(), "any", &clawkerdv1.Signal{Signo: 0})
	resps := drainAll(s)
	require.Len(t, resps, 1)
	er := resps[0].GetError()
	require.NotNil(t, er)
	assert.Equal(t, clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST, er.Code)
}

func TestRouteSignal_UnknownCommandID(t *testing.T) {
	s, _ := newTestSession()
	s.routeSignal(context.Background(), "ghost", &clawkerdv1.Signal{Signo: int32(syscall.SIGTERM)})
	resps := drainAll(s)
	require.Len(t, resps, 1)
	er := resps[0].GetError()
	require.NotNil(t, er)
	assert.Equal(t, clawkerdv1.ErrorCode_ERROR_CODE_UNKNOWN_COMMAND_ID, er.Code)
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

// TestDispatch_RegisterRequired_EmptyCommandID pins the contract that
// RegisterRequired (like every non-Hello payload) requires a non-empty
// command_id. Without it CP cannot correlate the RegisterDone reply.
func TestDispatch_RegisterRequired_EmptyCommandID(t *testing.T) {
	s, _ := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.dispatch(ctx, &clawkerdv1.Command{
		Payload: &clawkerdv1.Command_RegisterRequired{RegisterRequired: &clawkerdv1.RegisterRequired{}},
	})
	resps := drainAll(s)
	require.Len(t, resps, 1)
	er := resps[0].GetError()
	require.NotNil(t, er)
	assert.Equal(t, clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST, er.Code)
}

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
