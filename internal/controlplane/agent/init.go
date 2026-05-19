package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"time"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// Per-step timeout defaults. post-init can install packages and warm
// caches, hence 600s; other steps are file-IO and should complete in
// milliseconds in steady state, 30s tolerates a slow first-boot fs.
// CP's `runStep` ceiling is now the only init wall-clock gate —
// clawkerd-as-PID-1 has no separate shell-script timeout to align
// with (the legacy bash entrypoint + fifo wait was retired by the
// PID-1 cutover; see cmd/clawkerd/CLAUDE.md).
const (
	initStepTimeoutDefault  = consts.InitStepTimeoutDefaultSeconds
	initStepTimeoutPostInit = consts.InitStepTimeoutPostInitSeconds
)

// defaultKnownHosts is the openssh published host-key blob for the
// common public Git forges (github.com, gitlab.com, bitbucket.org),
// seeded into ~/.ssh/known_hosts on every init run via the ssh step's
// InitialStdin. Update if upstream rotates.
//
// Source: https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints
//
//	https://docs.gitlab.com/ee/user/gitlab_com/#ssh-host-keys-fingerprints
//	https://support.atlassian.com/bitbucket-cloud/docs/configure-ssh-and-two-step-verification/
const defaultKnownHosts = `github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=
github.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=
gitlab.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAfuCHKVTjquxvt6CM6tdG4SLp1Btn/nOeHHE5UOzRdf
gitlab.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBFSMqzJeV9rUzU4kWitGjeR4PWSa29SPqJ1fVkhtj3Hw9xjLVXVYrU9QlYWrOLXBpQ6KWjbjTDTdDkoohFzgbEY=
gitlab.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCsj2bNKTBSpIYDEGk9KxsGh3mySTRgMtXL583qmBpzeQ+jqCMRgBqB98u3z++J1sKlXHWfM9dyhSevkMwSbhoR8XIq/U0tCNyokEi/ueaBMCvbcTHhO7FcwzY92WK4Ik8Y0iQ7F3awE8ntZELLwOvLYjzo3yl7hGRM9aLhHaVCF8bCG7cJTbplCCVSLKcQzQasPAOmPTmCC/NfZqrT0cIQ2rWM8Q1xI/z3THw1h19WSSqLBgNmz8M+Z7oqlABp7UMlP8W5K5RUCTASg9K7hNg7Jy3gmr3G6V+/FFHDB5PASg8q2g9ByCVWDqt1r8I5NxpqhUJ47RCrKE01zEIyc9z
bitbucket.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIazEu89wgQZ4bqs3d63QSMzYVa0MuJ2e2gKTKqu+UUO
bitbucket.org ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBPIQmuzMBuKdWeF4+a2sjSSpBK0iqitSQ+5BM9KhpexuGt20JpTVM7u5BDZngncgrqDMbWdxMWWOGtZ9UgbqgZE=
bitbucket.org ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDQeJzhupRu0u0cdegZIa8e86EG2qOCsIsD1Xw0xSeiPDlCr7kq97NLmMbpKTX6Esc30NuoqEEHCuc7yWtwp8dI76EEEB1VqY9QJq6vk+aySyboD5QF61I/1WeTwu+deCbgKMGbUijeXhtfbxSxm6JwGrXrhBdofTsbKRUsrN1WoNgUa8uqN1Vx6WAJw1JHPhglEGGHea6QICwJOAr/6mrui/oB7pkaWKHj3z7d1IC4KWLtY47elvjbaTlkN04Kc/5LFEirorGYVbt15kAUlqGM65pk6ZBxtaO3+30LVlORZkxOh+LKL/BvbZ/iRNhItLqNyieoQj/uj/4PXhq0r2tVoBqXJCmLk7k+zpcaoprJBFQDa5A7SjqPQK0pCwBvhOT0hHpF0sWH4AIQHvYAWVTD0tBFPF1yENBxnVJpfL0L2qgGxLbQCWgOG0/1ygM+Gf9n0AIksE1h/uoLERBHQXE30XuP4pHV3n+7kO5+nw5VVFIsMfrQ3oT89Si/NvvmM=
`

