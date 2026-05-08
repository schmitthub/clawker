package agent

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// expectedInitStepNames pins the static plan to the load-bearing
// vocabulary subscribers (CLI WatchAgent, monitoring, log greppers)
// match against. Reordering or renaming any of these is a CP-side
// breaking change and must be paired with subscriber updates.
var expectedInitStepNames = []string{
	"docker-socket",
	"config",
	"git",
	"git-credentials",
	"ssh",
	"post-init",
	"agent-ready",
}

// initEventCapture subscribes to every init event type before the
// Executor runs so no event can race past the subscription.
type initEventCapture struct {
	started       overseer.Subscription[InitStarted]
	stepStarted   overseer.Subscription[InitStepStarted]
	stepCompleted overseer.Subscription[InitStepCompleted]
	stepFailed    overseer.Subscription[InitStepFailed]
	completed     overseer.Subscription[InitCompleted]
	failed        overseer.Subscription[InitFailed]
}

func subscribeInitEvents(t *testing.T, bus *overseer.Overseer) *initEventCapture {
	t.Helper()
	c := &initEventCapture{}
	var ok bool
	c.started, ok = overseer.Subscribe[InitStarted](bus, "init-started")
	require.True(t, ok)
	c.stepStarted, ok = overseer.Subscribe[InitStepStarted](bus, "init-step-started")
	require.True(t, ok)
	c.stepCompleted, ok = overseer.Subscribe[InitStepCompleted](bus, "init-step-completed")
	require.True(t, ok)
	c.stepFailed, ok = overseer.Subscribe[InitStepFailed](bus, "init-step-failed")
	require.True(t, ok)
	c.completed, ok = overseer.Subscribe[InitCompleted](bus, "init-completed")
	require.True(t, ok)
	c.failed, ok = overseer.Subscribe[InitFailed](bus, "init-failed")
	require.True(t, ok)
	t.Cleanup(func() {
		c.started.Unsubscribe()
		c.stepStarted.Unsubscribe()
		c.stepCompleted.Unsubscribe()
		c.stepFailed.Unsubscribe()
		c.completed.Unsubscribe()
		c.failed.Unsubscribe()
	})
	return c
}

// drainInitEvents reads from each event channel for up to wait, then
// returns the collected slices. Used after the Executor returns so we
// can assert on the full sequence without flake.
func (c *initEventCapture) drain(wait time.Duration) initEventSlice {
	deadline := time.After(wait)
	out := initEventSlice{}
	for {
		select {
		case e := <-c.started.C:
			out.started = append(out.started, e)
		case e := <-c.stepStarted.C:
			out.stepStarted = append(out.stepStarted, e)
		case e := <-c.stepCompleted.C:
			out.stepCompleted = append(out.stepCompleted, e)
		case e := <-c.stepFailed.C:
			out.stepFailed = append(out.stepFailed, e)
		case e := <-c.completed.C:
			out.completed = append(out.completed, e)
		case e := <-c.failed.C:
			out.failed = append(out.failed, e)
		case <-deadline:
			return out
		}
	}
}

type initEventSlice struct {
	started       []InitStarted
	stepStarted   []InitStepStarted
	stepCompleted []InitStepCompleted
	stepFailed    []InitStepFailed
	completed     []InitCompleted
	failed        []InitFailed
}

// (Step name + ordering is pinned by TestExecutor_Run_HappyPath, which
// asserts events.stepStarted[i].StepName == expectedInitStepNames[i]
// after a real Run dispatches every step. Step kind / stages shape is
// enforced structurally by runStep's switch + Go's type system.)

// TestNewExecutor_NilBusReturnsError pins the constructor-time
// rejection of a nil bus. Returning an error (vs panicking) is
// load-bearing for CP resilience: a wiring bug must surface as a
// structured log line on the project's normal log surface, not crash
// CP and strand the trace on os.Stderr where only `docker logs <cp>`
// sees it. main.go logs and proceeds with initExec = nil; the dialer
// logs agent_init_executor_unset per dial. CP stays up.
func TestNewExecutor_NilBusReturnsError(t *testing.T) {
	exec, err := NewExecutor(nil, logger.Nop())
	require.Error(t, err, "NewExecutor must reject nil bus")
	assert.Nil(t, exec)
	assert.Contains(t, err.Error(), "bus is required")
}

