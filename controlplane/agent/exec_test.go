package agent_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	moby "github.com/moby/moby/client"
	clawkerdv1mocks "github.com/schmitthub/clawker/api/clawkerd/v1/mocks"
	"github.com/schmitthub/clawker/controlplane/agent"
	agentmocks "github.com/schmitthub/clawker/controlplane/agent/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	dockermocks "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// selfExitDocker returns a *docker.Client whose ContainerWait reports the
// container already not-running, so killAfterGrace takes the self-exit path
// and never issues a SIGKILL. Used by Run tests that drive a fatal Step (the
// Step failure — not container teardown — is what they assert).
func selfExitDocker(t *testing.T) *docker.Client {
	t.Helper()
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerWait(0)
	fake.SetupContainerKill()
	return fake.Client
}

// initStepNames pins the static InitPlan to the load-bearing vocabulary
// subscribers (CLI WatchAgent, monitoring, log greppers) match against.
// Reordering or renaming any of these is a CP-side breaking change and
// must be paired with subscriber updates.
var initStepNames = []string{
	"docker-socket",
	"config",
	"git",
	"git-credentials",
	"ssh",
	"post-init",
	"agent-initialized",
}

// mustExecutor constructs an Executor wired to topic + a self-exit docker
// fake, failing the test on construction error.
func mustExecutor(t *testing.T, rec **agentmocks.AgentRecorder) *agent.Executor {
	t.Helper()
	topic := agentmocks.NewAgentTopic(t)
	if rec != nil {
		*rec = agentmocks.RecordAgent(topic)
	}
	e, err := agent.NewExecutor(topic, selfExitDocker(t), logger.Nop())
	require.NoError(t, err)
	return e
}

// panickingSendStream wraps FakeSessionStream and panics on the Nth Send.
// Used by TestExecutor_Run_PanicInStep to exercise the recover defer in
// Executor.Run.
type panickingSendStream struct {
	*clawkerdv1mocks.FakeSessionStream
	panicAtSend int
	sendCount   int
}

func (p *panickingSendStream) Send(c *clawkerdv1.Command) error {
	p.sendCount++
	if p.sendCount == p.panicAtSend {
		panic("synthetic Send panic for Executor.Run recover test")
	}
	return p.FakeSessionStream.Send(c)
}

// TestExecutor_Run_PanicInStep verifies the recover defer at the top of
// Executor.Run. A panic mid-step (here: Send panics on the first frame of
// the first step) must:
//  1. NOT propagate up to the caller (would crash CP and strand eBPF).
//  2. Convert to an error return so the dialer's log+continue path fires.
//  3. Publish a synthetic step_failed AgentEvent for the in-flight step.
//  4. Publish a synthetic exec_failed AgentEvent so the exec lifecycle
//     transitions out of Running.
//  5. NOT publish a completed AgentEvent.
func TestExecutor_Run_PanicInStep(t *testing.T) {
	tests := map[string]struct {
		plan  []agent.Step
		label string
	}{
		"boot plan": {plan: agent.BootPlan, label: "boot"},
		"init plan": {plan: agent.InitPlan, label: "init"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var rec *agentmocks.AgentRecorder
			e := mustExecutor(t, &rec)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			fake := clawkerdv1mocks.NewFakeSessionStream(ctx)
			stream := &panickingSendStream{FakeSessionStream: fake, panicAtSend: 1}

			target := agent.ExecTarget{ContainerID: "c-panic-1234567890ab", AgentName: "dev", Project: "clawker"}

			// The panic must NOT propagate (which would crash CP); the
			// recover converts it to an ordinary error return so the
			// dialer's log+continue path fires and the Session stays open.
			err := e.Run(ctx, stream, target, test.plan, test.label)
			require.Error(t, err, "panic must convert to an error return, not propagate")
			assert.Contains(t, err.Error(), "panicked")

			require.Eventually(t, func() bool { return len(rec.WithAction(agent.ExecutorEventType, agent.ActionExecFailed)) == 1 }, time.Second, 10*time.Millisecond)
			assert.Empty(t, rec.WithAction(agent.ExecutorEventType, agent.ActionExecCompleted), "completed must NOT fire on panic")

			failed := rec.WithAction(agent.ExecutorEventType, agent.ActionExecFailed)
			require.Len(t, failed, 1)
			assert.Equal(t, agent.ReasonUnknown, failed[0].Message.Reason,
				"panic classifies as Unknown — distinct from Transport/ExitCode")
			assert.Contains(t, failed[0].Message.Detail, "panicked")

			stepFailed := rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepFailed)
			require.Len(t, stepFailed, 1, "synthetic step_failed must fire for the in-flight step (currentIdx=0)")
			assert.Equal(t, test.plan[0].StepName(), stepFailed[0].Message.StepName,
				"in-flight step name must be the first step (panic on first Send before any step completed)")
			assert.Equal(t, 0, stepFailed[0].Message.StepIndex)
		})
	}
}