// Inline self-gating shell scripts. Each script's gating predicates
// ([ -d INIT_DIR ], [ -f $HOST_GITCONFIG ], CLAWKER_GIT_HTTPS, etc.)
// keep the dispatch CP-feature-flag-free: CP doesn't need to know
// which optional features a given container has wired.
const (
	configSeedScript = `INIT_DIR="$HOME/.claude-init"
CONFIG_DIR="$HOME/.claude"
[ -d "$INIT_DIR" ] || exit 0
mkdir -p "$CONFIG_DIR"
[ ! -f "$CONFIG_DIR/statusline.sh" ] && cp "$INIT_DIR/statusline.sh" "$CONFIG_DIR/statusline.sh"
if [ ! -f "$CONFIG_DIR/.config.json" ] || [ ! -s "$CONFIG_DIR/.config.json" ]; then
    cp "$INIT_DIR/.config.json" "$CONFIG_DIR/.config.json"
fi
if [ ! -f "$CONFIG_DIR/settings.json" ]; then
    cp "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json"
else
    if jq -s '.[0] * .[1]' "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json" > "$CONFIG_DIR/settings.json.tmp" 2>/dev/null; then
        mv "$CONFIG_DIR/settings.json.tmp" "$CONFIG_DIR/settings.json"
    else
        rm -f "$CONFIG_DIR/settings.json.tmp"
    fi
fi
`

	// gitconfigFilterTemplate strips [credential] sections from the
	// host-mounted gitconfig before placing it under the unprivileged
	// user's home. %q is replaced with consts.HostGitConfigStagingPath
	// (Go-quoted so the bash literal never drifts from the workspace
	// mount target).
	//
	// Three branches:
	//   - awk succeeds with non-empty output → move into place
	//   - awk succeeds with empty output (host gitconfig had ONLY
	//     [credential] blocks) → discard the empty tmp; copying the
	//     unfiltered file would leak credentials.
	//   - awk syscall failed → discard tmp and bail; same rationale.
	gitconfigFilterTemplate = `HOST_GITCONFIG=%q
[ -f "$HOST_GITCONFIG" ] || exit 0
TMP="$HOME/.gitconfig.tmp"
if awk '/^\[credential/ {in_cred=1; next} /^\[/ {in_cred=0} !in_cred {print}' "$HOST_GITCONFIG" > "$TMP" 2>/dev/null; then
    if [ -s "$TMP" ]; then
        mv "$TMP" "$HOME/.gitconfig"
    else
        rm -f "$TMP"
    fi
else
    rm -f "$TMP"
fi
`

	gitCredentialsScript = `[ -n "$CLAWKER_HOST_PROXY" ] || exit 0
[ "$CLAWKER_GIT_HTTPS" = "true" ] || exit 0
git config --global credential.helper clawker
`

	// sshKnownHostsScript reads the host blob from stdin (PipeStage's
	// initial_stdin) and appends only lines not already present.
	// Idempotent across container restarts.
	sshKnownHostsScript = `mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
KH="$HOME/.ssh/known_hosts"
touch "$KH"
chmod 600 "$KH"
while IFS= read -r line; do
    [ -z "$line" ] && continue
    if ! grep -qF -- "$line" "$KH"; then
        printf '%s\n' "$line" >> "$KH"
    fi
done
`

	postInitScript = `POST="$HOME/.clawker/post-init.sh"
DONE="$HOME/.claude/post-initialized"
[ -x "$POST" ] || exit 0
[ -f "$DONE" ] && exit 0
if "$POST"; then
    touch "$DONE"
else
    exit $?
fi
`
)

// gitconfigFilterScript is the rendered git-step body. Computed once
// at package init; %s slot carries the workspace const.
var gitconfigFilterScript = fmt.Sprintf(gitconfigFilterTemplate, consts.HostGitConfigStagingPath)

// step is one entry in the init plan. Sealed sum: shellStep or
// agentReadyStep. Adding a new step kind is a compile-time change
// (implement step) — runStep's type switch loses its runtime
// "unknown step kind" branch entirely.
type step interface {
	stepName() string
	// command builds the wire payload for this step under commandID.
	// followCloseStdin reports whether runStep should follow with a
	// CloseStdin frame (true for shell steps that don't consume
	// stdin; false for AgentReady which has no stdin pipe).
	command(commandID string) (cmd *clawkerdv1.Command, followCloseStdin bool)
	// isStep is the unexported sealing marker. A third implementer
	// outside this package is rejected at compile time; package-
	// internal additions still need a paired runStep / plan() update
	// by convention.
	isStep()
}