// TestExecutor_Plan_PrivilegeAndShape pins three load-bearing plan
// invariants:
//
//  1. docker-socket is the SOLE uid=0 step (privilege-drop contract).
//  2. AgentReady is the LAST step (any later step would race CMD
//     execution past the entrypoint fifo release).
//  3. ssh's InitialStdin is non-empty (the host-key blob known_hosts
//     consumes; an empty payload silently produces an empty file).
//
// The per-stage HOME/USER override + uid/gid match against
// consts.Container* values is enforced structurally by userStage:
// build-time wiring, not a runtime invariant — exercised once in
// TestExecutor_Run_HappyPath via the full plan.
func TestExecutor_Plan_PrivilegeAndShape(t *testing.T) {
	bus := overseer.New(overseer.Options{})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })
	exec, err := NewExecutor(bus, logger.Nop())
	require.NoError(t, err)
	plan := exec.plan()
	require.NotEmpty(t, plan)

	var rootSteps []string
	for _, st := range plan {
		s, ok := st.(shellStep)
		if !ok {
			continue
		}
		if s.Shell.Stages[0].Uid == 0 {
			rootSteps = append(rootSteps, s.Name)
		}
	}
	assert.Equal(t, []string{"docker-socket"}, rootSteps,
		"only docker-socket may run as root")

	last := plan[len(plan)-1]
	_, isAgentReady := last.(agentReadyStep)
	assert.True(t, isAgentReady, "agent-ready must be terminal (no step may follow)")

	for _, st := range plan {
		if s, ok := st.(shellStep); ok && s.Name == "ssh" {
			assert.NotEmpty(t, s.Shell.InitialStdin,
				"ssh step must carry the known_hosts blob via InitialStdin")
			return
		}
	}
	t.Fatal("ssh step missing from plan")
}

// TestExecutor_Run_HappyPath drives the full plan with Done{0} on
// every step and verifies the event sequence subscribers see:
// InitStarted, 7×(StepStarted,StepCompleted), InitCompleted; no
// failures.
func TestExecutor_Run_HappyPath(t *testing.T) {
	bus := overseer.New(overseer.Options{})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })

	caps := subscribeInitEvents(t, bus)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := newFakeStream(ctx)
	exec, errExec := NewExecutor(bus, logger.Nop())
	require.NoError(t, errExec)
	target := InitTarget{ContainerID: "c-happy-1234567890ab", AgentName: "dev", Project: "clawker"}

	// Stream-feeder goroutine: for each Command sent by Run, push back
	// a matching Done{0}. Decouples the push timing from Send timing
	// so Run's Recv has a frame waiting on every step.
	doneFeeder := make(chan struct{})
	go func() {
		defer close(doneFeeder)
		for cmd := range stream.sent {
			// Skip CloseStdin frames the Executor sends after each
			// ShellCommand to signal stdin EOF — they're routing-only,
			// no response expected from clawkerd.
			if _, isClose := cmd.Payload.(*clawkerdv1.Command_CloseStdin); isClose {
				continue
			}
			stream.recvCh <- recvFrame{resp: &clawkerdv1.Response{
				CommandId: cmd.CommandId,
				Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 0}},
			}}
		}
	}()

	err := exec.Run(ctx, stream, target)
	close(stream.sent)
	<-doneFeeder
	require.NoError(t, err)

	events := caps.drain(500 * time.Millisecond)
	require.Len(t, events.started, 1, "exactly one InitStarted")
	require.Len(t, events.completed, 1, "exactly one InitCompleted")
	assert.Empty(t, events.stepFailed, "no step failures expected")
	assert.Empty(t, events.failed, "no terminal InitFailed expected")
	assert.Equal(t, len(expectedInitStepNames), events.started[0].StepCount)
	assert.Equal(t, target.ContainerID, events.completed[0].ContainerID)

	// Step events: every step name appears once in started and once in
	// completed, in plan order.
	require.Len(t, events.stepStarted, len(expectedInitStepNames))
	require.Len(t, events.stepCompleted, len(expectedInitStepNames))
	for i, name := range expectedInitStepNames {
		assert.Equal(t, name, events.stepStarted[i].StepName, "stepStarted[%d]", i)
		assert.Equal(t, i, events.stepStarted[i].StepIndex)
		assert.Equal(t, name, events.stepCompleted[i].StepName, "stepCompleted[%d]", i)
		assert.Equal(t, int32(0), events.stepCompleted[i].ExitCode)
	}
}