// TestNewExecutor_NilTopicReturnsError pins the constructor-time rejection
// of a nil topic. Returning an error (vs panicking) is load-bearing for CP
// resilience: a wiring bug must surface as a structured log line on the
// project's normal log surface, not crash CP and strand the trace on
// os.Stderr where only `docker logs <cp>` sees it.
func TestNewExecutor_NilTopicReturnsError(t *testing.T) {
	exec, err := agent.NewExecutor(nil, nil, logger.Nop())
	require.Error(t, err, "agent.NewExecutor must reject nil topic")
	assert.Nil(t, exec)
	assert.Contains(t, err.Error(), "topic is required")
}

// TestInitPlan_PrivilegeAndShape pins three load-bearing plan invariants:
//
//  1. docker-socket is the SOLE uid=0 step (privilege-drop contract).
//  2. agent-initialized is the LAST step.
//  3. ssh's InitialStdin is non-empty (the host-key blob known_hosts
//     consumes; an empty payload silently produces an empty file).
func TestInitPlan_PrivilegeAndShape(t *testing.T) {
	require.NotEmpty(t, agent.InitPlan)

	var rootSteps []string
	for _, st := range agent.InitPlan {
		s, ok := st.(agent.ShellStep)
		if !ok {
			continue
		}
		if s.Shell.Stages[0].Uid == 0 {
			rootSteps = append(rootSteps, s.Name)
		}
	}
	assert.Equal(t, []string{"docker-socket"}, rootSteps, "only docker-socket may run as root")

	last := agent.InitPlan[len(agent.InitPlan)-1]
	_, isInitialized := last.(agent.AgentInitializedStep)
	assert.True(t, isInitialized, "agent-initialized must be terminal (no step may follow)")

	for _, st := range agent.InitPlan {
		if s, ok := st.(agent.ShellStep); ok && s.Name == "ssh" {
			assert.NotEmpty(t, s.Shell.InitialStdin,
				"ssh step must carry the known_hosts blob via InitialStdin")
			return
		}
	}
	t.Fatal("ssh step missing from agent.InitPlan")
}

// TestBootPlan_PreRunShape pins the boot plan's pre-run step: it runs the
// every-start pre_run hook via userStage, carries the defensive
// `[ -x … ] || exit 0` guard, and (unlike post-init) no idempotency
// marker. It must run before the terminal agent-ready (which is last so
// no step races the CMD past the entrypoint fifo release).
func TestBootPlan_PreRunShape(t *testing.T) {
	idxPreRun, idxReady := -1, -1
	for i, st := range agent.BootPlan {
		switch s := st.(type) {
		case agent.ShellStep:
			if s.Name == consts.HookPreRun {
				idxPreRun = i
				require.Len(t, s.Shell.Stages, 1)
				assert.Equal(t, []string{"sh", "-c", agent.PreRunScript}, s.Shell.Stages[0].Argv,
					"pre-run must run agent.PreRunScript via userStage")
				assert.Contains(t, agent.PreRunScript, "|| exit 0", "pre-run guard net must be present")
				assert.NotContains(t, agent.PreRunScript, "post-initialized",
					"pre-run must carry no idempotency marker")
			}
		case agent.AgentReadyStep:
			if s.Name == "agent-ready" {
				idxReady = i
			}
		}
	}
	require.NotEqual(t, -1, idxPreRun, "pre-run must be present in the boot plan")
	require.NotEqual(t, -1, idxReady, "agent-ready must be present in the boot plan")
	// The boot tail is fixed: pre-run second-to-last, agent-ready last. New
	// steps prepend to the head; this pair must stay terminal, in this order
	// (mirrors agent.BootPlanPost). Pinning both indices catches a reorder of the
	// pair or any step wedged between them.
	assert.Equal(t, len(agent.BootPlan)-1, idxReady, "agent-ready must be the terminal step")
	assert.Equal(t, len(agent.BootPlan)-2, idxPreRun, "pre-run must be the second-to-last step (immediately before agent-ready)")
}