type shellStep struct {
	Name  string
	Shell *clawkerdv1.ShellCommand
}

func (s shellStep) stepName() string { return s.Name }
func (shellStep) isStep()            {}
func (s shellStep) command(id string) (*clawkerdv1.Command, bool) {
	return &clawkerdv1.Command{
		CommandId: id,
		Payload:   &clawkerdv1.Command_Shell{Shell: s.Shell},
	}, true
}

type agentReadyStep struct {
	Name string
}

func (s agentReadyStep) stepName() string { return s.Name }
func (agentReadyStep) isStep()            {}
func (s agentReadyStep) command(id string) (*clawkerdv1.Command, bool) {
	return &clawkerdv1.Command{
		CommandId: id,
		Payload:   &clawkerdv1.Command_AgentReady{AgentReady: &clawkerdv1.AgentReady{}},
	}, false
}

// InitTarget identifies the agent the Executor is initializing.
// Threaded through every init event so subscribers see consistent
// identity fields without re-deriving from the registry.
type InitTarget struct {
	ContainerID string
	AgentName   string
	Project     string
}

// Executor dispatches the static CP-driven init plan against an open
// Session stream. Owns Recv during Run; the dialer's drainStream
// takes over after Run returns.
//
// Plan idempotency contract: every Session establish runs the full
// plan, including reconnects after CP restart. Each step is
// idempotent (file-presence gates, append-if-missing, marker-file
// post-init), and AgentReady is no-op success when clawkerd already
// spawned the user CMD (spawnState's CAS rejects re-fork; handler
// replies Done{0}). Trade: a small volume of shell commands
// fires on every reconnect; gain: no per-container completed flag.
// Executor is shared across all containers — the dialer constructs
// one at CP boot and calls Run from a goroutine per DialAgent (one per
// agent container). Run holds no Executor-scoped mutable state: every
// call gets its own (ctx, stream, target) and drives its own stream's
// Recv loop in a single goroutine. The Run-owns-Recv invariant is
// per-stream (one Run, one stream, one Recv-driving goroutine), not
// per-Executor — concurrent Runs across different containers must not
// be serialized.
type Executor struct {
	bus *overseer.Overseer
	log *logger.Logger
}

// NewExecutor constructs an Executor. nil log is replaced with
// logger.Nop(). bus is required — Run publishes init events
// unconditionally, so a nil bus would NPE deep inside overseer.Publish
// on the first event dispatch. Returning an error lets the caller
// (cmd/clawker-cp/main.go) log the wiring bug to the structured log
// surface and degrade gracefully (initExec = nil → dialer logs
// agent_init_executor_unset per dial) instead of crashing CP and
// stranding the failure on os.Stderr where only `docker logs` sees it.
// Matches the nil-bus contract on agent.New for the dialer.
func NewExecutor(bus *overseer.Overseer, log *logger.Logger) (*Executor, error) {
	if bus == nil {
		return nil, errors.New("agent.NewExecutor: bus is required")
	}
	if log == nil {
		log = logger.Nop()
	}
	return &Executor{bus: bus, log: log}, nil
}

