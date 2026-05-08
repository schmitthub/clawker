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
	"github.com/schmitthub/clawker/internal/consts"
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

// TestExecutor_Plan_UidGid_RootForDockerSocket_UserForRest pins the
// privilege drop contract: only docker-socket runs as root; every
// user-scoped step targets consts.ContainerUID/ContainerGID. Drift
// here is a privilege-escalation or privilege-drop regression
// depending on direction. HOME/USER must be set in env on every
// user-scoped step (setuid does not update env on its own; without
// these the script would write to clawkerd's HOME=/root and fail).
func TestExecutor_Plan_UidGid_RootForDockerSocket_UserForRest(t *testing.T) {
	wantUID := uint32(consts.ContainerUID)
	wantGID := uint32(consts.ContainerGID)
	wantHome := "/home/" + consts.ContainerUser
	e := NewExecutor(nil, logger.Nop())
	for _, st := range e.plan() {
		if st.Kind == stepKindAgentReady {
			continue
		}
		stage := st.Shell.Stages[0]
		switch st.Name {
		case "docker-socket":
			assert.Equal(t, uint32(0), stage.Uid, "docker-socket must run as root to chgrp")
			assert.Equal(t, uint32(0), stage.Gid, "docker-socket must run as root to chgrp")
		default:
			assert.Equal(t, wantUID, stage.Uid, "step %q uid", st.Name)
			assert.Equal(t, wantGID, stage.Gid, "step %q gid", st.Name)
			assert.Equal(t, wantHome, stage.Env["HOME"], "step %q HOME", st.Name)
			assert.Equal(t, consts.ContainerUser, stage.Env["USER"], "step %q USER", st.Name)
		}
	}
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
	exec := NewExecutor(bus, logger.Nop())
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
	exec := NewExecutor(bus, logger.Nop())
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
	assert.Contains(t, events.stepFailed[0].Reason, "boom")
	assert.Equal(t, expectedInitStepNames[failAtIdx], events.failed[0].FailedStep)

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
	exec := NewExecutor(bus, logger.Nop())
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
	require.Len(t, events.failed, 1)
	assert.Empty(t, events.completed)
	assert.Contains(t, events.failed[0].Reason, "rpc connection reset")
}

// TestRunInit_NoExecutorWired pins the misconfiguration warning
// behavior: a Dialer without Executor wired logs a warning event and
// skips (does not panic, does not block). The warn log is the
// operator-facing signal — without it, an upgrade that drops the
// agent.NewExecutor wiring would show up as silent container hangs
// at the entrypoint fifo with no diagnostic breadcrumb.
func TestRunInit_NoExecutorWired(t *testing.T) {
	var buf bytes.Buffer
	stepLog := logger.NewWriter(&buf)
	d := &Dialer{log: logger.Nop()}
	d.runInit(context.Background(), "c-1", establishResult{}, stepLog)
	assert.Contains(t, buf.String(), "agent_init_executor_unset",
		"missing-executor path must emit the diagnostic event so operators can grep for it")
}