// TestPreRunScript_GuardSemantics executes agent.PreRunScript the same way the
// boot plan does (sh -c, as userStage runs it) against a real filesystem.
// It covers the three behaviors the string assertions cannot: absent file →
// no-op exit 0, present+success → exit 0, present+failure → the script's
// own exit code propagates (the fatal contract).
func TestPreRunScript_GuardSemantics(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash required (agent.PreRunScript's delivered wrapper uses #!/bin/bash)")
	}

	run := func(t *testing.T, body string, present bool) int {
		t.Helper()
		home := t.TempDir()
		if present {
			dir := filepath.Join(home, ".clawker")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			// Mirror PrepareHookTar's wrapper: #!/bin/bash + set -e + body.
			script := "#!/bin/bash\nset -e\n" + body
			require.NoError(t, os.WriteFile(filepath.Join(dir, "pre-run.sh"), []byte(script), 0o755))
		}
		cmd := exec.Command("sh", "-c", agent.PreRunScript)
		cmd.Env = append(os.Environ(), "HOME="+home)
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			require.ErrorAs(t, err, &ee)
			return ee.ExitCode()
		}
		return 0
	}

	t.Run("absent file no-ops with exit 0", func(t *testing.T) {
		assert.Equal(t, 0, run(t, "", false))
	})
	t.Run("present success exits 0", func(t *testing.T) {
		assert.Equal(t, 0, run(t, "echo hi\n", true))
	})
	t.Run("present failure propagates exit code", func(t *testing.T) {
		assert.Equal(t, 7, run(t, "exit 7\n", true))
	})
}

// TestExecutor_Run_HappyPath drives the full agent.InitPlan with Done{0} on every
// step and verifies the AgentEvent sequence subscribers see: one started,
// N×(step_started, step_completed), one completed; no failures.
func TestExecutor_Run_HappyPath(t *testing.T) {
	var rec *agentmocks.AgentRecorder
	e := mustExecutor(t, &rec)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
	target := agent.ExecTarget{ContainerID: "c-happy-1234567890ab", AgentName: "dev", Project: "clawker"}

	doneFeeder := stream.FeedDone()
	err := e.Run(ctx, stream, target, agent.InitPlan, "init")
	stream.CloseSend()
	<-doneFeeder
	require.NoError(t, err)

	require.Eventually(t, func() bool { return len(rec.WithAction(agent.ExecutorEventType, agent.ActionExecCompleted)) == 1 }, time.Second, 10*time.Millisecond)

	started := rec.WithAction(agent.ExecutorEventType, agent.ActionExecStarted)
	require.Len(t, started, 1, "exactly one started")
	assert.Equal(t, len(initStepNames), started[0].Message.StepCount)

	completed := rec.WithAction(agent.ExecutorEventType, agent.ActionExecCompleted)
	require.Len(t, completed, 1, "exactly one completed")
	assert.Equal(t, target.ContainerID, completed[0].Agent.ContainerID)

	assert.Empty(t, rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepFailed), "no step failures expected")
	assert.Empty(t, rec.WithAction(agent.ExecutorEventType, agent.ActionExecFailed), "no terminal exec_failed expected")

	stepStarted := rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepStarted)
	stepCompleted := rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepCompleted)
	require.Len(t, stepStarted, len(initStepNames))
	require.Len(t, stepCompleted, len(initStepNames))
	for i, name := range initStepNames {
		assert.Equal(t, name, stepStarted[i].Message.StepName, "stepStarted[%d]", i)
		assert.Equal(t, i, stepStarted[i].Message.StepIndex)
		assert.Equal(t, name, stepCompleted[i].Message.StepName, "stepCompleted[%d]", i)
		assert.Equal(t, int32(0), stepCompleted[i].Message.ExitCode)
	}
}