// Run dispatches the init plan one step at a time, awaiting Done or
// Error per command before sending the next. Publishes init events
// throughout. Returns the error that halted the run (transport
// failure, step failure surfaced as a Go error), or nil on full
// success.
//
// Caller invariant: stream must already have completed Hello/HelloAck
// and any Register handshake. Run owns stream.Recv exclusively for
// its duration — but per-stream, not per-Executor. Concurrent Runs
// across different streams (the prod case: parallel agent containers
// after a CP restart) execute in parallel.
func (e *Executor) Run(ctx context.Context, stream clawkerdv1.ClawkerdService_SessionClient, target InitTarget) (runErr error) {
	plan := e.plan()
	startedAt := time.Now()
	overseer.Publish(e.bus, InitStarted{
		ContainerID: target.ContainerID,
		AgentName:   target.AgentName,
		Project:     target.Project,
		StepCount:   len(plan),
		At:          startedAt,
	})
	log := e.log.With(
		"component", "agent.init",
		"container_id", target.ContainerID,
		"agent", target.AgentName,
		"project", target.Project,
	)
	log.Info().
		Str("event", "agent_init_started").
		Int("step_count", len(plan)).
		Msg("agent.init: dispatching plan")

	// currentIdx / currentName are written at the head of each step
	// iteration so the panic recover below can publish a synthetic
	// InitStepFailed for the in-flight step. -1 means the panic
	// happened before any step started (plan setup, log composition,
	// etc.) — only InitFailed is synthesized in that case.
	currentIdx, currentName := -1, ""
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// Return the recovered value as an error so dialer.runInit
		// hits the existing error path (Session held open per
		// asymmetric trust). Re-panicking would land in the dial
		// goroutine's outer recover and strand the Init axis at
		// Running because that recover doesn't know step state.
		now := time.Now()
		dur := now.Sub(startedAt)
		detail := fmt.Sprintf("Executor.Run panicked: %v", r)
		log.Error().
			Interface("panic", r).
			Bytes("stack", debug.Stack()).
			Str("event", "agent_init_panic").
			Int("step_index", currentIdx).
			Str("step", currentName).
			Msg("agent.init: Executor.Run panicked; publishing synthetic terminal events; Session held open for containment")
		if currentIdx >= 0 {
			overseer.Publish(e.bus, InitStepFailed{
				ContainerID: target.ContainerID,
				AgentName:   target.AgentName,
				Project:     target.Project,
				StepName:    currentName,
				StepIndex:   currentIdx,
				Duration:    dur,
				ExitCode:    -1,
				Reason:      overseer.InitFailureReasonUnknown,
				Detail:      detail,
				At:          now,
			})
		}
		overseer.Publish(e.bus, InitFailed{
			ContainerID: target.ContainerID,
			AgentName:   target.AgentName,
			Project:     target.Project,
			FailedStep:  currentName,
			Reason:      overseer.InitFailureReasonUnknown,
			Detail:      detail,
			Duration:    dur,
			At:          now,
		})
		runErr = errors.New(detail)
	}()

	for i, st := range plan {
		currentIdx = i
		currentName = st.stepName()
		stepStart := time.Now()
		overseer.Publish(e.bus, InitStepStarted{
			ContainerID: target.ContainerID,
			AgentName:   target.AgentName,
			Project:     target.Project,
			StepName:    st.stepName(),
			StepIndex:   i,
			StepCount:   len(plan),
			At:          stepStart,
		})
		log.Info().
			Str("event", "agent_init_step_started").
			Str("step", st.stepName()).
			Int("step_index", i).
			Msg("agent.init: step started")

		out, err := e.runStep(ctx, stream, target.ContainerID, i, st, log)
		dur := time.Since(stepStart)
		if out.Failed() {
			overseer.Publish(e.bus, InitStepFailed{
				ContainerID: target.ContainerID,
				AgentName:   target.AgentName,
				Project:     target.Project,
				StepName:    st.stepName(),
				StepIndex:   i,
				Duration:    dur,
				ExitCode:    out.ExitCode,
				Reason:      out.Reason,
				Detail:      out.Detail,
				At:          time.Now(),
			})
			overseer.Publish(e.bus, InitFailed{
				ContainerID: target.ContainerID,
				AgentName:   target.AgentName,
				Project:     target.Project,
				FailedStep:  st.stepName(),
				Reason:      out.Reason,
				Detail:      out.Detail,
				Duration:    time.Since(startedAt),
				At:          time.Now(),
			})
			log.Error().
				Str("event", "agent_init_failed").
				Str("step", st.stepName()).
				Int("step_index", i).
				Int32("exit_code", out.ExitCode).
				Str("reason", string(out.Reason)).
				Str("detail", out.Detail).
				Msg("agent.init: plan halted on step failure")
			if err != nil {
				return err
			}
			return fmt.Errorf("agent.init: step %q failed: %s", st.stepName(), out.Detail)
		}
		overseer.Publish(e.bus, InitStepCompleted{
			ContainerID: target.ContainerID,
			AgentName:   target.AgentName,
			Project:     target.Project,
			StepName:    st.stepName(),
			StepIndex:   i,
			Duration:    dur,
			ExitCode:    out.ExitCode,
			At:          time.Now(),
		})
		log.Info().
			Str("event", "agent_init_step_completed").
			Str("step", st.stepName()).
			Int("step_index", i).
			Dur("duration", dur).
			Msg("agent.init: step completed")
		// Reset between steps: a panic here (between iterations,
		// e.g. during defer scheduling) must not be mis-attributed
		// to the just-completed step. The recover gates synthetic
		// InitStepFailed on currentIdx >= 0; -1 means "between
		// steps — publish only InitFailed".
		currentIdx, currentName = -1, ""
	}

	totalDur := time.Since(startedAt)
	overseer.Publish(e.bus, InitCompleted{
		ContainerID: target.ContainerID,
		AgentName:   target.AgentName,
		Project:     target.Project,
		Duration:    totalDur,
		At:          time.Now(),
	})
	log.Info().
		Str("event", "agent_init_completed").
		Dur("duration", totalDur).
		Msg("agent.init: plan completed")
	return nil
}

