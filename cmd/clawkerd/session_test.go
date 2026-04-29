package main

import (
	"bytes"
	"context"
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

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

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

func TestDispatch_EmptyCommandID_RejectsForShell(t *testing.T) {
	s, _ := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.dispatch(ctx, &clawkerdv1.Command{
		Payload: &clawkerdv1.Command_Shell{Shell: &clawkerdv1.ShellCommand{}},
	})
	resps := drainAll(s)
	require.Len(t, resps, 1)
	er := resps[0].GetError()
	require.NotNil(t, er)
	assert.Equal(t, clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST, er.Code)
	assert.Contains(t, er.Message, "command_id required")
}

func TestDispatch_EmptyCommandID_RejectsForStdin(t *testing.T) {
	s, _ := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.dispatch(ctx, &clawkerdv1.Command{
		Payload: &clawkerdv1.Command_Stdin{Stdin: &clawkerdv1.Stdin{Data: []byte("x")}},
	})
	resps := drainAll(s)
	require.Len(t, resps, 1)
	er := resps[0].GetError()
	require.NotNil(t, er)
	assert.Equal(t, clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST, er.Code)
}

func TestDispatch_EmptyCommandID_RejectsForCloseStdin(t *testing.T) {
	s, _ := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.dispatch(ctx, &clawkerdv1.Command{
		Payload: &clawkerdv1.Command_CloseStdin{CloseStdin: &clawkerdv1.CloseStdin{}},
	})
	resps := drainAll(s)
	require.Len(t, resps, 1)
	require.NotNil(t, resps[0].GetError())
}

func TestDispatch_EmptyCommandID_RejectsForSignal(t *testing.T) {
	s, _ := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.dispatch(ctx, &clawkerdv1.Command{
		Payload: &clawkerdv1.Command_Signal{Signal: &clawkerdv1.Signal{Signo: int32(syscall.SIGTERM)}},
	})
	resps := drainAll(s)
	require.Len(t, resps, 1)
	require.NotNil(t, resps[0].GetError())
}

func TestDispatch_EmptyCommandID_AllowsHello(t *testing.T) {
	// Hello is a stateless echo with no dup tracking — empty
	// command_id must remain accepted to preserve compatibility.
	s, _ := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.dispatch(ctx, &clawkerdv1.Command{
		Payload: &clawkerdv1.Command_Hello{Hello: &clawkerdv1.Hello{}},
	})
	resps := drainAll(s)
	require.Len(t, resps, 1)
	assert.NotNil(t, resps[0].GetHelloAck())
}

// --- dispatch: dup-detection on command_id -------------------------

func TestStartShellCommand_DuplicateID_Rejects(t *testing.T) {
	s, _ := newTestSession()
	ctx := t.Context()

	// Inject an already-running runningCommand with id "dup". Don't
	// bother spawning a real process — the dup check fires before
	// any pipeline setup.
	cmdCtx, cmdCancel := context.WithCancel(ctx)
	defer cmdCancel()
	s.mu.Lock()
	s.cmds["dup"] = &runningCommand{id: "dup", ctx: cmdCtx, cancel: cmdCancel}
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
	rc := &runningCommand{id: id, ctx: cmdCtx, cancel: cmdCancel}
	s.mu.Lock()
	s.cmds[rc.id] = rc
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runShellCommand(rc, sc)
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

	runUntilDone(t, ctx, s, &clawkerdv1.ShellCommand{
		Stages: []*clawkerdv1.PipeStage{{Argv: []string{"/bin/true"}}},
	}, "audit-1")

	logs := logBuf.String()
	assert.Contains(t, logs, `"event":"shell_command_started"`, "started event missing")
	assert.Contains(t, logs, `"event":"shell_command_done"`, "done event missing")
	assert.Contains(t, logs, `"argv":["/bin/true"]`, "argv field missing")
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
	rc := &runningCommand{id: "spawn-fail", ctx: cmdCtx, cancel: cmdCancel}
	s.mu.Lock()
	s.cmds[rc.id] = rc
	s.mu.Unlock()

	s.runShellCommand(rc, &clawkerdv1.ShellCommand{
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
	var logged bool
	for range 5 {
		s.closePipeOnce("cmd-x", "stdin", c, &logged)
	}
	out := logBuf.String()
	occurrences := strings.Count(out, "session_pipe_close_failed")
	assert.Equal(t, 1, occurrences, "want exactly one Warn line, got %d:\n%s", occurrences, out)
	assert.True(t, logged)
	assert.Equal(t, int32(5), c.closed.Load(), "closer should still be invoked every call")
}

func TestClosePipeOnce_SilentOnClosedPipe(t *testing.T) {
	// io.ErrClosedPipe is success-equivalent — peer already closed.
	// The helper must not log it.
	s, logBuf := newTestSession()
	var logged bool
	s.closePipeOnce("cmd-y", "stdin", &errCloser{err: io.ErrClosedPipe}, &logged)
	assert.NotContains(t, logBuf.String(), "session_pipe_close_failed")
	assert.False(t, logged)
}

// --- routeSignal: os.ErrProcessDone + ESRCH filter -----------------

func TestRouteSignal_FiltersErrProcessDone(t *testing.T) {
	// Spawn a real process that exits immediately. After Wait, the
	// kernel has reaped the pid and Go's os.Process.Signal returns
	// os.ErrProcessDone (Go 1.17+) or syscall.ESRCH on older runtimes.
	// Either way, routeSignal must NOT log at Error.
	c := exec.Command("/bin/true")
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
		s.mu.Lock()
		s.cmds[id] = &runningCommand{id: id, ctx: c, cancel: cancel}
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