// TestExecutor_Run_StepFailureHaltsAndPublishesFailed feeds Done{0} for the
// first N-1 steps and Done{exit=2,output="boom"} for step N. Run must halt
// at step N, publish step_failed{step:N} + exec_failed{step:N}, return a
// non-nil error, and NOT publish completed.
func TestExecutor_Run_StepFailureHaltsAndPublishesFailed(t *testing.T) {
	const failAtIdx = 2 // "git"
	var rec *agentmocks.AgentRecorder
	e := mustExecutor(t, &rec)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
	target := agent.ExecTarget{ContainerID: "c-fail-9876543210ab", AgentName: "dev", Project: "clawker"}

	doneFeeder := stream.FeedSteps(func(idx int, cmd *clawkerdv1.Command) []*clawkerdv1.Response {
		if idx == failAtIdx {
			return []*clawkerdv1.Response{
				{CommandId: cmd.CommandId, Payload: &clawkerdv1.Response_Output{Output: &clawkerdv1.OutputChunk{Data: []byte("boom")}}},
				{CommandId: cmd.CommandId, Payload: &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 2}}},
			}
		}
		return []*clawkerdv1.Response{clawkerdv1mocks.DoneResp(cmd.CommandId, 0)}
	})

	err := e.Run(ctx, stream, target, agent.InitPlan, "init")
	stream.CloseSend()
	<-doneFeeder
	require.Error(t, err)
	assert.Contains(t, err.Error(), initStepNames[failAtIdx])

	require.Eventually(t, func() bool { return len(rec.WithAction(agent.ExecutorEventType, agent.ActionExecFailed)) == 1 }, time.Second, 10*time.Millisecond)

	stepFailed := rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepFailed)
	failed := rec.WithAction(agent.ExecutorEventType, agent.ActionExecFailed)
	require.Len(t, stepFailed, 1)
	require.Len(t, failed, 1)
	assert.Empty(t, rec.WithAction(agent.ExecutorEventType, agent.ActionExecCompleted), "completed must NOT fire on failure")
	assert.Equal(t, initStepNames[failAtIdx], stepFailed[0].Message.StepName)
	assert.Equal(t, int32(2), stepFailed[0].Message.ExitCode)
	assert.Equal(t, agent.ReasonExitCode, stepFailed[0].Message.Reason)
	assert.Contains(t, stepFailed[0].Message.Detail, "boom")
	assert.Equal(t, initStepNames[failAtIdx], failed[0].Message.StepName)
	assert.Equal(t, agent.ReasonExitCode, failed[0].Message.Reason)

	// Steps after the failure must not have started.
	require.Len(t, rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepStarted), failAtIdx+1, "step dispatch must halt at first failure")
	require.Len(t, rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepCompleted), failAtIdx, "no completed event for the failing step")
}

// TestExecutor_Run_TransportError covers the case where stream.Recv returns
// a non-EOF error — the stream is broken; Run returns an error and
// publishes exec_failed with agent.ReasonTransportError but nothing later.
func TestExecutor_Run_TransportError(t *testing.T) {
	var rec *agentmocks.AgentRecorder
	e := mustExecutor(t, &rec)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
	target := agent.ExecTarget{ContainerID: "c-transport-1234567890ab", AgentName: "dev", Project: "clawker"}

	// Push an error frame that Run will see on its first Recv.
	go func() {
		stream.PushRecvError(errors.New("rpc connection reset"))
	}()

	err := e.Run(ctx, stream, target, agent.InitPlan, "init")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpc connection reset")

	require.Eventually(t, func() bool { return len(rec.WithAction(agent.ExecutorEventType, agent.ActionExecFailed)) == 1 }, time.Second, 10*time.Millisecond)
	assert.Len(t, rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepFailed), 1,
		"transport failure must carry a step-level event so subscribers see WHICH step was in flight")
	assert.Empty(t, rec.WithAction(agent.ExecutorEventType, agent.ActionExecCompleted))
	failed := rec.WithAction(agent.ExecutorEventType, agent.ActionExecFailed)
	assert.Equal(t, agent.ReasonTransportError, failed[0].Message.Reason)
	assert.Contains(t, failed[0].Message.Detail, "rpc connection reset")
}