// maxStderrCapture caps how much stderr Run preserves on the failed-
// step Detail. Truncation is marked explicitly so operators see the
// boundary instead of guessing.
const maxStderrCapture = 4096

// stepOutcome bundles the per-step result fields runStep produces.
// Zero value means the step succeeded; populated values are produced
// only via the constructors below, which keep Reason / ExitCode /
// Detail coherent. Run reads outcome.Failed() to decide whether to
// publish terminal events.
type stepOutcome struct {
	ExitCode int32
	Reason   overseer.InitFailureReason
	Detail   string
}

func (o stepOutcome) Failed() bool {
	return o.Reason != overseer.InitFailureReasonNone
}

// stepSucceeded is the zero outcome — the only success shape.
func stepSucceeded() stepOutcome { return stepOutcome{} }

// stepFailedTransport classifies any transport break (Send error,
// Recv error, ctx cancel, premature EOF). The paired transport error
// returned alongside drives Run's dispatch-halt branch; the outcome
// carries the human-readable detail for the InitStepFailed event.
func stepFailedTransport(detail string) stepOutcome {
	return stepOutcome{
		ExitCode: -1,
		Reason:   overseer.InitFailureReasonTransportError,
		Detail:   detail,
	}
}

// stepFailedExit classifies a clawkerd Done with a non-zero exit
// code. Stderr (truncated to maxStderrCapture) is folded into detail
// upstream of this constructor.
func stepFailedExit(exit int32, detail string) stepOutcome {
	return stepOutcome{
		ExitCode: exit,
		Reason:   overseer.InitFailureReasonExitCode,
		Detail:   detail,
	}
}

// stepFailedClassified classifies a clawkerd Response_Error frame
// (timeout, spawn failed, IO error, protocol violation, ...) into
// the typed reason vocabulary subscribers branch on.
func stepFailedClassified(reason overseer.InitFailureReason, detail string) stepOutcome {
	return stepOutcome{
		ExitCode: -1,
		Reason:   reason,
		Detail:   detail,
	}
}