// TestExecutor_Run_StepFailureHaltsAndPublishesFailed feeds Done{0}
// for the first N-1 steps and Done{exit=2,stderr="boom"} for step N.
// Run must halt at step N, publish InitStepFailed{step:N} +
// InitFailed{failed_step:N}, return a non-nil error, and NOT publish
// InitCompleted.
func TestExecutor_Run_StepFailureHaltsAndPublishesFailed(t *testing.T) {
	const failAtIdx = 2 // "git"
	bus := overseer.New(overseer.Options{})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })

	caps := subscribeInitEvents(t, bus)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := newFakeStream(ctx)
	exec, errExec := NewExecutor(bus, logger.Nop())
	require.NoError(t, errExec)
	target := InitTarget{ContainerID: "c-fail-9876543210ab", AgentName: "dev", Project: "clawker"}

	doneFeeder := make(chan struct{})
	stepCount := 0
	go func() {
		defer close(doneFeeder)
		for cmd := range stream.sent {
			// Skip CloseStdin frames — those don't get responses.
			if _, isClose := cmd.Payload.(*clawkerdv1.Command_CloseStdin); isClose {
				continue
			}
			if stepCount == failAtIdx {
				stream.recvCh <- recvFrame{resp: &clawkerdv1.Response{
					CommandId: cmd.CommandId,
					Payload:   &clawkerdv1.Response_Stderr{Stderr: &clawkerdv1.StderrChunk{Data: []byte("boom")}},
				}}
				stream.recvCh <- recvFrame{resp: &clawkerdv1.Response{
					CommandId: cmd.CommandId,
					Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 2}},
				}}
			} else {
				stream.recvCh <- recvFrame{resp: &clawkerdv1.Response{
					CommandId: cmd.CommandId,
					Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 0}},
				}}
			}
			stepCount++
		}
	}()

	err := exec.Run(ctx, stream, target)
	close(stream.sent)
	<-doneFeeder
	require.Error(t, err)
	assert.Contains(t, err.Error(), expectedInitStepNames[failAtIdx])

	events := caps.drain(500 * time.Millisecond)
	require.Len(t, events.started, 1)
	require.Len(t, events.stepFailed, 1)
	require.Len(t, events.failed, 1)
	assert.Empty(t, events.completed, "InitCompleted must NOT fire on failure")
	assert.Equal(t, expectedInitStepNames[failAtIdx], events.stepFailed[0].StepName)
	assert.Equal(t, int32(2), events.stepFailed[0].ExitCode)
	assert.Equal(t, overseer.InitFailureReasonExitCode, events.stepFailed[0].Reason)
	assert.Contains(t, events.stepFailed[0].Detail, "boom")
	assert.Equal(t, expectedInitStepNames[failAtIdx], events.failed[0].FailedStep)
	assert.Equal(t, overseer.InitFailureReasonExitCode, events.failed[0].Reason)

	// Steps after the failure must not have started.
	require.Len(t, events.stepStarted, failAtIdx+1, "step dispatch must halt at first failure")
	require.Len(t, events.stepCompleted, failAtIdx, "no completed event for the failing step")
}

// TestExecutor_Run_TransportError covers the case where stream.Recv
// returns a non-EOF error — the stream is broken; Run returns an
// error and publishes InitFailed but nothing later.
func TestExecutor_Run_TransportError(t *testing.T) {
	bus := overseer.New(overseer.Options{})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })

	caps := subscribeInitEvents(t, bus)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := newFakeStream(ctx)
	exec, errExec := NewExecutor(bus, logger.Nop())
	require.NoError(t, errExec)
	target := InitTarget{ContainerID: "c-transport-1234567890ab", AgentName: "dev", Project: "clawker"}

	// Push an error frame that Run will see on its first Recv.
	go func() {
		<-stream.sent // wait for first Send to land
		stream.recvCh <- recvFrame{err: errors.New("rpc connection reset")}
	}()

	err := exec.Run(ctx, stream, target)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpc connection reset")

	events := caps.drain(500 * time.Millisecond)
	require.Len(t, events.started, 1)
	require.Len(t, events.stepFailed, 1, "transport failure must carry a step-level event so subscribers see WHICH step was in flight")
	require.Len(t, events.failed, 1)
	assert.Empty(t, events.completed)
	assert.Equal(t, overseer.InitFailureReasonTransportError, events.failed[0].Reason)
	assert.Contains(t, events.failed[0].Detail, "rpc connection reset")
}