// TestExecutor_Run_StreamErrorResponse pins the typed-classification
// surface for clawkerd-side ErrorCodes. Without explicit coverage, a
// regression in classifyErrorCode (e.g. dropping the SPAWN_FAILED case)
// would surface as agent.ReasonUnknown on operator dashboards with no
// compile-time signal.
func TestExecutor_Run_StreamErrorResponse(t *testing.T) {
	cases := []struct {
		name       string
		code       clawkerdv1.ErrorCode
		wantReason agent.Reason
	}{
		{"timeout", clawkerdv1.ErrorCode_ERROR_CODE_TIMEOUT, agent.ReasonTimeout},
		{"spawn_failed", clawkerdv1.ErrorCode_ERROR_CODE_SPAWN_FAILED, agent.ReasonSpawnFailed},
		{"io_error", clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR, agent.ReasonIOError},
		{"not_found", clawkerdv1.ErrorCode_ERROR_CODE_NOT_FOUND, agent.ReasonIOError},
		{"invalid_request", clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST, agent.ReasonProtocolError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec *agentmocks.AgentRecorder
			e := mustExecutor(t, &rec)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
			target := agent.ExecTarget{ContainerID: "c-err-1234567890ab", AgentName: "dev", Project: "clawker"}

			done := stream.FeedSteps(func(_ int, cmd *clawkerdv1.Command) []*clawkerdv1.Response {
				return []*clawkerdv1.Response{{
					CommandId: cmd.CommandId,
					Payload: &clawkerdv1.Response_Error{Error: &clawkerdv1.Error{
						Code:    tc.code,
						Message: "synthetic " + tc.name,
					}},
				}}
			})

			err := e.Run(ctx, stream, target, agent.InitPlan, "init")
			stream.CloseSend()
			<-done
			require.Error(t, err)

			require.Eventually(t, func() bool { return len(rec.WithAction(agent.ExecutorEventType, agent.ActionExecFailed)) == 1 }, time.Second, 10*time.Millisecond)
			stepFailed := rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepFailed)
			failed := rec.WithAction(agent.ExecutorEventType, agent.ActionExecFailed)
			require.Len(t, stepFailed, 1)
			assert.Equal(t, tc.wantReason, stepFailed[0].Message.Reason)
			assert.Equal(t, tc.wantReason, failed[0].Message.Reason)
			assert.Contains(t, failed[0].Message.Detail, tc.code.String())
			assert.Contains(t, failed[0].Message.Detail, "synthetic "+tc.name)
		})
	}
}

// TestExecutor_Run_StateProjection drives Run twice (first with a failure,
// then a success) and asserts the agent domain's AgentStore exec axis
// reflects every transition: zero → Running → Failed → Running →
// Completed, with the exec LastError clearing on the success cycle. The
// projection contract is what subscribers (CLI WatchAgent, monitoring)
// consume — a projection regression here would silently break
// operator-facing UX.
func TestExecutor_Run_StateProjection(t *testing.T) {
	topic := agentmocks.NewAgentTopic(t)
	store := agent.NewAgentStore()
	store.Subscribe(topic)
	rec := agentmocks.RecordAgent(topic) // ordering anchor for the terminal events

	target := agent.ExecTarget{ContainerID: "c-proj-1234567890ab", AgentName: "dev", Project: "clawker"}
	e, err := agent.NewExecutor(topic, selfExitDocker(t), logger.Nop())
	require.NoError(t, err)

	// Cycle 1: fail at step "config" (idx 1).
	{
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
		done := stream.FeedSteps(func(idx int, cmd *clawkerdv1.Command) []*clawkerdv1.Response {
			exit := int32(0)
			if idx == 1 {
				exit = 7
			}
			return []*clawkerdv1.Response{{
				CommandId: cmd.CommandId,
				Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: exit}},
			}}
		})
		err := e.Run(ctx, stream, target, agent.InitPlan, "init")
		stream.CloseSend()
		<-done
		cancel()
		require.Error(t, err)
	}

	require.Eventually(t, func() bool { return len(rec.WithAction(agent.ExecutorEventType, agent.ActionExecFailed)) == 1 }, 2*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		v, ok := store.Get(target.ContainerID)
		return ok && v.Executor.Status() == agent.StatusFailed
	}, 2*time.Second, 10*time.Millisecond)
	view1, _ := store.Get(target.ContainerID)
	assert.NotEmpty(t, view1.Executor.LastError(), "Failed cycle must populate exec LastError")
	assert.Equal(t, initStepNames[1], view1.Executor.StepName(),
		"Failed cycle must keep the in-flight StepName from the step_started projection")

	// Cycle 2: full success.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
		doneFeeder := stream.FeedDone()
		err := e.Run(ctx, stream, target, agent.InitPlan, "init")
		stream.CloseSend()
		<-doneFeeder
		cancel()
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		v, ok := store.Get(target.ContainerID)
		return ok && v.Executor.Status() == agent.StatusCompleted
	}, 2*time.Second, 10*time.Millisecond)
	view2, _ := store.Get(target.ContainerID)
	assert.Empty(t, view2.Executor.LastError(), "Completed cycle must clear stale exec LastError")
	assert.Equal(t, len(initStepNames), view2.Executor.StepCount())
}