// runStep dispatches one step's wire payload and waits for its Done
// or Error. Returns:
//   - outcome: zero value on success, populated with Reason/Detail/
//     ExitCode on step failure or transport break. Run consumes this
//     to publish the terminal InitStepFailed + InitFailed events.
//   - transport error: non-nil iff the stream is broken; the caller
//     should bail Run after publishing terminal events. A non-nil
//     err always pairs with outcome.Failed() == true (Reason ==
//     InitFailureReasonTransportError) so Run branches on a single
//     check.
//
// Bounding wait time: clawkerd enforces the per-stage timeout server-
// side (ShellCommand.TimeoutSeconds → time.AfterFunc → SIGKILL +
// ERROR_CODE_TIMEOUT response). gRPC keepalive (consts.Clawkerd*)
// breaks a wedged transport. CP-side wall-clock deadlines are
// deliberately omitted — a duplicate budget here would race the
// server-side timer and risk misclassifying a server-detected timeout
// as a client-side break.
func (e *Executor) runStep(ctx context.Context, stream clawkerdv1.ClawkerdService_SessionClient, containerID string, idx int, st step, log *logger.Logger) (stepOutcome, error) {
	commandID := buildCommandID(containerID, st.stepName(), idx)

	cmd, followCloseStdin := st.command(commandID)
	if err := stream.Send(cmd); err != nil {
		wrapped := fmt.Errorf("send %s: %w", st.stepName(), err)
		return stepFailedTransport(wrapped.Error()), wrapped
	}

	// CloseStdin invariant: clawkerd's runShellCommand publishes
	// stdinW under the registry lock at startShellCommand entry, so
	// the Send below cannot race the worker goroutine's pipe
	// registration. Without this Close, every shell step that doesn't
	// consume stdin would hang in exec.Cmd.Wait's awaitGoroutines
	// until the entrypoint timeout fires.
	if followCloseStdin {
		closeCmd := &clawkerdv1.Command{
			CommandId: commandID,
			Payload:   &clawkerdv1.Command_CloseStdin{CloseStdin: &clawkerdv1.CloseStdin{}},
		}
		if err := stream.Send(closeCmd); err != nil {
			wrapped := fmt.Errorf("send %s close_stdin: %w", st.stepName(), err)
			return stepFailedTransport(wrapped.Error()), wrapped
		}
	}

	var stderrBuf strings.Builder
	stderrTruncated := 0

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stepFailedTransport(ctxErr.Error()), ctxErr
		}
		resp, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				const eofDetail = "stream EOF before terminal response"
				return stepFailedTransport(eofDetail), errors.New(eofDetail)
			}
			wrapped := fmt.Errorf("recv %s: %w", st.stepName(), err)
			return stepFailedTransport(wrapped.Error()), wrapped
		}
		if resp.GetCommandId() != commandID {
			log.Debug().
				Str("event", "agent_init_unexpected_command_id").
				Str("got", resp.GetCommandId()).
				Str("expected", commandID).
				Str("payload_type", fmt.Sprintf("%T", resp.Payload)).
				Msg("agent.init: ignoring response with non-matching command_id")
			continue
		}
		switch p := resp.Payload.(type) {
		case *clawkerdv1.Response_Started, *clawkerdv1.Response_StageExit, *clawkerdv1.Response_Stdout:
			// Init steps run with stdout discarded; only Stderr,
			// Done, and Error feed the failure-detail/exit pipeline.
			continue
		case *clawkerdv1.Response_Stderr:
			if p.Stderr != nil {
				data := p.Stderr.GetData()
				if remaining := maxStderrCapture - stderrBuf.Len(); remaining > 0 {
					if len(data) > remaining {
						stderrTruncated += len(data) - remaining
						data = data[:remaining]
					}
					stderrBuf.Write(data)
				} else {
					stderrTruncated += len(p.Stderr.GetData())
				}
			}
		case *clawkerdv1.Response_Done:
			exit := p.Done.GetFinalExitCode()
			if exit == 0 {
				return stepSucceeded(), nil
			}
			detail := fmt.Sprintf("exit_code=%d", exit)
			if s := strings.TrimSpace(stderrBuf.String()); s != "" {
				detail += "; stderr: " + s
				if stderrTruncated > 0 {
					detail += fmt.Sprintf(" ... [%d bytes truncated]", stderrTruncated)
				}
			}
			return stepFailedExit(exit, detail), nil
		case *clawkerdv1.Response_Error:
			return stepFailedClassified(
				classifyErrorCode(p.Error.GetCode()),
				fmt.Sprintf("%s: %s", p.Error.GetCode().String(), p.Error.GetMessage()),
			), nil
		default:
			// Warn-level: an unknown payload variant means the
			// clawkerd-CP wire vocabulary has drifted. Production
			// Debug logs are typically off — operators would otherwise
			// see only the eventual server-side timeout with no hint
			// that a new payload variant slipped past the switch.
			log.Warn().
				Str("event", "agent_init_unknown_payload").
				Str("command_id", resp.GetCommandId()).
				Str("step", st.stepName()).
				Str("payload_type", fmt.Sprintf("%T", resp.Payload)).
				Msg("agent.init: ignoring unknown response payload — wire vocabulary drift")
		}
	}
}

// classifyErrorCode maps a clawkerd ErrorCode to the typed init
// failure classification. New codes default to Unknown so producers
// don't drop information silently — the human-readable detail still
// carries the ErrorCode string.
func classifyErrorCode(code clawkerdv1.ErrorCode) overseer.InitFailureReason {
	switch code {
	case clawkerdv1.ErrorCode_ERROR_CODE_TIMEOUT:
		return overseer.InitFailureReasonTimeout
	case clawkerdv1.ErrorCode_ERROR_CODE_SPAWN_FAILED:
		return overseer.InitFailureReasonSpawnFailed
	case clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR, clawkerdv1.ErrorCode_ERROR_CODE_NOT_FOUND:
		return overseer.InitFailureReasonIOError
	case clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
		clawkerdv1.ErrorCode_ERROR_CODE_UNKNOWN_COMMAND_ID:
		return overseer.InitFailureReasonProtocol
	default:
		return overseer.InitFailureReasonUnknown
	}
}