// TestExecutor_Run_StreamErrorResponse pins the typed-classification
// surface for clawkerd-side ErrorCodes. Without explicit coverage, a
// regression in classifyErrorCode (e.g. dropping the SPAWN_FAILED
// case) would surface as InitFailureReasonUnknown on operator
// dashboards with no compile-time signal.
func TestExecutor_Run_StreamErrorResponse(t *testing.T) {
	cases := []struct {
		name       string
		code       clawkerdv1.ErrorCode
		wantReason overseer.InitFailureReason
	}{
		{"timeout", clawkerdv1.ErrorCode_ERROR_CODE_TIMEOUT, overseer.InitFailureReasonTimeout},
		{"spawn_failed", clawkerdv1.ErrorCode_ERROR_CODE_SPAWN_FAILED, overseer.InitFailureReasonSpawnFailed},
		{"io_error", clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR, overseer.InitFailureReasonIOError},
		{"not_found", clawkerdv1.ErrorCode_ERROR_CODE_NOT_FOUND, overseer.InitFailureReasonIOError},
		{"invalid_request", clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST, overseer.InitFailureReasonProtocol},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := overseer.New(overseer.Options{})
			require.NoError(t, bus.Start(context.Background()))
			t.Cleanup(func() { _ = bus.Close() })

			caps := subscribeInitEvents(t, bus)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			stream := newFakeStream(ctx)
			exec, errExec := NewExecutor(bus, logger.Nop())
			require.NoError(t, errExec)
			target := InitTarget{ContainerID: "c-err-1234567890ab", AgentName: "dev", Project: "clawker"}

			done := make(chan struct{})
			go func() {
				defer close(done)
				for cmd := range stream.sent {
					if _, isClose := cmd.Payload.(*clawkerdv1.Command_CloseStdin); isClose {
						continue
					}
					stream.recvCh <- recvFrame{resp: &clawkerdv1.Response{
						CommandId: cmd.CommandId,
						Payload: &clawkerdv1.Response_Error{Error: &clawkerdv1.Error{
							Code:    tc.code,
							Message: "synthetic " + tc.name,
						}},
					}}
				}
			}()

			err := exec.Run(ctx, stream, target)
			close(stream.sent)
			<-done
			require.Error(t, err)

			events := caps.drain(500 * time.Millisecond)
			require.Len(t, events.failed, 1)
			require.Len(t, events.stepFailed, 1)
			assert.Equal(t, tc.wantReason, events.stepFailed[0].Reason)
			assert.Equal(t, tc.wantReason, events.failed[0].Reason)
			assert.Contains(t, events.failed[0].Detail, tc.code.String())
			assert.Contains(t, events.failed[0].Detail, "synthetic "+tc.name)
		})
	}
}

// TestExecutor_Run_StateProjection drives Run twice (first with a
// failure, then a success) and asserts the overseer worldview's Init
// axis reflects every transition: zero → Running → Failed → Running →
// Completed, with Init.LastError clearing on the success cycle. The
// projection contract is what subscribers (CLI WatchAgent, monitoring)
// consume — an ApplyTo-method regression here would silently break
// operator-facing UX.
func TestExecutor_Run_StateProjection(t *testing.T) {
	bus := overseer.New(overseer.Options{})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })

	// Subscribe to the terminal events BEFORE running so we can wait
	// for them to drain through the bus loop before snapshotting —
	// Publish enqueues asynchronously, ApplyTo runs on the loop.
	failedSub, ok := overseer.Subscribe[InitFailed](bus, "proj-failed")
	require.True(t, ok)
	defer failedSub.Unsubscribe()
	completedSub, ok := overseer.Subscribe[InitCompleted](bus, "proj-completed")
	require.True(t, ok)
	defer completedSub.Unsubscribe()

	target := InitTarget{ContainerID: "c-proj-1234567890ab", AgentName: "dev", Project: "clawker"}
	exec, errExec := NewExecutor(bus, logger.Nop())
	require.NoError(t, errExec)

	// Cycle 1: fail at step "config" (idx 1).
	{
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stream := newFakeStream(ctx)
		done := make(chan struct{})
		idx := 0
		go func() {
			defer close(done)
			for cmd := range stream.sent {
				if _, isClose := cmd.Payload.(*clawkerdv1.Command_CloseStdin); isClose {
					continue
				}
				exit := int32(0)
				if idx == 1 {
					exit = 7
				}
				stream.recvCh <- recvFrame{resp: &clawkerdv1.Response{
					CommandId: cmd.CommandId,
					Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: exit}},
				}}
				idx++
			}
		}()
		err := exec.Run(ctx, stream, target)
		close(stream.sent)
		<-done
		require.Error(t, err)
	}

	select {
	case <-failedSub.C:
	case <-time.After(time.Second):
		t.Fatal("InitFailed did not drain")
	}
	snap1, ok := bus.Snapshot(context.Background())
	require.True(t, ok)
	view1 := snap1.Agents[target.ContainerID]
	assert.Equal(t, overseer.InitStatusFailed, view1.Init.Status)
	assert.NotEmpty(t, view1.Init.LastError, "Failed cycle must populate Init.LastError")

	// Cycle 2: full success.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stream := newFakeStream(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for cmd := range stream.sent {
				if _, isClose := cmd.Payload.(*clawkerdv1.Command_CloseStdin); isClose {
					continue
				}
				stream.recvCh <- recvFrame{resp: &clawkerdv1.Response{
					CommandId: cmd.CommandId,
					Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 0}},
				}}
			}
		}()
		err := exec.Run(ctx, stream, target)
		close(stream.sent)
		<-done
		require.NoError(t, err)
	}

	select {
	case <-completedSub.C:
	case <-time.After(time.Second):
		t.Fatal("InitCompleted did not drain")
	}
	snap2, ok := bus.Snapshot(context.Background())
	require.True(t, ok)
	view2 := snap2.Agents[target.ContainerID]
	assert.Equal(t, overseer.InitStatusCompleted, view2.Init.Status)
	assert.Empty(t, view2.Init.LastError, "Completed cycle must clear stale Init.LastError")
	assert.Equal(t, len(expectedInitStepNames), view2.Init.StepCount)
}