// TestExecutor_Run_CloseStdinFollowsEveryShellStep pins the CloseStdin
// contract: every shell step's ShellCommand is followed by exactly one
// CloseStdin frame; agent-ready (last step of boot) is NOT. A regression
// that drops the CloseStdin Send re-introduces the init-step hang the gate
// exists to fix.
func TestExecutor_Run_CloseStdinFollowsEveryShellStep(t *testing.T) {
	e := mustExecutor(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
	target := agent.ExecTarget{ContainerID: "c-cs-1234567890ab", AgentName: "dev", Project: "clawker"}

	done := stream.FeedDone()
	require.NoError(t, e.Run(ctx, stream, target, agent.BootPlan, "boot"))
	stream.CloseSend()
	<-done
	captured := stream.SentCommands()

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
	assert.Equal(t, 2, shellCount, "expected 2 shell steps in the static boot plan (pre-run, docker-socket)")
	assert.Equal(t, 1, agentReadyCount, "expected exactly one AgentReady step")
	assert.Equal(t, shellCount, closeCount,
		"every shell step needs exactly one CloseStdin (none for AgentReady)")
}

// TestExecutor_Run_ParallelStreamsBothComplete pins the requirement that a
// single CP-owned Executor must dispatch the plan in parallel across
// containers. CP boot with multiple already-running agents fans out one
// DialAgent goroutine per container; the Executor must be safe to share.
func TestExecutor_Run_ParallelStreamsBothComplete(t *testing.T) {
	e := mustExecutor(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	run := func(containerID string) error {
		stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
		feederDone := stream.FeedDone()
		err := e.Run(ctx, stream, agent.ExecTarget{ContainerID: containerID, AgentName: "dev", Project: "clawker"}, agent.InitPlan, "init")
		stream.CloseSend()
		<-feederDone
		return err
	}

	results := make(chan error, 2)
	go func() { results <- run("c-par-aaaaaaaaaaaa") }()
	go func() { results <- run("c-par-bbbbbbbbbbbb") }()

	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			require.NoError(t, err,
				"both parallel Runs must succeed — Executor must be safe to share across containers")
		case <-time.After(8 * time.Second):
			t.Fatal("parallel Runs did not both complete within timeout")
		}
	}
}

// TestExecutor_Run_PlanIdempotent drives Run twice on the same Executor +
// same target with full success on both cycles. The idempotency contract is
// what allows Session reconnects to re-run the plan without per-container
// "already done" tracking.
func TestExecutor_Run_PlanIdempotent(t *testing.T) {
	topic := agentmocks.NewAgentTopic(t)
	store := agent.NewAgentStore()
	store.Subscribe(topic)
	e, err := agent.NewExecutor(topic, selfExitDocker(t), logger.Nop())
	require.NoError(t, err)
	target := agent.ExecTarget{ContainerID: "c-idem-1234567890ab", AgentName: "dev", Project: "clawker"}

	for cycle := 0; cycle < 2; cycle++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
		feederDone := stream.FeedDone()

		require.NoError(t, e.Run(ctx, stream, target, agent.InitPlan, "init"),
			"cycle %d must succeed — Executor must be reusable across Sessions", cycle)
		stream.CloseSend()
		<-feederDone
		cancel()

		require.Eventually(t, func() bool {
			v, ok := store.Get(target.ContainerID)
			return ok && v.Executor.Status() == agent.StatusCompleted
		}, 2*time.Second, 10*time.Millisecond, "cycle %d: worldview must reach Completed", cycle)
		view, _ := store.Get(target.ContainerID)
		assert.Empty(t, view.Executor.LastError(),
			"cycle %d: LastError must clear on success", cycle)
	}
}