// buildCommandID composes a stable, human-debuggable command_id for
// one step dispatch. Prefix is bounded so log lines stay compact.
func buildCommandID(containerID, stepName string, idx int) string {
	prefix := containerID
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return fmt.Sprintf("init-%s-%s-%d", prefix, stepName, idx)
}

// plan builds the static init step list. Order is load-bearing:
// docker-socket runs first as the only privileged step, then user-
// scoped steps populate ~/.claude / ~/.gitconfig / ~/.ssh before
// post-init runs (which may reference any of them).
//
// AgentReady MUST be the terminal step. Any step appended after it
// would race CMD execution — the entrypoint exec's the user CMD as
// soon as AgentReady's fifo write lands.
func (e *Executor) plan() []step {
	return []step{
		shellStep{
			Name: "docker-socket",
			Shell: &clawkerdv1.ShellCommand{
				Stages: []*clawkerdv1.PipeStage{{
					Argv: []string{"sh", "-c", `[ -S /var/run/docker.sock ] && chgrp docker /var/run/docker.sock || true`},
					Uid:  0,
					Gid:  0,
				}},
				TimeoutSeconds: initStepTimeoutDefault,
			},
		},
		shellStep{
			Name: "config",
			Shell: &clawkerdv1.ShellCommand{
				Stages:         []*clawkerdv1.PipeStage{userStage(configSeedScript)},
				TimeoutSeconds: initStepTimeoutDefault,
			},
		},
		shellStep{
			Name: "git",
			Shell: &clawkerdv1.ShellCommand{
				Stages:         []*clawkerdv1.PipeStage{userStage(gitconfigFilterScript)},
				TimeoutSeconds: initStepTimeoutDefault,
			},
		},
		shellStep{
			Name: "git-credentials",
			Shell: &clawkerdv1.ShellCommand{
				Stages:         []*clawkerdv1.PipeStage{userStage(gitCredentialsScript)},
				TimeoutSeconds: initStepTimeoutDefault,
			},
		},
		shellStep{
			Name: "ssh",
			Shell: &clawkerdv1.ShellCommand{
				Stages:         []*clawkerdv1.PipeStage{userStage(sshKnownHostsScript)},
				InitialStdin:   []byte(defaultKnownHosts),
				TimeoutSeconds: initStepTimeoutDefault,
			},
		},
		shellStep{
			Name: "post-init",
			Shell: &clawkerdv1.ShellCommand{
				Stages:         []*clawkerdv1.PipeStage{userStage(postInitScript)},
				TimeoutSeconds: initStepTimeoutPostInit,
			},
		},
		agentReadyStep{Name: "agent-ready"},
	}
}

// userStage returns a fresh PipeStage running `sh -c <script>` as the
// unprivileged container user.
//
// HOME/USER override: clawkerd runs as root and spawns each stage
// with SysProcAttr.Credential to drop privileges, but the setuid
// syscall does NOT update HOME/USER env — they stay inherited from
// clawkerd (HOME=/root). Init scripts reference $HOME for config seed
// paths, gitconfig output, ssh known_hosts, post-init script
// location; without an explicit override they'd write to /root
// (permission denied). Any future ShellCommand dispatched with
// uid != 0 must do the same — clawkerd is a dumb pipe.
//
// Uid/Gid: consts.HostUID()/HostGID() — see internal/consts/controlplane.go.
// CP's os.Getuid() inside the CP container is the CP image's UID
// (typically 0), not the host invoker's.
func userStage(script string) *clawkerdv1.PipeStage {
	return &clawkerdv1.PipeStage{
		Argv: []string{"sh", "-c", script},
		Uid:  consts.HostUID(),
		Gid:  consts.HostGID(),
		Env: map[string]string{
			"HOME": consts.ContainerHomeDir,
			"USER": consts.ContainerUser,
		},
	}
}