// TestExecutor_Run_CloseStdinFollowsEveryShellStep pins the
// CloseStdin contract: every shell step's ShellCommand is followed by
// exactly one CloseStdin frame; AgentReady (last step) is NOT. A
// regression that drops the CloseStdin Send re-introduces the 120s
// init-step hang the gate exists to fix — and the HappyPath feeder
// SKIPS CloseStdin frames, so without this assertion the regression
// would pass green.
func TestExecutor_Run_CloseStdinFollowsEveryShellStep(t *testing.T) {
	bus := overseer.New(overseer.Options{})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := newFakeStream(ctx)
	exec, errExec := NewExecutor(bus, logger.Nop())
	require.NoError(t, errExec)
	target := InitTarget{ContainerID: "c-cs-1234567890ab", AgentName: "dev", Project: "clawker"}

	var captured []*clawkerdv1.Command
	done := make(chan struct{})
	go func() {
		defer close(done)
		for cmd := range stream.sent {
			captured = append(captured, cmd)
			if _, isClose := cmd.Payload.(*clawkerdv1.Command_CloseStdin); isClose {
				continue
			}
			stream.recvCh <- recvFrame{resp: &clawkerdv1.Response{
				CommandId: cmd.CommandId,
				Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 0}},
			}}
		}
	}()
	require.NoError(t, exec.Run(ctx, stream, target))
	close(stream.sent)
	<-done

	var shellCount, agentReadyCount, closeCount int
	for i := 0; i+1 < len(captured); i++ {
		if _, isShell := captured[i].Payload.(*clawkerdv1.Command_Shell); !isShell {
			continue
		}
		_, nextIsClose := captured[i+1].Payload.(*clawkerdv1.Command_CloseStdin)
		assert.True(t, nextIsClose,
			"Shell command at index %d must be immediately followed by CloseStdin", i)
		assert.Equal(t, captured[i].CommandId, captured[i+1].CommandId,
			"CloseStdin command_id must match the preceding Shell")
	}
	for _, cmd := range captured {
		switch cmd.Payload.(type) {
		case *clawkerdv1.Command_Shell:
			shellCount++
		case *clawkerdv1.Command_AgentReady:
			agentReadyCount++
		case *clawkerdv1.Command_CloseStdin:
			closeCount++
		}
	}
	assert.Equal(t, 6, shellCount, "expected 6 shell steps in the static plan")
	assert.Equal(t, 1, agentReadyCount, "expected exactly one AgentReady step")
	assert.Equal(t, shellCount, closeCount,
		"every shell step needs exactly one CloseStdin (none for AgentReady)")
}

// TestRunInit_NoExecutorWired pins the misconfiguration warning
// behavior: a Dialer without Executor wired logs a warning event and
// skips (does not panic, does not block). The warn log is the
// operator-facing signal so a regression that drops NewExecutor
// wiring is observable as a structured event, not a silent hang.
func TestRunInit_NoExecutorWired(t *testing.T) {
	var buf bytes.Buffer
	stepLog := logger.NewWriter(&buf)
	d := &Dialer{log: logger.Nop()}
	d.runInit(context.Background(), "c-1", establishResult{}, stepLog)
	assert.Contains(t, buf.String(), "agent_init_executor_unset",
		"missing-executor path must emit the diagnostic event so operators can grep for it")
}