// TestExecutor_Run_IgnoresUnknownAndMismatchedFrames pins the recv-loop
// noise-tolerance contract: a frame with a mismatched command_id, a
// Started/Output payload (explicit continue arms), and the real terminal
// Done must all resolve to step success without affecting outcome.
func TestExecutor_Run_IgnoresUnknownAndMismatchedFrames(t *testing.T) {
	e := mustExecutor(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
	target := agent.ExecTarget{ContainerID: "c-noise-1234567890ab", AgentName: "dev", Project: "clawker"}

	doneFeeder := stream.FeedSteps(func(_ int, cmd *clawkerdv1.Command) []*clawkerdv1.Response {
		return []*clawkerdv1.Response{
			// Mismatched command_id — runStep continues past frames that
			// don't address the in-flight command.
			{CommandId: "noise-other-command", Payload: &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 99}}},
			// Started: explicit continue arm.
			{CommandId: cmd.CommandId, Payload: &clawkerdv1.Response_Started{Started: &clawkerdv1.Started{}}},
			// Output: combined output, discarded here because the step succeeds.
			{CommandId: cmd.CommandId, Payload: &clawkerdv1.Response_Output{Output: &clawkerdv1.OutputChunk{Data: []byte("captured output")}}},
			// Real terminal frame.
			{CommandId: cmd.CommandId, Payload: &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 0}}},
		}
	})

	err := e.Run(ctx, stream, target, agent.InitPlan, "init")
	stream.CloseSend()
	<-doneFeeder
	require.NoError(t, err,
		"Run must tolerate noise frames (mismatched command_id, Started, Output) and succeed when the terminal Done lands")
}

// TestExecutor_Run_CapturesCombinedOutputInDetail proves runStep folds the
// command's combined output into the failure Detail. A regression that
// dropped output frames would leave the Detail carrying only "exit_code=N".
func TestExecutor_Run_CapturesCombinedOutputInDetail(t *testing.T) {
	const failAtIdx = 2 // "git"
	var rec *agentmocks.AgentRecorder
	e := mustExecutor(t, &rec)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := clawkerdv1mocks.NewFakeSessionStream(ctx)
	target := agent.ExecTarget{ContainerID: "c-out-1234567890ab", AgentName: "dev", Project: "clawker"}

	doneFeeder := stream.FeedSteps(func(idx int, cmd *clawkerdv1.Command) []*clawkerdv1.Response {
		if idx == failAtIdx {
			return []*clawkerdv1.Response{
				{CommandId: cmd.CommandId, Payload: &clawkerdv1.Response_Output{Output: &clawkerdv1.OutputChunk{Data: []byte("combined-output-xyz")}}},
				{CommandId: cmd.CommandId, Payload: &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 1}}},
			}
		}
		return []*clawkerdv1.Response{clawkerdv1mocks.DoneResp(cmd.CommandId, 0)}
	})

	err := e.Run(ctx, stream, target, agent.InitPlan, "init")
	stream.CloseSend()
	<-doneFeeder
	require.Error(t, err)

	require.Eventually(t, func() bool { return len(rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepFailed)) == 1 }, time.Second, 10*time.Millisecond)
	stepFailed := rec.WithAction(agent.ExecutorEventType, agent.ActionExecStepFailed)
	assert.Contains(t, stepFailed[0].Message.Detail, "combined-output-xyz",
		"combined output must be folded into the failure detail")
	assert.Contains(t, stepFailed[0].Message.Detail, "output:",
		"detail must label the captured combined output")
}

// TestInitPlan_ShellStepsCarryFlags pins the per-step flag policy the init
// plan configures: every shell step sets exit_on_non_zero (a non-zero exit
// halts the plan), and only the user-authored hook (post-init) sets
// print_output. clawker's own plumbing steps stay quiet on success.
func TestInitPlan_ShellStepsCarryFlags(t *testing.T) {
	var sawShellStep bool
	for _, st := range agent.InitPlan {
		sh, ok := st.(agent.ShellStep)
		if !ok {
			continue // agent-initialized is not a ShellCommand
		}
		sawShellStep = true
		cmd, follow := sh.Command("init-test-" + sh.Name)
		require.True(t, follow)
		shell := cmd.GetShell()
		require.NotNil(t, shell, "step %q must carry a ShellCommand", sh.Name)

		assert.True(t, shell.GetExitOnNonZero(),
			"every shell init step is fatal → exit_on_non_zero must be set (step %q)", sh.Name)

		wantPrint := sh.Name == consts.HookPostInit
		assert.Equal(t, wantPrint, shell.GetPrintOutput(),
			"print_output must be set only for the user hook (step %q)", sh.Name)
	}
	require.True(t, sawShellStep, "agent.InitPlan must contain shell steps")
}

// TestKillAfterGrace_SelfExit: a container that reports not-running within
// the grace is a clean self-exit — killAfterGrace returns nil and never
// issues a SIGKILL.
func TestKillAfterGrace_SelfExit(t *testing.T) {
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerWait(0) // already not-running
	fake.SetupContainerKill()
	e, err := agent.NewExecutor(agentmocks.NewAgentTopic(t), fake.Client, logger.Nop())
	require.NoError(t, err)

	require.NoError(t, e.KillAfterGrace(context.Background(), "c-selfexit-1234567890ab", logger.Nop()))
	fake.AssertNotCalled(t, "ContainerKill")
}

// TestKillAfterGrace_BackstopSIGKILL: when ContainerWait cannot confirm a
// self-exit, killAfterGrace escalates to SIGKILL and reports success once
// the kill lands.
func TestKillAfterGrace_BackstopSIGKILL(t *testing.T) {
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.FakeAPI.ContainerWaitFn = func(_ context.Context, _ string, _ moby.ContainerWaitOptions) moby.ContainerWaitResult {
		errCh := make(chan error, 1)
		errCh <- errors.New("wait stream broke")
		return moby.ContainerWaitResult{Error: errCh}
	}
	fake.SetupContainerKill()
	e, err := agent.NewExecutor(agentmocks.NewAgentTopic(t), fake.Client, logger.Nop())
	require.NoError(t, err)

	require.NoError(t, e.KillAfterGrace(context.Background(), "c-wedged-1234567890ab", logger.Nop()))
	fake.AssertCalled(t, "ContainerKill")
}

// TestKillAfterGrace_ShutdownStillKillsOnLiveCtx: a CP shutdown cancels the
// parent ctx mid-grace. killAfterGrace must still SIGKILL the doomed
// container — and must issue that kill on a live (Background) context, not
// the cancelled parent, or the moby client rejects it before it reaches the
// daemon and the container leaks.
func TestKillAfterGrace_ShutdownStillKillsOnLiveCtx(t *testing.T) {
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	// ContainerWait never resolves, so the only ready select arm is the
	// cancelled parent ctx (the shutdown).
	fake.FakeAPI.ContainerWaitFn = func(_ context.Context, _ string, _ moby.ContainerWaitOptions) moby.ContainerWaitResult {
		return moby.ContainerWaitResult{} // nil channels — Result/Error never fire
	}
	var killCtxLive bool
	fake.FakeAPI.ContainerKillFn = func(ctx context.Context, _ string, _ moby.ContainerKillOptions) (moby.ContainerKillResult, error) {
		killCtxLive = ctx.Err() == nil
		return moby.ContainerKillResult{}, nil
	}
	e, err := agent.NewExecutor(agentmocks.NewAgentTopic(t), fake.Client, logger.Nop())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // CP shutdown already happened

	require.NoError(t, e.KillAfterGrace(ctx, "c-shutdown-1234567890ab", logger.Nop()))
	fake.AssertCalled(t, "ContainerKill")
	assert.True(t, killCtxLive,
		"SIGKILL must run on a live ctx (Background), not the cancelled parent")
}
